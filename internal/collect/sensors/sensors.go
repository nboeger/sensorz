// Package sensors turns raw hwmon channels into the metric series the UI
// graphs, applying per-chip filtering so the sensor panel stays readable.
package sensors

import (
	"sort"
	"strings"
	"sync"

	"github.com/nathan/sensorz/internal/collect/hwmon"
	"github.com/nathan/sensorz/internal/model"
)

// Collector reads hwmon and normalises it into metrics.
type Collector struct {
	mu      sync.Mutex
	chips   []hwmon.Chip
	scanned bool
	filter  Filter
}

// Filter decides which channels make it into the UI.
type Filter struct {
	// ExcludeChips lists chip names to skip entirely, e.g. "acpitz" whose
	// reading merely mirrors the ACPI board sensor already shown.
	ExcludeChips map[string]bool
	// ExcludeChannels lists "<chip>/<label>" entries to skip. These are
	// mostly the fake sensors Super Micro motherboards expose: NCT6798
	// boards register dozens of "voltage" inputs that are permanently stuck
	// at their maximum, which is noise rather than information.
	ExcludeChannels map[string]bool
	// MaxChannelsPerChip caps how many channels of one kind are listed per
	// chip, keeping a 32-core CPU from swamping the panel.
	MaxChannelsPerChip int
}

// DefaultFilter returns a filter that shows the temperatures and fans worth
// looking at, and nothing else: sensorz reports temperature and fan speed, so
// the voltage, current and power channels a board exposes are noise here.
func DefaultFilter() Filter {
	f := Filter{
		ExcludeChips:    map[string]bool{"acpitz": true},
		ExcludeChannels: map[string]bool{},
		// Voltages and currents are never collected: an NCT6798 board
		// registers 14 voltage inputs and 7 current inputs, nearly all of
		// which read their unpopulated default and never change.
		MaxChannelsPerChip: 24,
	}
	for _, ch := range defaultChannelExclusions {
		f.ExcludeChannels[ch] = true
	}
	return f
}

// defaultChannelExclusions are channels that are present on the board's wiring
// diagram but not actually populated, so they sit at their unpopulated default
// forever. They are excluded by default and shown under -all-sensors, because
// the honest thing is to hide a reading that carries no information while still
// letting the user see it if they know their board.
//
// The Super-I/O firmware exposes these PCH (Platform Controller Hub) channels
// on every board using an NCT677x, whether or not a thermistor is wired to
// them. A real NTC thermistor at room temperature never reads exactly 0.0C, so
// a fixed zero is always the unpopulated default, never a real measurement.
//
// The PECI calibration channel is excluded for a different reason: it reports
// the offset the firmware applies to the CPU sensor, which is a number the
// board picked at boot rather than a temperature anything is sitting at.
var defaultChannelExclusions = []string{
	"nct6798/PCH_CHIP_CPU_MAX_TEMP",
	"nct6798/PCH_CPU_TEMP",
	"nct6775/PCH_CHIP_CPU_MAX_TEMP",
	"nct6775/PCH_CPU_TEMP",
	"nct6776/PCH_CHIP_CPU_MAX_TEMP",
	"nct6776/PCH_CPU_TEMP",
	"nct6798/PECI Agent 0 Calibration",
	"nct6775/PECI Agent 0 Calibration",
	"nct6776/PECI Agent 0 Calibration",
}

// New builds a sensor collector with the given filter.
func New(f Filter) *Collector {
	if f.ExcludeChips == nil {
		f.ExcludeChips = map[string]bool{}
	}
	if f.ExcludeChannels == nil {
		f.ExcludeChannels = map[string]bool{}
	}
	if f.MaxChannelsPerChip <= 0 {
		f.MaxChannelsPerChip = 24
	}
	return &Collector{filter: f}
}

// Collect reads hwmon and returns the filtered sensor metrics.
//
// Chip discovery is cached: the set of hwmon devices is stable for the life of
// the process unless the user hot-plugs a USB sensor, so we re-scan only when
// a channel fails to read. That keeps the hot path to a few hundred small
// file reads.
func (c *Collector) Collect() ([]model.Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var (
		chips []hwmon.Chip
		err   error
	)
	if c.scanned {
		chips = c.chips
	} else {
		chips, err = hwmon.Read()
		if err != nil {
			return nil, err
		}
		c.chips, c.scanned = chips, true
	}

	out := make([]model.Metric, 0, 64)
	var misses int

	// Two chips can share a name - two NVMe drives both publish a chip called
	// "nvme" - so disambiguate the display label before building metrics.
	// Without this the panel shows two rows called "nvme" with different
	// temperatures and no way to tell which drive is which.
	labels := chipLabels(c.chips)

	for _, chip := range chips {
		if c.filter.ExcludeChips[chip.Name] {
			continue
		}
		label := labels[chip.Path]
		perKind := map[string]int{}
		for _, s := range chip.Sensors {
			m, kind, ok := c.metricFor(chip, label, s)
			if !ok {
				continue
			}
			if perKind[kind] >= c.filter.MaxChannelsPerChip {
				continue
			}
			perKind[kind]++
			if !s.Valid() {
				// Keep the metric so the panel layout stays stable, but do
				// not record a value; the graph will show a gap.
				misses++
				continue
			}
			out = append(out, m)
		}
	}
	if misses > 0 && c.scanned {
		// A USB sensor may have been plugged in since the last scan; refresh
		// the device list once and let the next tick pick up the new chip.
		if fresh, ferr := hwmon.Read(); ferr == nil && len(fresh) != len(c.chips) {
			c.chips = fresh
		}
	}
	return out, nil
}

// excluded reports whether a channel should be hidden. Lookups are
// case-insensitive on both halves because sysfs labels vary in case between
// drivers ("PCH_CPU_TEMP" versus "PchCpuTemp").
func (c *Collector) excluded(chip, label string) bool {
	for _, key := range []string{chip + "/" + label, strings.ToLower(chip + "/" + label)} {
		if c.filter.ExcludeChannels[key] {
			return true
		}
	}
	return false
}

// chipLabels returns a display label per chip, appending a distinguishing
// suffix only where a chip name is ambiguous.
func chipLabels(chips []hwmon.Chip) map[string]string {
	counts := map[string]int{}
	for _, c := range chips {
		counts[c.Name]++
	}
	out := make(map[string]string, len(chips))
	for _, c := range chips {
		if counts[c.Name] == 1 {
			out[c.Path] = c.Name
			continue
		}
		// Use the trailing component of the key, which for these drivers is
		// the device name: "nvme0-0000:00:06.0" becomes "nvme0".
		suffix := c.Key
		if i := strings.Index(suffix, "-"); i > 0 {
			suffix = suffix[:i]
		}
		// "nvme0" already says "nvme", so do not render "nvme nvme0".
		if strings.HasPrefix(suffix, c.Name) {
			out[c.Path] = suffix
		} else {
			out[c.Path] = c.Name + " " + suffix
		}
	}
	return out
}

// metricFor converts one hwmon channel into a metric, reporting whether it
// should be shown at all.
func (c *Collector) metricFor(chip hwmon.Chip, chipLabel string, s hwmon.Sensor) (model.Metric, string, bool) {
	label := s.Name()
	if c.excluded(chip.Name, label) {
		return model.Metric{}, "", false
	}

	// The chip key, not the chip name, goes into the metric id: two NVMe
	// drives both publish a chip called "nvme", and keying on the name would
	// make them overwrite each other's history.
	id := "hwmon." + strings.ToLower(chip.Key) + "." + strings.ToLower(s.Prefix) + "." + itoa(s.Index)
	kind := kindOf(s.Prefix)
	if kind != model.KindTemperature && kind != model.KindFan {
		return model.Metric{}, "", false
	}

	m := model.Metric{
		ID:       id,
		Label:    label,
		Group:    chipLabel,
		Category: model.CategorySensor,
		Kind:     kind,
		Value:    s.Value,
	}
	applyDefaults(&m, s)
	return m, s.Prefix, true
}

// applyDefaults sets sensible graph bounds and warning thresholds. A channel's
// own crit/max wins when the driver reports one, because those are the real
// hardware limits rather than a guess.
func applyDefaults(m *model.Metric, s hwmon.Sensor) {
	switch m.Kind {
	case model.KindTemperature:
		m.Min = 20
		m.Max, m.Warn, m.Crit = 100, 70, 85
		if s.HasCrit && s.Crit > m.Min && s.Crit < 150 {
			m.Max = clampMax(s.Crit*1.1, 60, 120)
			m.Warn, m.Crit = s.Crit*0.85, s.Crit*0.97
		}
	case model.KindFan:
		m.Min, m.Max = 0, 6000
		if s.Max > 0 {
			m.Max = s.Max * 1.2
		}
		// A fan spinning harder is not a warning, so the fan graph has no
		// thresholds and stays in the healthy colour throughout.
		m.Warn, m.Crit = 0, 0
	default:
		m.Min, m.Max = 0, 100
	}
}

func clampMax(v, lo, hi float64) float64 { return model.Clamp(v, lo, hi) }

// ChipRank orders hwmon chips by whose panel a reading belongs to: 0 is the
// CPU, 1 is the motherboard and everything else, and 2 is a GPU or a drive,
// which get their own panels.
//
// Both the aggregator (which computes the CPU and GPU averages) and the UI
// (which decides which panel a chip's readings are drawn in) use this, so the
// classification lives here rather than being written twice.
func ChipRank(chip string) int {
	c := strings.ToLower(chip)
	switch {
	case strings.Contains(c, "coretemp") || strings.Contains(c, "k10temp") ||
		strings.Contains(c, "zenpower") || strings.Contains(c, "cpu_thermal") ||
		strings.Contains(c, "soc_thermal") || strings.HasPrefix(c, "x86_pkg_temp"):
		return 0
	case strings.HasPrefix(c, "nvme") || strings.HasPrefix(c, "drivetemp") ||
		strings.HasPrefix(c, "amdgpu") || strings.HasPrefix(c, "i915") ||
		strings.HasPrefix(c, "xe") || strings.HasPrefix(c, "nouveau") ||
		strings.HasPrefix(c, "radeon"):
		return 2
	default:
		return 1
	}
}

// IsCPUChip reports whether a chip reports the CPU's own temperatures. A
// machine with several of these (one per socket) is what makes the CPU
// average graph meaningful.
func IsCPUChip(chip string) bool { return ChipRank(chip) == 0 }

// IsGPUChip reports whether a chip belongs to a graphics adapter.
func IsGPUChip(chip string) bool {
	c := strings.ToLower(chip)
	for _, p := range []string{"amdgpu", "i915", "xe", "nouveau", "radeon", "gpu"} {
		if strings.HasPrefix(c, p) {
			return true
		}
	}
	return false
}

// IsDriveChip reports whether a chip reports a drive's temperature.
func IsDriveChip(chip string) bool {
	return strings.HasPrefix(strings.ToLower(chip), "nvme") ||
		strings.HasPrefix(strings.ToLower(chip), "drivetemp")
}

func kindOf(prefix string) model.Kind {
	switch prefix {
	case "temp":
		return model.KindTemperature
	case "fan":
		return model.KindFan
	case "in":
		return model.KindVoltage
	case "curr":
		return model.KindCurrent
	case "power":
		return model.KindPower
	case "energy":
		return model.KindCount
	default:
		return ""
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// SortMetrics orders metrics by category then label so the panel layout is
// stable between frames.
func SortMetrics(ms []model.Metric) {
	rank := map[model.Kind]int{
		model.KindTemperature: 0,
		model.KindFan:         1,
	}
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		return a.Label < b.Label
	})
}
