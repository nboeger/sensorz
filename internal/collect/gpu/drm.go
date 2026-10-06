package gpu

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nathan/sensorz/internal/model"
)

const drmRoot = "/sys/class/drm"

// drmBackend covers every driver that exposes telemetry through sysfs: amdgpu,
// recent i915, nouveau, radeon, panfrost and the SoC GPUs. The proprietary
// NVIDIA driver is not among them, which is why NVML exists as a separate
// backend.
type drmBackend struct {
	cards []drmCard
}

type drmCard struct {
	// Dir is /sys/class/drm/cardN, the symlink into the PCI device.
	Dir string
	// Dev is the resolved /sys/class/drm/cardN/device path.
	Dev    string
	Name   string
	Vendor string
	Driver string
	Pci    string
	// hwmon is the resolved hwmon directory, "" when the driver exposes none.
	hwmon string
	index int
}

// newDRMBackend enumerates DRM cards that actually carry telemetry. Cards
// whose driver exposes neither an hwmon directory nor a busy counter are left
// to the PCI backend, which lists them without values.
func newDRMBackend() *drmBackend {
	entries, err := os.ReadDir(drmRoot)
	if err != nil {
		return nil
	}

	b := &drmBackend{}
	for _, e := range entries {
		name := e.Name()
		// Skip connectors (card0-DP-1, card0-HDMI-A-1) and the version file.
		if !strings.HasPrefix(name, "card") || strings.Contains(name, "-") {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimPrefix(name, "card"))
		if err != nil {
			continue
		}
		dir := filepath.Join(drmRoot, name)
		dev, err := filepath.EvalSymlinks(filepath.Join(dir, "device"))
		if err != nil {
			continue
		}
		c := drmCard{Dir: dir, Dev: dev, index: idx, Name: name}
		c.Driver = basename(readFile(filepath.Join(dev, "driver")))
		c.Vendor = vendorOf(dev)
		c.Pci = basename(dev) // 0000:0a:00.0
		c.hwmon = resolveHwmon(dev)

		// A card is worth owning only if it can tell us something.
		if c.hwmon == "" && !fileExists(filepath.Join(dev, "gpu_busy_percent")) {
			continue
		}
		b.cards = append(b.cards, c)
	}
	if len(b.cards) == 0 {
		return nil
	}
	sort.Slice(b.cards, func(i, j int) bool { return b.cards[i].index < b.cards[j].index })
	return b
}

func (b *drmBackend) Name() string { return BackendDRM }

// Collect refreshes each DRM-backed card.
func (b *drmBackend) Collect(ctx context.Context) ([]model.Device, error) {
	out := make([]model.Device, 0, len(b.cards))
	for _, c := range b.cards {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		d := model.Device{
			Index:      c.index,
			Name:       c.Name,
			Vendor:     c.Vendor,
			Driver:     c.Driver,
			PciAddress: c.Pci,
			// "No sensor yet" rather than a zero: a driver that exposes
			// telemetry but no temperature must not draw a 0C graph.
			Temperature: math.NaN(),
		}

		// amdgpu and i915 both export gpu_busy_percent as a whole-device
		// utilization percentage.
		if v, err := readFloat(filepath.Join(c.Dev, "gpu_busy_percent")); err == nil {
			d.UtilPercent = v
		}

		if c.hwmon != "" {
			// temp1_input is the main reading; any other channel with a label
			// is an auxiliary reading (edge, junction, memory).
			main, extras := hwmonTemps(c.hwmon)
			if main.Value == main.Value {
				d.Temperature = main.Value
			}
			d.ExtraTemps = extras
			if p, err := readFloat(filepath.Join(c.hwmon, "power1_average")); err == nil {
				d.PowerTotal = p
			} else if p, err := readFloat(filepath.Join(c.hwmon, "power1_input")); err == nil {
				d.PowerTotal = p
			}
			if cap, err := readFloat(filepath.Join(c.hwmon, "power1_cap")); err == nil {
				d.PowerLimit = cap / 1e6 // microwatts to watts
			}
			if used, total, ok := amdgpuVRAM(c.Dev); ok {
				d.MemoryUsed, d.MemoryTotal = used, total
				if total > 0 {
					d.MemUtilPct = used / total * 100
				}
			}
			if cl, err := readFloat(filepath.Join(c.hwmon, "pwm1")); err == nil {
				// amdgpu exports fan speed as a 0..255 duty cycle; convert it
				// to a percentage so the UI does not have to care.
				d.FanPercent = cl / 255 * 100
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// resolveHwmon finds a card's hwmon directory. Drivers expose it either as
// device/hwmon/hwmonN (amdgpu, i915) or, for some, directly as the hwmon
// sibling, so both layouts are probed.
func resolveHwmon(dev string) string {
	for _, pattern := range []string{
		filepath.Join(dev, "hwmon", "hwmon[0-9]*"),
		filepath.Join(dev, "device", "hwmon", "hwmon[0-9]*"),
	} {
		if m, err := filepath.Glob(pattern); err == nil && len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// hwmonTemps reads every temperature channel on a card's hwmon directory. It
// returns the main reading (the unlabelled temp1, which is what every GPU
// driver uses for the headline temperature) separately from the auxiliary
// readings, so the caller can graph the main one and list the rest.
func hwmonTemps(dir string) (main model.ExtraTemp, extras []model.ExtraTemp) {
	matches, err := filepath.Glob(filepath.Join(dir, "temp*_input"))
	if err != nil {
		return main, nil
	}
	// No readable channel at all is reported as NaN rather than zero, so the
	// caller can tell "no sensor" from "a sensor reading 0C".
	main = model.ExtraTemp{Value: math.NaN()}
	for _, path := range matches {
		milli, err := readFloat(path)
		if err != nil {
			continue
		}
		key := strings.TrimSuffix(filepath.Base(path), "_input") // "temp1"
		label := readFile(filepath.Join(dir, key+"_label"))

		var crit float64
		if v, err := readFloat(filepath.Join(dir, key+"_crit")); err == nil {
			crit = v / 1000
		}

		if key == "temp1" && label == "" {
			main = model.ExtraTemp{Label: "GPU", Value: milli / 1000, Max: crit}
			continue
		}
		maxT := crit
		if maxT <= 0 {
			maxT = 100
		}
		if label == "" {
			label = key
		}
		extras = append(extras, model.ExtraTemp{Label: label, Value: milli / 1000, Max: maxT})
	}
	return main, extras
}

// amdgpuVRAM reads total and used VRAM from the amdgpu-specific
// mem_info_vram_total / mem_info_vram_used attributes, expressed in bytes.
func amdgpuVRAM(dev string) (used, total float64, ok bool) {
	total, err := readFloat(filepath.Join(dev, "mem_info_vram_total"))
	if err != nil || total <= 0 {
		return 0, 0, false
	}
	used, err = readFloat(filepath.Join(dev, "mem_info_vram_used"))
	if err != nil {
		used = 0
	}
	return used, total, true
}

func vendorOf(dev string) string {
	// The vendor file holds the 0x1002 style PCI vendor id; map the common
	// ones to readable names so the UI can say "AMD" rather than "0x1002".
	switch readFile(filepath.Join(dev, "vendor")) {
	case "0x1002", "0x1022":
		return "AMD"
	case "0x10de":
		return "NVIDIA"
	case "0x8086":
		return "Intel"
	case "0x1af4", "0x15ad":
		return "VMware"
	case "0x1234":
		return "QEMU"
	case "0x80ee":
		return "VirtualBox"
	default:
		return strings.TrimPrefix(readFile(filepath.Join(dev, "vendor")), "0x")
	}
}

func readFloat(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func basename(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}
