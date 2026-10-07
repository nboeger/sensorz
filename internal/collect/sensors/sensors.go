// Package sensors turns raw hwmon channels into the metric series the UI
// graphs, applying per-chip filtering so the sensor panel stays readable.
package sensors

import (
	"strings"
	"sync"
	"time"

	"github.com/nathan/sensorz/internal/collect/hwmon"
	"github.com/nathan/sensorz/internal/model"
)

// Collector reads hwmon and normalises it into metrics.
type Collector struct {
	mu    sync.Mutex
	chips []hwmon.Chip
	// lastScan is when the chip list was last enumerated. The values are read
	// every tick; only the list is expensive enough to be worth caching, and
	// only rarely enough to be worth refreshing.
	lastScan time.Time
	filter   Filter
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
// The chip list is cached and re-enumerated once a minute: which devices exist
// is stable for the life of the process unless someone plugs in a USB thermal
// sensor, while their values change every tick and are always read. A sensor
// plugged in after startup therefore appears within a minute without a restart.
func (c *Collector) Collect() ([]model.Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-enumerate the chip list on a slow cadence so a USB thermal sensor
	// plugged in after startup turns up, but do not pay for a full re-read on
	// every tick. Between enumerations we reuse the cached list and read only
	// the values, which is what makes a two second sample cheap.
	if c.chips == nil || time.Since(c.lastScan) > rescanInterval {
		chips, err := hwmon.Read()
		if err != nil {
			return nil, err
		}
		c.chips, c.lastScan = chips, time.Now()
	}

	out := make([]model.Metric, 0, 64)

	// Two chips can share a name - two NVMe drives both publish a chip called
	// "nvme" - so disambiguate the display label before building metrics.
	// Without this the panel shows two rows called "nvme" with different
	// temperatures and no way to tell which drive is which.
	labels := chipLabels(c.chips)

	for _, chip := range c.chips {
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
				// The channel exists but the kernel will not produce a value
				// for it yet - a wifi radio that has not associated, a drive
				// asleep. Leaving it out keeps a dead channel from drawing an
				// empty graph; the value simply is not there this tick.
				continue
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// rescanInterval is how often the hwmon chip list is re-enumerated. Long enough
// that the enumeration is noise, short enough that plugging in a sensor does not
// mean restarting the dashboard to see it.
const rescanInterval = time.Minute

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

// friendlyLabels maps raw hwmon label strings (lowercased) to display names
// that a user can understand without a datasheet.
//
// These come from two chip families. NCT677x/678x/679x Super I/O chips appear
// on nearly every consumer and workstation Intel/AMD board; they expose a set
// of fixed label names regardless of what is actually wired to each pin. The
// NVMe standard defines secondary sensor slots whose purpose varies by drive
// firmware, so only the ones with a consistent meaning are renamed here.
var friendlyLabels = map[string]string{
	// NCT677x/678x/679x — motherboard thermistors
	"systin":         "Ambient",
	"cputin":         "Near CPU",
	"auxtin0":        "Probe 1",
	"auxtin1":        "Probe 2",
	"auxtin2":        "Probe 3",
	"auxtin3":        "Probe 4",
	"auxtin4":        "Probe 5",
	"peci agent 0":   "CPU (PECI)",
	"pch_chip_temp":  "Chipset",
}

// friendlyLabel returns a display-ready name for a raw hwmon label, falling
// back to the original when no mapping exists.
func friendlyLabel(label string) string {
	if renamed, ok := friendlyLabels[strings.ToLower(label)]; ok {
		return renamed
	}
	return label
}

// metricFor converts one hwmon channel into a metric, reporting whether it
// should be shown at all.
func (c *Collector) metricFor(chip hwmon.Chip, chipLabel string, s hwmon.Sensor) (model.Metric, string, bool) {
	label := s.Name()
	if c.excluded(chip.Name, label) {
		return model.Metric{}, "", false
	}
	label = friendlyLabel(label)

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
