// Package hwmon reads Linux hwmon sensors straight out of sysfs.
//
// Why not a library: the Go options for hwmon are either unmaintained (the
// various `gosensors` forks all stopped between 2016 and 2022) or built for
// Prometheus-style metric export (elastic-agent-system-metrics' hwmon package
// exposes only temp/volt/fan as unsigned integers and drags in go-structform).
// The sysfs ABI is stable, small and documented in the kernel tree under
// Documentation/ABI/testing/sysfs-class-hwmon, so reading it directly is less
// code and strictly more capable than any of them.
package hwmon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Root is the sysfs hwmon class directory. It is a variable so tests can point
// it at a fixture tree.
var Root = "/sys/class/hwmon"

// Sensor kinds as they appear in sysfs filenames.
const (
	kindTemp    = "temp"
	kindFan     = "fan"
	kindVolt    = "in"
	kindCurrent = "curr"
	kindPower   = "power"
	kindEnergy  = "energy"
)

// Chip is one hwmon device, e.g. "coretemp" or "nct6798".
type Chip struct {
	// Name is the driver name from the chip's `name` file. It is not unique:
	// a machine with two NVMe drives publishes two chips both named "nvme".
	Name string
	// Key uniquely identifies this chip. It is the basename of the resolved
	// device path ("nvme0", "coretemp.0") and is what metric IDs are built
	// from, so two identically named chips never collide.
	Key string
	// Path is the resolved /sys/class/hwmon/hwmonN directory.
	Path string
	// DevicePath is the resolved path of the parent device the chip belongs
	// to, or "" for a chip with no parent (rare).
	DevicePath string
	// Driver is the name of the kernel driver behind the chip.
	Driver string
	// Sensors holds every readable channel on the chip.
	Sensors []Sensor
}

// Sensor is a single readable channel.
type Sensor struct {
	// Prefix is the sysfs filename prefix, e.g. "temp" or "in".
	Prefix string
	// Index is the channel number, e.g. 1 for temp1.
	Index int
	// Label is the friendly name from `<prefix><n>_label`, or "" when the
	// kernel does not provide one.
	Label string
	// Value is the reading converted to canonical units:
	//   temperature -> degrees Celsius
	//   fan         -> RPM
	//   voltage     -> volts
	//   current     -> amps
	//   power       -> watts
	//   energy      -> joules
	Value float64
	// Crit and Max are the driver reported thresholds, when present.
	Crit float64
	Max  float64
	Min  float64
	// HasCrit reports whether Crit carries a real threshold.
	HasCrit bool
	// Err is set when the channel exists but the kernel refused to return a
	// value (common: sensors that are present but idle).
	Err error
}

// Valid reports whether the channel produced a usable reading this pass.
func (s Sensor) Valid() bool { return s.Err == nil && !mathIsNaN(s.Value) }

func mathIsNaN(f float64) bool { return f != f }

// Name returns the best human label for the channel, falling back to the
// generic sysfs-derived name when the driver supplies no label.
func (s Sensor) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return defaultName(s.Prefix, s.Index)
}

func defaultName(prefix string, index int) string {
	base := strings.TrimSuffix(prefix, "put")
	switch prefix {
	case kindTemp:
		return fmt.Sprintf("Temp %d", index)
	case kindFan:
		return fmt.Sprintf("Fan %d", index)
	case kindVolt:
		return fmt.Sprintf("Volt %d", index)
	case kindCurrent:
		return fmt.Sprintf("Current %d", index)
	case kindPower:
		return fmt.Sprintf("Power %d", index)
	case kindEnergy:
		return fmt.Sprintf("Energy %d", index)
	default:
		return fmt.Sprintf("%s %d", base, index)
	}
}

// Unit returns the canonical unit of the channel.
func (s Sensor) Unit() string {
	switch s.Prefix {
	case kindTemp:
		return "°C"
	case kindFan:
		return "RPM"
	case kindVolt:
		return "V"
	case kindCurrent:
		return "A"
	case kindPower:
		return "W"
	case kindEnergy:
		return "J"
	default:
		return ""
	}
}

// Scale returns the divisor that converts a raw sysfs integer to canonical
// units, and the offset applied afterwards (needed for temperatures).
func Scale(prefix string) (div, off float64) {
	switch prefix {
	case kindTemp:
		return 1000, 0 // millidegrees
	case kindVolt:
		return 1000, 0 // millivolts
	case kindCurrent:
		return 1000, 0 // milliamps
	case kindPower:
		return 1e6, 0 // microwatts
	case kindEnergy:
		return 1e6, 0 // microjoules
	default: // fan RPM, and any integer count
		return 1, 0
	}
}

// prefixes are probed in this order; the resulting sensor list keeps that
// order so the UI shows temperatures before fans before voltages.
var prefixes = []string{kindTemp, kindFan, kindVolt, kindCurrent, kindPower, kindEnergy}

// Read enumerates every hwmon chip under Root and reads all of its channels.
//
// A chip that disappears mid-scan (USB thermal sensors being unplugged) is
// skipped rather than failing the whole pass, because a monitoring tool must
// not die because one device went away.
func Read() ([]Chip, error) {
	entries, err := os.ReadDir(Root)
	if err != nil {
		return nil, fmt.Errorf("hwmon: read %s: %w", Root, err)
	}

	caps := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		caps = append(caps, p+"*_input")
	}

	chips := make([]Chip, 0, len(entries))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "hwmon") {
			continue
		}
		// /sys/class/hwmon/hwmonN is a symlink into the device tree, so the
		// directory entry itself has the symlink type and IsDir reports false.
		// Stat the resolved path rather than trusting the entry.
		dir := filepath.Join(Root, e.Name())
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		chip, err := readChip(dir)
		if err != nil {
			continue
		}
		if len(chip.Sensors) == 0 {
			continue
		}
		chips = append(chips, chip)
	}

	sort.Slice(chips, func(i, j int) bool { return chips[i].Name < chips[j].Name })
	return chips, nil
}

func readChip(dir string) (Chip, error) {
	chip := Chip{Path: dir, Name: filepath.Base(dir)}
	if b, err := os.ReadFile(filepath.Join(dir, "name")); err == nil {
		chip.Name = strings.TrimSpace(string(b))
	}
	chip.DevicePath = resolveDevice(dir)
	chip.Key = chip.DeviceKey(dir)
	chip.Driver = driverOf(chip.DevicePath, chip.Name)

	for _, prefix := range prefixes {
		indices, err := channelIndices(dir, prefix)
		if err != nil {
			continue
		}
		for _, idx := range indices {
			chip.Sensors = append(chip.Sensors, readChannel(dir, prefix, idx))
		}
	}
	return chip, nil
}

// resolveDevice returns the real path of the device a chip belongs to, or ""
// when the chip has no device link.
func resolveDevice(hwmonDir string) string {
	target, err := filepath.EvalSymlinks(filepath.Join(hwmonDir, "device"))
	if err != nil {
		return ""
	}
	return target
}

// DeviceKey derives the chip's unique key: the basename of its device path,
// with the kernel's instance suffix ("nct6775.656" -> "nct6775", "coretemp.0"
// -> "coretemp") removed so the key stays stable across reboots. Chips with no
// device link fall back to their hwmon directory name.
func (c Chip) DeviceKey(hwmonDir string) string {
	if c.DevicePath == "" {
		return filepath.Base(hwmonDir)
	}
	base := filepath.Base(c.DevicePath)
	if i := strings.LastIndex(base, "."); i > 0 {
		if _, err := strconv.Atoi(base[i+1:]); err == nil {
			base = base[:i]
		}
	}
	// Two PCI devices can share a basename, e.g. two "nvme0" controllers on
	// different roots, so qualify with the PCI address when there is one.
	if pci := pciAddressOf(c.DevicePath); pci != "" {
		return base + "-" + pci
	}
	return base
}

// pciAddressOf extracts the "0000:03:00.0" component from a sysfs device path.
func pciAddressOf(devicePath string) string {
	for _, seg := range strings.Split(devicePath, string(filepath.Separator)) {
		parts := strings.Split(seg, ":")
		if len(parts) == 3 && len(parts[0]) == 4 && len(parts[1]) == 2 {
			if _, err := strconv.ParseUint(parts[0], 16, 16); err == nil {
				return seg
			}
		}
	}
	return ""
}

// driverOf resolves the kernel driver behind a chip.
//
// Not every chip has a `driver` symlink on its parent device: platform sensors
// such as coretemp expose none, and a chip parented to a thermal zone has a
// self-referential one. So this walks the parent device looking for a driver
// link, then falls back to the chip's own name.
func driverOf(devicePath, chipName string) string {
	if devicePath != "" {
		// Walk up a few levels: some chips hang off an intermediate device
		// (an MFD parent, a PCI bridge) that carries the driver link.
		cur := devicePath
		for i := 0; i < 3; i++ {
			if link, err := filepath.EvalSymlinks(filepath.Join(cur, "driver")); err == nil {
				return filepath.Base(link)
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	return chipName
}

// channelIndices finds the channel numbers present for a prefix, e.g. 1..4 for
// temp, by probing both `tempN_input` and `tempN_label`. Probing the label too
// matters because a few drivers ship the label but leave the value unreadable
// until the sensor spins up, and we still want the channel listed.
func channelIndices(dir, prefix string) ([]int, error) {
	seen := map[int]bool{}
	for _, suffix := range []string{"_input", "_label"} {
		matches, _ := filepath.Glob(filepath.Join(dir, prefix+"*"+suffix))
		for _, m := range matches {
			base := filepath.Base(m)
			digits := strings.TrimSuffix(strings.TrimPrefix(base, prefix), suffix)
			n, err := strconv.Atoi(digits)
			if err != nil {
				continue
			}
			seen[n] = true
		}
	}
	if len(seen) == 0 {
		return nil, fs.ErrNotExist
	}
	idx := make([]int, 0, len(seen))
	for n := range seen {
		idx = append(idx, n)
	}
	sort.Ints(idx)
	return idx, nil
}

func readChannel(dir, prefix string, index int) Sensor {
	s := Sensor{Prefix: prefix, Index: index}
	base := fmt.Sprintf("%s%d", prefix, index)

	if b, err := os.ReadFile(filepath.Join(dir, base+"_label")); err == nil {
		s.Label = strings.TrimSpace(string(b))
	}

	raw, err := readInt(filepath.Join(dir, base+"_input"))
	if err != nil {
		// Three different situations collapse into one read error, and they
		// must be handled differently:
		//
		//  * ENOENT with no label: the channel is not present at all. Skip
		//    it, so the panel does not list a sensor that does not exist.
		//  * ENOENT with a label, or a read failure such as ENODATA/EIO:
		//    the channel exists but the hardware will not answer yet. This
		//    is common - the iwlwifi temperature returns ENODATA until the
		//    radio has associated - so keep it listed but mark it invalid.
		//    Critically, do NOT fall through to a zero reading: an idle
		//    sensor reporting a confident 0C is worse than no sensor at all,
		//    because it drags every temperature threshold's baseline down.
		if !errors.Is(err, fs.ErrNotExist) || s.Label != "" {
			s.Err = err
		}
		return s
	}

	div, off := Scale(prefix)
	s.Value = float64(raw)/div + off

	if c, err := readInt(filepath.Join(dir, base+"_crit")); err == nil {
		s.Crit = float64(c)/div + off
		s.HasCrit = true
	}
	if m, err := readInt(filepath.Join(dir, base+"_max")); err == nil {
		s.Max = float64(m)/div + off
	}
	if m, err := readInt(filepath.Join(dir, base+"_min")); err == nil {
		s.Min = float64(m)/div + off
	}
	return s
}

// readInt reads a sysfs integer attribute. Values above 2^53 cannot occur for
// sensor attributes, so the int64 -> float64 conversion below is exact.
func readInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	// Strip a trailing newline without allocating a second buffer for the
	// common case of an already-trimmed value.
	s := strings.TrimSpace(string(b))
	return strconv.ParseInt(s, 10, 64)
}
