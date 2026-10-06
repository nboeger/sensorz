package collect

import (
	"testing"

	"github.com/nathan/sensorz/internal/model"
)

func temp(id, chip string, v float64) model.Metric {
	return model.Metric{
		ID: id, Label: chip + " temp", Group: chip,
		Category: model.CategorySensor, Kind: model.KindTemperature,
		Value: v, Min: 20, Max: 100, Warn: 70, Crit: 85,
	}
}

// A machine with several CPUs - which on any modern board means several
// temperature channels - gets one graph showing the average across them.
func TestCPUTempIsTheAverageAcrossSensors(t *testing.T) {
	snap := &model.Snapshot{
		Cores: 16,
		Metrics: []model.Metric{
			temp("hwmon.coretemp.1", "coretemp", 60),
			temp("hwmon.coretemp.2", "coretemp", 70),
			temp("hwmon.coretemp.3", "coretemp", 80),
			// A motherboard sensor must not drag the CPU average around.
			temp("hwmon.nct6798.1", "nct6798", 30),
		},
	}
	derive(snap)

	avg := find(t, snap, MetricCPUTemp)
	if avg.Value != 70 {
		t.Errorf("cpu average = %v, want 70", avg.Value)
	}
	if avg.Category != model.CategoryCPU {
		t.Errorf("category = %q, want cpu", avg.Category)
	}
	// The label has to say how many CPUs this covers, or a flat line on a big
	// machine looks like a single reading.
	if avg.Label != "CPU ×16" {
		t.Errorf("label = %q, want the core count in it", avg.Label)
	}
	// Thresholds follow the sensors' own critical temperature, not a guess.
	if avg.Crit != 85 || avg.Warn != 85*0.85 {
		t.Errorf("thresholds warn=%v crit=%v, want 85%% and 100%% of 85C", avg.Warn, avg.Crit)
	}
	if avg.Max <= avg.Crit {
		t.Errorf("graph ceiling %v must sit above the critical point %v", avg.Max, avg.Crit)
	}
}

func TestSingleCPUIsNotLabelledAsMany(t *testing.T) {
	snap := &model.Snapshot{
		Cores:   1,
		Metrics: []model.Metric{temp("hwmon.coretemp.1", "coretemp", 55)},
	}
	derive(snap)
	if got := find(t, snap, MetricCPUTemp); got.Label != "CPU" {
		t.Errorf("label = %q, want plain CPU on a one core machine", got.Label)
	}
}

// With no CPU sensor at all there is no average to graph, and emitting a
// zeroed one would draw a plausible but fictional line.
func TestCPUTempAverageOmittedWithoutSensors(t *testing.T) {
	snap := &model.Snapshot{Cores: 8, Metrics: []model.Metric{temp("hwmon.nct6798.1", "nct6798", 30)}}
	derive(snap)
	if has(snap, MetricCPUTemp) {
		t.Error("a cpu average was published with no CPU sensors behind it")
	}
}

// Several GPUs collapse into one graph, and cards whose driver exposed no
// temperature are left out of the average entirely.
func TestGPUTempAveragesOnlyReadableCards(t *testing.T) {
	snap := &model.Snapshot{Devices: []model.Device{
		{Index: 0, Name: "first", Temperature: 60},
		{Index: 1, Name: "second", Temperature: 80},
		{Index: 2, Name: "silent", Temperature: nan()},
	}}
	derive(snap)

	avg := find(t, snap, MetricGPUTemp)
	if avg.Value != 70 {
		t.Errorf("gpu average = %v, want 70", avg.Value)
	}
	if avg.Label != "GPU ×2" {
		t.Errorf("label = %q, want the card count", avg.Label)
	}
}

func TestFanAverageAcrossEveryFan(t *testing.T) {
	snap := &model.Snapshot{
		Metrics: []model.Metric{
			{ID: "hwmon.a.1", Group: "nct6798", Category: model.CategorySensor, Kind: model.KindFan, Value: 1000, Max: 2000},
			{ID: "hwmon.a.2", Group: "nct6798", Category: model.CategorySensor, Kind: model.KindFan, Value: 3000, Max: 2000},
		},
	}
	derive(snap)

	avg := find(t, snap, MetricFanAvg)
	if avg.Value != 2000 {
		t.Errorf("fan average = %v, want 2000", avg.Value)
	}
	// The graph is scaled to the fastest fan the chips declare.
	if avg.Max != 2000 {
		t.Errorf("fan ceiling = %v, want the declared 2000 RPM", avg.Max)
	}
	// A fan spinning harder is not a warning, so the graph must not colour
	// itself red at high RPM.
	if avg.Warn != 0 || avg.Crit != 0 {
		t.Errorf("fan thresholds warn=%v crit=%v, want none", avg.Warn, avg.Crit)
	}
}

func TestNoFanAverageWithoutFans(t *testing.T) {
	snap := &model.Snapshot{Metrics: []model.Metric{temp("hwmon.coretemp.1", "coretemp", 50)}}
	derive(snap)
	if has(snap, MetricFanAvg) {
		t.Error("a fan average was published on a machine with no fan sensors")
	}
}

// The published series must have unique ids: two sharing one would overwrite
// each other's history.
func TestDerivedIDsAreUnique(t *testing.T) {
	snap := &model.Snapshot{
		Cores: 8,
		Metrics: []model.Metric{
			temp("hwmon.coretemp.1", "coretemp", 50),
			{ID: "hwmon.a.1", Group: "nct6798", Category: model.CategorySensor, Kind: model.KindFan, Value: 500, Max: 2000},
		},
		Devices: []model.Device{{Index: 0, Temperature: 60}},
	}
	derive(snap)

	seen := map[string]bool{}
	for _, mt := range snap.Metrics {
		if seen[mt.ID] {
			t.Errorf("duplicate metric id %q", mt.ID)
		}
		seen[mt.ID] = true
		if mt.Max <= mt.Min {
			t.Errorf("%s: range [%v,%v] cannot be plotted", mt.ID, mt.Min, mt.Max)
		}
	}
}

func find(t *testing.T, s *model.Snapshot, id string) model.Metric {
	t.Helper()
	for _, mt := range s.Metrics {
		if mt.ID == id {
			return mt
		}
	}
	t.Fatalf("metric %q was not published", id)
	return model.Metric{}
}

func has(s *model.Snapshot, id string) bool {
	for _, mt := range s.Metrics {
		if mt.ID == id {
			return true
		}
	}
	return false
}

func nan() float64 { var z float64; return z / z }
