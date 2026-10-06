// Package disk collects per-device temperatures from hwmon.
//
// Throughput is deliberately not collected: sensorz reports temperature and
// fan speed, so the only reason to know a block device exists at all is to
// show how hot it is running.
package disk

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/prometheus/procfs/blockdevice"

	"github.com/nathan/sensorz/internal/collect/hwmon"
	"github.com/nathan/sensorz/internal/model"
)

// Collector resolves the machine's block devices and their temperatures.
type Collector struct {
	fs blockdevice.FS
}

// New builds a disk collector rooted at the given /proc mount point. procfs
// requires both the proc and sys mount points because it reads /proc/diskstats
// for the device list and /sys/block for the device attributes.
func New(procPath string) (*Collector, error) {
	fs, err := blockdevice.NewFS(procPath, "/sys")
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}
	return &Collector{fs: fs}, nil
}

// Device is one block device.
type Device struct {
	Name string
	// Rotational is true for spinning media, which we label differently in
	// the UI because its temperature profile differs.
	Rotational bool
	// Temperature is the drive temperature in Celsius. It is only meaningful
	// when HasTemp is set.
	Temperature float64
	// HasTemp reports whether the drive exposes a temperature sensor at all.
	// An explicit flag rather than a NaN sentinel, because a zero-valued
	// Device would otherwise look like a drive reading a plausible 0C instead
	// of one with no sensor.
	HasTemp bool
	// TempCritical is the drive's reported critical temperature when known.
	TempCritical float64
	// ExtraTemps are the drive's secondary sensors. NVMe drives commonly
	// expose a NAND temperature that runs far hotter than the composite
	// reading, and it is the number that predicts throttling.
	ExtraTemps []model.ExtraTemp
}

// Sample is one disk measurement pass.
type Sample struct {
	Devices []Device
}

// Collect resolves the block devices and reads their temperatures.
func (c *Collector) Collect() (Sample, error) {
	stats, err := c.fs.ProcDiskstats()
	if err != nil {
		return Sample{}, fmt.Errorf("disk: read /proc/diskstats: %w", err)
	}

	// Drive temperatures come from hwmon and are cheap, so read them every
	// pass rather than trying to correlate them with the diskstats snapshot.
	temps := nvmeTemps()

	var out Sample
	for _, s := range stats {
		// Skip partitions and virtual devices: a user cares about nvme0n1 and
		// sda, not about every mounted slice, and both entries pointing at
		// the same drive would double count it.
		name := s.DeviceName
		if isVirtual(name) {
			continue
		}
		t, ok := temps[name]
		if !ok {
			// A drive with no temperature sensor has nothing to show here.
			continue
		}
		out.Devices = append(out.Devices, Device{
			Name:         name,
			Rotational:   rotational(name),
			HasTemp:      true,
			Temperature:  t.value,
			TempCritical: t.critical,
			ExtraTemps:   t.extra,
		})
	}
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].Name < out.Devices[j].Name })
	return out, nil
}

// isVirtual filters out partitions, loop, ram, zram and other non-physical
// block devices so the disk panel lists real drives only.
func isVirtual(name string) bool {
	base := filepath.Base(name)
	for _, p := range []string{"loop", "ram", "zram", "dm-", "sr", "md", "fd", "nbd", "bcache"} {
		if strings.HasPrefix(base, p) {
			return true
		}
	}
	// Partition suffixes: "nvme0n1p2", "mmcblk0p1", "sda3", ...
	if i := strings.LastIndex(base, "p"); i > 0 {
		if _, err := strconv.Atoi(base[i+1:]); err == nil {
			return true
		}
	}
	// NVMe namespaces such as "nvme0n1" are whole devices, so stop here;
	// their trailing digits are the namespace id, not a partition number.
	if strings.HasPrefix(base, "nvme") {
		return false
	}
	// In the sd/hd/vd/xvd families the disk is a letter run ending in one
	// letter ("sda"), so a trailing digit is a partition number ("sda1").
	// Other families embed digits in the device name itself - "mmcblk0",
	// "mmcblk1" - and applying the rule to them would drop every eMMC and SD
	// card from the panel, so those families are matched explicitly instead.
	for _, fam := range []string{"sd", "hd", "vd", "xvd"} {
		if !strings.HasPrefix(base, fam) {
			continue
		}
		// "sda" is a whole disk, "sda1" a partition: exactly one letter
		// followed by digits.
		if diskPartition.MatchString(base[len(fam):]) {
			return true
		}
		return false
	}
	return false
}

// diskPartition matches the partition suffix of a disk name: one letter then
// digits, as in "a1" for "sda1".
var diskPartition = regexp.MustCompile(`^[a-z][0-9]+$`)

func rotational(name string) bool {
	b, err := os.ReadFile(filepath.Join("/sys/block", filepath.Base(name), "queue/rotational"))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == "1"
}

// tempReading is a drive temperature plus its critical threshold and any
// secondary sensors.
type tempReading struct {
	value    float64
	critical float64
	extra    []model.ExtraTemp
}

// nvmeTemps reads drive temperatures out of the hwmon class.
//
// The NVMe driver publishes one hwmon chip per controller, named "nvme". The
// chip's device symlink and the block device's device symlink both resolve to
// the same PCI node, so matching the resolved paths associates each temperature
// with the right drive without having to decode PCI slot numbers.
func nvmeTemps() map[string]tempReading {
	chips, err := hwmon.Read()
	if err != nil {
		return nil
	}
	blockDevs := nvmeBlockDevices()
	if len(blockDevs) == 0 {
		return nil
	}

	out := map[string]tempReading{}
	for _, chip := range chips {
		if chip.Name != "nvme" {
			continue
		}
		chipDev, err := filepath.EvalSymlinks(filepath.Join(chip.Path, "device"))
		if err != nil {
			continue
		}
		name, ok := blockDevs[chipDev]
		if !ok {
			continue
		}
		var composite float64
		var found bool
		var crit float64
		var extra []model.ExtraTemp
		for _, s := range chip.Sensors {
			if s.Prefix != "temp" {
				continue
			}
			// "Composite" is the drive's headline temperature; the numbered
			// "Sensor N" entries are internal hot spots, and the hottest of
			// them is what actually predicts a thermal throttle.
			if s.Valid() && strings.EqualFold(s.Label, "Composite") {
				composite = s.Value
				found = true
				continue
			}
			if s.Valid() && s.Label != "" {
				maxT := s.Crit
				if maxT <= 0 || maxT > 200 {
					maxT = 100
				}
				extra = append(extra, model.ExtraTemp{Label: s.Label, Value: s.Value, Max: maxT})
			}
			if s.HasCrit && s.Crit > crit && s.Crit < 200 {
				crit = s.Crit
			}
		}
		if !found {
			continue
		}
		out[name] = tempReading{value: composite, critical: crit, extra: extra}
	}
	return out
}

// nvmeBlockDevices maps the resolved PCI device path of each NVMe namespace to
// its kernel name (nvme0n1, nvme1n1, ...). Partitions are skipped: they share
// a temperature with their parent namespace.
func nvmeBlockDevices() map[string]string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "nvme") || strings.Contains(name, "p") {
			continue
		}
		dev, err := filepath.EvalSymlinks(filepath.Join("/sys/block", name, "device"))
		if err != nil {
			continue
		}
		out[dev] = name
	}
	return out
}

// Metrics returns the temperature series per device.
func (s Sample) Metrics() []model.Metric {
	var out []model.Metric
	for _, d := range s.Devices {
		kind := "SSD"
		warn, crit := 55.0, 70.0
		if d.Rotational {
			kind, warn, crit = "HDD", 42.0, 50.0
		}
		if d.HasTemp {
			w := warn
			c := crit
			if d.TempCritical > 0 {
				w, c = d.TempCritical*0.8, d.TempCritical*0.95
			}
			out = append(out, model.Metric{
				ID:       "disk." + d.Name + ".temp",
				Label:    d.Name,
				Group:    kind + " Temp",
				Category: model.CategoryDisk,
				Kind:     model.KindTemperature,
				Value:    d.Temperature,
				Min:      20,
				Max:      100,
				Warn:     w,
				Crit:     c,
			})
			for _, t := range d.ExtraTemps {
				out = append(out, model.Metric{
					ID:       "disk." + d.Name + ".temp." + strings.ToLower(strings.ReplaceAll(t.Label, " ", "")),
					Label:    d.Name,
					Group:    t.Label,
					Category: model.CategoryDisk,
					Kind:     model.KindTemperature,
					Value:    t.Value,
					Min:      20,
					Max:      t.Max,
					Warn:     t.Max * 0.85,
					Crit:     t.Max * 0.95,
				})
			}
		}
	}
	return out
}
