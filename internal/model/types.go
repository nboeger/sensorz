// Package model holds the shared data types used across sensorz. Collectors
// produce these values, the history store keeps them, and the UI renders them.
package model

import (
	"math"
	"time"
)

// Kind identifies what a metric measures. It drives units, formatting and the
// colour thresholds used by the UI.
type Kind string

const (
	KindTemperature Kind = "temperature" // degrees Celsius
	KindUtilization Kind = "utilization" // percent 0-100
	KindFrequency   Kind = "frequency"   // hertz
	KindPower       Kind = "power"       // watts
	KindFan         Kind = "fan"         // RPM
	KindVoltage     Kind = "voltage"     // volts
	KindCurrent     Kind = "current"     // amps
	KindBytes       Kind = "bytes"       // bytes/second
	KindCount       Kind = "count"       // dimensionless count
	KindClock       Kind = "clock"       // hertz (clock speed)
)

// Unit returns the canonical unit suffix for the kind.
func (k Kind) Unit() string {
	switch k {
	case KindTemperature:
		return "°C"
	case KindUtilization:
		return "%"
	case KindFrequency, KindClock:
		return "MHz"
	case KindPower:
		return "W"
	case KindFan:
		return "RPM"
	case KindVoltage:
		return "V"
	case KindCurrent:
		return "A"
	case KindBytes:
		return "B/s"
	default:
		return ""
	}
}

// String implements fmt.Stringer.
func (k Kind) String() string { return string(k) }

// Category groups metrics into panels.
type Category string

const (
	CategoryCPU     Category = "cpu"
	CategoryGPU     Category = "gpu"
	CategoryMemory  Category = "memory"
	CategoryDisk    Category = "disk"
	CategoryNetwork Category = "network"
	CategorySensor  Category = "sensor" // motherboard / fans / voltages
	CategoryProcess Category = "process"
)

// Metric is a single named, graphable time series. Every collector produces a
// set of these and they are the only thing the UI needs to draw a panel.
type Metric struct {
	// ID uniquely identifies the series across the whole session. It is used
	// as the history key and must be stable between samples.
	ID string
	// Label is what the user sees, e.g. "Package id 0" or "GPU 0".
	Label string
	// Group is the sub-heading a metric is listed under inside its panel,
	// e.g. "Core 0" or the hwmon chip name.
	Group    string
	Category Category
	Kind     Kind
	// Value is the current reading in canonical units (Celsius, percent,
	// watts...). Collectors normalise everything up front.
	Value float64
	// Max and Min are the vertical scale of the graph. A temperature has no
	// intrinsic maximum - a CPU dies somewhere below 150 and nowhere near it -
	// so these come from the sensor's own critical point rather than from a
	// table.
	Max float64
	Min float64
	// Thresholds drive the green -> yellow -> red colouring.
	Warn float64
	Crit float64
	// Hint is a short note shown under the figure: for an average, how many
	// sensors went into it.
	Hint string
}

// Valid reports whether the metric carries a usable reading. Sensors such as
// the iwlwifi temperature return EINVAL until the radio is up; those samples
// must not create graph discontinuities.
func (m Metric) Valid() bool { return !math.IsNaN(m.Value) }

// Device is one GPU (or other accelerator) exposed by the gpu collector.
type Device struct {
	Index      int
	Name       string
	Vendor     string
	Driver     string
	PciAddress string
	// FanPercent is the card's fan duty cycle, or -1 where the driver will not
	// say.
	FanPercent  float64
	Temperature float64 // Celsius, NaN when the driver exposes no sensor
	// ExtraTemps are the card's other temperature sensors: an NVIDIA card has a
	// hotspot and a memory junction reading beside its GPU temperature.
	ExtraTemps []ExtraTemp
	// Err records why this device could not be refreshed this tick.
	Err string
}

// ExtraTemp is an auxiliary temperature reading on an accelerator.
type ExtraTemp struct {
	Label string
	Value float64
	Max   float64
}

// HasTemperature reports whether the device actually produced a usable
// temperature this tick.
//
// A card whose driver exposes no thermal sensor leaves Temperature as NaN
// rather than 0, because a zero would render as a plausible - if alarming -
// temperature instead of "no sensor". The UI keys its GPU panel off this, so
// a machine whose only GPU cannot be read still gets a clean panel.
func (d Device) HasTemperature() bool {
	t := d.Temperature
	return t == t && t > -50 && t < 200
}

// Snapshot is the complete result of one collection pass.
type Snapshot struct {
	Time time.Time
	// Cores is the number of logical CPUs, used to label the CPU average.
	Cores    int
	Metrics  []Metric
	Devices  []Device
	Warnings []string
}

// MetricsByCategory returns the metrics belonging to a category, preserving
// the order the collector produced them in.
func (s Snapshot) MetricsByCategory(c Category) []Metric {
	out := make([]Metric, 0, 16)
	for i := range s.Metrics {
		if s.Metrics[i].Category == c {
			out = append(out, s.Metrics[i])
		}
	}
	return out
}

// FormatValue renders a reading with the unit and precision its kind deserves.
// Precision is chosen so the number keeps a stable width between frames,
// which stops the UI from jittering as values change.
func FormatValue(k Kind, v float64) string {
	switch k {
	case KindTemperature:
		return sprintf("%.0f°C", v)
	case KindUtilization:
		return sprintf("%.0f%%", v)
	case KindFrequency, KindClock:
		return sprintf("%.0fMHz", shortHz(v*1e6))
	case KindPower:
		if v >= 100 {
			return sprintf("%.0fW", v)
		}
		return sprintf("%.1fW", v)
	case KindFan:
		return sprintf("%.0fRPM", v)
	case KindVoltage:
		return sprintf("%.2fV", v)
	case KindCurrent:
		return sprintf("%.2fA", v)
	case KindBytes:
		return HumanBytes(v) + "/s"
	default:
		return sprintf("%.0f", v)
	}
}

// FormatValueCompact is FormatValue without the unit, for tight columns where
// the unit is printed once in the panel header.
func FormatValueCompact(k Kind, v float64) string {
	switch k {
	case KindVoltage, KindCurrent:
		return sprintf("%.2f", v)
	case KindPower:
		if v >= 100 {
			return sprintf("%.0f", v)
		}
		return sprintf("%.1f", v)
	case KindBytes:
		// Raw byte counts are unreadable in a narrow column and would blow
		// the graph label out to eleven digits.
		return HumanBytes(v)
	case KindCount:
		return HumanBytes(v)
	default:
		return sprintf("%.0f", v)
	}
}
