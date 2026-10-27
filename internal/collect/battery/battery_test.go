package battery

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nathan/sensorz/internal/model"
)

// pack writes a fake power supply directory: a type file plus whatever
// attributes the test needs, in the kernel's units (microvolt, microamp-hour,
// microwatt) so the parsing under test is the parsing that runs for real.
func pack(t *testing.T, root, name string, attrs map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range attrs {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v+"\n"), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func collect(t *testing.T, root string) []model.Battery {
	t.Helper()
	bats, err := newAt(root).Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return bats
}

func TestCollectDischargingPack(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "BAT0", map[string]string{
		"type":               "Battery",
		"present":            "1",
		"status":             "Discharging",
		"capacity":           "72",
		"voltage_now":        "11400000", // 11.4 V
		"power_now":          "9500000",  // 9.5 W
		"charge_now":         "2500000",  // 2.5 Ah
		"charge_full":        "3015000",
		"charge_full_design": "3335000",
		"cycle_count":        "42",
		"model_name":         "BAT",
		"manufacturer":       "Notebook",
	})

	bats := collect(t, root)
	if len(bats) != 1 {
		t.Fatalf("got %d batteries, want 1", len(bats))
	}
	b := bats[0]
	if b.Percent != 72 {
		t.Errorf("Percent = %v, want 72", b.Percent)
	}
	if b.State != model.BatteryDischarging {
		t.Errorf("State = %q, want discharging", b.State)
	}
	if got, want := b.Voltage, 11.4; math.Abs(got-want) > 1e-9 {
		t.Errorf("Voltage = %v, want %v", got, want)
	}
	if got, want := b.Power, 9.5; math.Abs(got-want) > 1e-9 {
		t.Errorf("Power = %v, want %v", got, want)
	}
	if got, want := b.HealthPercent, 3015000.0/3335000.0*100; math.Abs(got-want) > 1e-9 {
		t.Errorf("HealthPercent = %v, want %v", got, want)
	}
	if b.Cycles != 42 {
		t.Errorf("Cycles = %d, want 42", b.Cycles)
	}
	if b.Name != "BAT" {
		t.Errorf("Name = %q, want the model name BAT", b.Name)
	}

	// 2.5 Ah at 9.5 W on an 11.4 V pack: 2.5 Ah / 0.83 A = about 3 hours.
	if b.MinutesLeft <= 150 || b.MinutesLeft > 200 {
		t.Errorf("MinutesLeft = %d, want roughly 180", b.MinutesLeft)
	}
	if b.MinutesToFull != -1 {
		t.Errorf("MinutesToFull = %d, want -1 while discharging", b.MinutesToFull)
	}
}

func TestCollectChargingPackEstimatesTimeToFull(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "BAT0", map[string]string{
		"type":        "Battery",
		"present":     "1",
		"status":      "Charging",
		"capacity":    "50",
		"charge_now":  "1500000",
		"charge_full": "3000000",
		"current_now": "1500000", // 1.5 A
	})

	b := collect(t, root)[0]
	if b.State != model.BatteryCharging {
		t.Fatalf("State = %q, want charging", b.State)
	}
	// Half of 3 Ah left at 1.5 A is one hour.
	if b.MinutesToFull != 60 {
		t.Errorf("MinutesToFull = %d, want 60", b.MinutesToFull)
	}
	if b.MinutesLeft != -1 {
		t.Errorf("MinutesLeft = %d, want -1 while charging", b.MinutesLeft)
	}
}

// Packs that report energy in Wh and current in W are the other half of the
// fleet; the percentage and the health ratio have to come out the same.
func TestCollectEnergyBasedPack(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "BAT1", map[string]string{
		"type":               "Battery",
		"present":            "1",
		"status":             "Discharging",
		"energy_now":         "25000000", // 25 Wh
		"energy_full":        "50000000", // 50 Wh
		"energy_full_design": "60000000", // 60 Wh
		"power_now":          "10000000", // 10 W
		"voltage_now":        "15000000", // 15 V
	})

	b := collect(t, root)[0]
	if b.Percent != 50 {
		t.Errorf("Percent = %v, want 50 derived from the energy counters", b.Percent)
	}
	if got, want := b.HealthPercent, 50000000.0/60000000.0*100; math.Abs(got-want) > 1e-9 {
		t.Errorf("HealthPercent = %v, want %v", got, want)
	}
	// 25 Wh at 10 W is 2.5 hours.
	if b.MinutesLeft != 150 {
		t.Errorf("MinutesLeft = %d, want 150", b.MinutesLeft)
	}
}

// Without power_now the wattage comes from volts times amps, which is the same
// number from the files the firmware does provide.
func TestPowerDerivedFromVoltageAndCurrent(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "BAT0", map[string]string{
		"type": "Battery", "present": "1", "status": "Discharging", "capacity": "80",
		"voltage_now": "12000000", // 12 V
		"current_now": "2000000",  // 2 A -> 24 W
	})

	b := collect(t, root)[0]
	if got, want := b.Power, 24.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("Power = %v, want %v derived from V×A", got, want)
	}
}

// A battery that is present but reports no percentage has nothing to draw, and
// charting it at zero would read as empty rather than unreadable.
func TestPackWithoutChargeIsSkipped(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "BAT0", map[string]string{"type": "Battery", "present": "1"})
	if bats := collect(t, root); len(bats) != 0 {
		t.Fatalf("got %d batteries for a pack with no charge, want 0", len(bats))
	}
}

func TestAbsentAndNonBatterySuppliesAreIgnored(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "BAT0", map[string]string{
		"type": "Battery", "present": "0", "capacity": "100",
	})
	pack(t, root, "AC", map[string]string{"type": "Mains", "online": "1"})
	pack(t, root, "hidpp_battery_0", map[string]string{
		"type": "Battery", "present": "1", "capacity": "55",
	})

	bats := collect(t, root)
	if len(bats) != 1 || bats[0].Path != "hidpp_battery_0" {
		t.Fatalf("got %+v, want only the present battery", bats)
	}
	if !bats[0].ACOnline {
		t.Error("ACOnline = false, want true with the mains adapter online")
	}
}

func TestMissingPowerSupplyClassIsNotAnError(t *testing.T) {
	bats, err := newAt(filepath.Join(t.TempDir(), "does-not-exist")).Collect()
	if err != nil {
		t.Fatalf("Collect on a machine with no power supply: %v", err)
	}
	if len(bats) != 0 {
		t.Fatalf("got %d batteries, want 0", len(bats))
	}
	if m := Metrics(bats); m != nil {
		t.Fatalf("Metrics = %v, want none", m)
	}
}

func TestMetricsCarryTheLowIsBadThresholds(t *testing.T) {
	bats := []model.Battery{{
		Path: "BAT0", Present: true, Percent: 74,
		State: model.BatteryDischarging,
		Power: 8, Voltage: 12, HealthPercent: 91,
	}}

	byID := map[string]model.Metric{}
	for _, m := range Metrics(bats) {
		byID[m.ID] = m
	}

	charge, ok := byID[MetricCharge]
	if !ok {
		t.Fatal("no charge metric")
	}
	if charge.Value != 74 {
		t.Errorf("charge Value = %v, want 74", charge.Value)
	}
	if !charge.LowIsBad {
		t.Error("charge LowIsBad = false, want true: an empty pack is the bad one")
	}
	if charge.Warn != 30 || charge.Crit != 15 {
		t.Errorf("charge thresholds = %v/%v, want 30/15", charge.Warn, charge.Crit)
	}
	if charge.Min != 0 || charge.Max != 100 {
		t.Errorf("charge scale = %v..%v, want 0..100", charge.Min, charge.Max)
	}
	if charge.Category != model.CategoryBattery {
		t.Errorf("charge Category = %q, want battery", charge.Category)
	}

	if h, ok := byID[MetricHealth]; !ok || !h.LowIsBad {
		t.Error("health metric missing or not LowIsBad")
	}
	// Power is bad in neither direction; it must not claim a threshold.
	if p, ok := byID[MetricPower]; !ok || p.LowIsBad {
		t.Error("power metric missing or wrongly marked LowIsBad")
	}
}

// Several packs collapse to one averaged charge: two graphs that move together
// say the same thing twice.
func TestMetricsAverageAcrossPacks(t *testing.T) {
	bats := []model.Battery{
		{Path: "BAT0", Present: true, Percent: 80, HealthPercent: 90, Power: 10, Voltage: 12},
		{Path: "BAT1", Present: true, Percent: 60, HealthPercent: 70, Power: 5, Voltage: 11},
	}
	byID := map[string]model.Metric{}
	for _, m := range Metrics(bats) {
		byID[m.ID] = m
	}
	if got := byID[MetricCharge].Value; got != 70 {
		t.Errorf("charge = %v, want the mean 70", got)
	}
	if got := byID[MetricHealth].Value; got != 80 {
		t.Errorf("health = %v, want the mean 80", got)
	}
	// Power sums: two packs drawing 10 W and 5 W pull 15 W from the adapter.
	if got := byID[MetricPower].Value; got != 15 {
		t.Errorf("power = %v, want the total 15", got)
	}
}

func TestStateMapping(t *testing.T) {
	for in, want := range map[string]model.BatteryState{
		"Charging":     model.BatteryCharging,
		"discharging":  model.BatteryDischarging,
		"Full":         model.BatteryFull,
		"Not charging": model.BatteryIdle,
		"":             model.BatteryUnknown,
		"weird":        model.BatteryUnknown,
	} {
		if got := state(in); got != want {
			t.Errorf("state(%q) = %q, want %q", in, got, want)
		}
	}
}
