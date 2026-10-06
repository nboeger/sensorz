package model

import (
	"math"
	"testing"
)

func TestFormatValueKeepsAStableWidth(t *testing.T) {
	// Values are rendered without units in narrow columns, and the column must
	// not jitter as the number changes, so precision is chosen per kind rather
	// than by a blanket default.
	cases := []struct {
		kind Kind
		v    float64
		want string
	}{
		{KindTemperature, 42.4, "42°C"},
		{KindTemperature, 42.6, "43°C"},
		{KindUtilization, 99.5, "100%"},
		{KindPower, 17.03, "17.0W"},
		{KindPower, 200, "200W"},
		{KindFan, 1404, "1404RPM"},
		{KindVoltage, 1.254, "1.25V"},
		{KindCurrent, 0.75, "0.75A"},
	}
	for _, tc := range cases {
		if got := FormatValue(tc.kind, tc.v); got != tc.want {
			t.Errorf("FormatValue(%v, %v) = %q, want %q", tc.kind, tc.v, got, tc.want)
		}
	}
}

func TestFormatValueBytesIsHuman(t *testing.T) {
	// A raw byte count would be an eleven digit number in a narrow column.
	got := FormatValueCompact(KindBytes, 9_470_000)
	if got != "9.03MB" {
		t.Errorf("FormatValueCompact(bytes) = %q, want 9.03MB", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.00KB"},
		{1536, "1.50KB"},
		{1024 * 1024, "1.00MB"},
		{125.5 * (1 << 30), "126GB"}, // binary GiB rendered as decimal GB
		{-1024, "-1.00KB"},
	}
	for _, tc := range cases {
		if got := HumanBytes(tc.in); got != tc.want {
			t.Errorf("HumanBytes(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A graph is only plottable if its ceiling is above its floor; a metric with a
// zero-width range would divide by zero in the renderer.
func TestMetricPercentHandlesDegenerateRange(t *testing.T) {
	m := Metric{Min: 0, Max: 0, Value: 5}
	if got := m.Percent(); got != 0 {
		t.Errorf("Percent with a zero-width range = %v, want 0", got)
	}
	// Values outside the range clamp rather than overflowing the panel.
	if got := (Metric{Min: 0, Max: 10, Value: 99}).Percent(); got != 1 {
		t.Errorf("Percent above the ceiling = %v, want 1", got)
	}
	if got := (Metric{Min: 0, Max: 10, Value: -5}).Percent(); got != 0 {
		t.Errorf("Percent below the floor = %v, want 0", got)
	}
}

func TestValidRejectsNaN(t *testing.T) {
	// A NaN reading means the sensor produced no value; it must not be
	// recorded as a data point or the graph grows a false spike.
	if (Metric{Value: nan()}).Valid() {
		t.Error("a NaN reading was accepted as valid")
	}
	if !(Metric{Value: 42}).Valid() {
		t.Error("a real reading was rejected")
	}
}

// A sensor that has gone away must leave a gap in the graph, not a repeated
// value; the store turns an absent metric into NaN for exactly this reason.
func TestMetricsByCategoryPreservesOrder(t *testing.T) {
	s := Snapshot{Metrics: []Metric{
		{ID: "cpu.1", Category: CategoryCPU},
		{ID: "gpu.1", Category: CategoryGPU},
		{ID: "cpu.2", Category: CategoryCPU},
		{ID: "mem.1", Category: CategoryMemory},
	}}
	got := s.MetricsByCategory(CategoryCPU)
	if len(got) != 2 {
		t.Fatalf("got %d cpu metrics, want 2", len(got))
	}
	if got[0].ID != "cpu.1" || got[1].ID != "cpu.2" {
		t.Errorf("order = %q,%q, want cpu.1,cpu.2", got[0].ID, got[1].ID)
	}
}

func TestKindUnits(t *testing.T) {
	cases := map[Kind]string{
		KindTemperature: "°C",
		KindUtilization: "%",
		KindPower:       "W",
		KindFan:         "RPM",
		KindBytes:       "B/s",
		KindCount:       "",
	}
	for kind, want := range cases {
		if got := kind.Unit(); got != want {
			t.Errorf("%q.Unit() = %q, want %q", kind, got, want)
		}
	}
}

func nan() float64 { return math.NaN() }
