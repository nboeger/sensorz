package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/nathan/sensorz/internal/collect"
	"github.com/nathan/sensorz/internal/collect/battery"
	"github.com/nathan/sensorz/internal/history"
	"github.com/nathan/sensorz/internal/model"
)

// batteryModel builds a model holding one snapshot with the given pack, so the
// battery panel can be tested without depending on the machine having one.
func batteryModel(t *testing.T, packs ...model.Battery) *Model {
	t.Helper()
	m := New(nil, history.NewStore(256), DefaultConfig(), DefaultTheme())
	m.width, m.height = 120, 40
	m.applySnapshot(model.Snapshot{
		Batteries: packs,
		Metrics:   battery.Metrics(packs),
	})
	return m
}

func laptopPack(percent float64, state model.BatteryState) model.Battery {
	return model.Battery{
		Path: "BAT0", Name: "BAT", Present: true,
		Percent: percent, State: state, ACOnline: state == model.BatteryCharging,
		Power: 9.5, Voltage: 11.4, HealthPercent: 90, Cycles: 42,
		MinutesLeft: 180, MinutesToFull: 45,
	}
}

func TestHasBattery(t *testing.T) {
	if m := batteryModel(t, laptopPack(50, model.BatteryDischarging)); !m.hasBattery() {
		t.Error("hasBattery = false with a present pack")
	}
	// A desktop: the collector reports nothing, so the panel must not appear.
	if m := batteryModel(t); m.hasBattery() {
		t.Error("hasBattery = true with no pack")
	}
	// An empty bay reports a pack that is not present.
	empty := laptopPack(0, model.BatteryUnknown)
	empty.Present = false
	empty.Percent = math.NaN()
	if m := batteryModel(t, empty); m.hasBattery() {
		t.Error("hasBattery = true for an absent pack")
	}
}

// A battery appears as a box in the hardware row, titled the same way the
// other panels are.
func TestBatteryPanelIsDrawn(t *testing.T) {
	m := batteryModel(t, laptopPack(64, model.BatteryDischarging))
	view := stripANSI(m.View())
	if !strings.Contains(view, "Battery") {
		t.Fatalf("no Battery panel in the view:\n%s", view)
	}
	for _, want := range []string{"state", "discharging", "time left", "3h00m", "power", "9.5W", "voltage", "11.40V", "health", "90%"} {
		if !strings.Contains(view, want) {
			t.Errorf("battery panel is missing %q:\n%s", want, view)
		}
	}
}

// The charge is the panel's headline, so it is drawn as the large figure with
// its unit rather than as another readout row.
func TestBatteryChargeIsTheHeadline(t *testing.T) {
	m := batteryModel(t, laptopPack(64, model.BatteryDischarging))
	mt, ok := m.Metric(battery.MetricCharge)
	if !ok {
		t.Fatal("charge metric missing")
	}
	if got := bigText(mt); got != "64%" {
		t.Errorf("bigText(charge) = %q, want the number and its unit", got)
	}
	if got := bigDigits(mt); got != "64" {
		t.Errorf("bigDigits(charge) = %q, want the bare number", got)
	}
}

// Charge is bad when it is low, so the colour ramp has to run the other way:
// a full pack is green and an empty one is red, which the temperature ramp
// alone would get exactly backwards.
func TestBatteryColourRunsLowToBad(t *testing.T) {
	m := batteryModel(t, laptopPack(100, model.BatteryFull))
	th := m.th

	full := m.metricColour(model.Metric{Kind: model.KindUtilization, Value: 100, Warn: 30, Crit: 15, LowIsBad: true})
	if full != th.Good {
		t.Errorf("100%% charge is drawn %v, want Good (%v)", full, th.Good)
	}
	low := m.metricColour(model.Metric{Kind: model.KindUtilization, Value: 8, Warn: 30, Crit: 15, LowIsBad: true})
	if low != th.Bad {
		t.Errorf("8%% charge is drawn %v, want Bad (%v)", low, th.Bad)
	}
	// A temperature keeps the ordinary direction: hot is the bad end.
	hot := m.metricColour(model.Metric{Kind: model.KindTemperature, Value: 95, Warn: 70, Crit: 90})
	if hot != th.Bad {
		t.Errorf("95°C is drawn %v, want Bad (%v)", hot, th.Bad)
	}
}

// The footer's bounds are the low-water marks, so they are written with the
// direction spelled out; "crit 15%" alone reads as a ceiling.
func TestBatteryFooterThresholds(t *testing.T) {
	m := batteryModel(t, laptopPack(64, model.BatteryDischarging))
	mt, _ := m.Metric(battery.MetricCharge)
	footer := stripANSI(m.footer(40, mt))
	if !strings.Contains(footer, "warn <30%") {
		t.Errorf("footer = %q, want the warn bound as a low-water mark", footer)
	}
	if !strings.Contains(footer, "crit <15%") {
		t.Errorf("footer = %q, want the crit bound as a low-water mark", footer)
	}
	if strings.Contains(footer, "°C") {
		t.Errorf("footer = %q, a charge is not a temperature", footer)
	}
}

// A charging pack counts up to full, not down to empty.
func TestChargingShowsTimeToFull(t *testing.T) {
	m := batteryModel(t, laptopPack(40, model.BatteryCharging))
	view := stripANSI(m.View())
	if !strings.Contains(view, "to full") || !strings.Contains(view, "45m") {
		t.Errorf("charging panel should show the time to full:\n%s", view)
	}
	if strings.Contains(view, "time left") {
		t.Errorf("charging panel should not show a discharge estimate:\n%s", view)
	}
}

// Full on the adapter has neither estimate; a dash would read as a figure the
// firmware failed to give.
func TestFullOnACShowsNoEstimate(t *testing.T) {
	p := laptopPack(100, model.BatteryFull)
	p.ACOnline = true
	p.MinutesLeft = -1
	m := batteryModel(t, p)
	view := stripANSI(m.View())
	if strings.Contains(view, "time left") || strings.Contains(view, "to full") {
		t.Errorf("a full pack on AC should show no estimate:\n%s", view)
	}
	if !strings.Contains(view, "full, on AC") {
		t.Errorf("state should say the pack is full on AC:\n%s", view)
	}
}

// The cursor visits the battery box like any other panel.
func TestBatteryIsInTheFocusCycle(t *testing.T) {
	m := batteryModel(t, laptopPack(64, model.BatteryDischarging))
	var found bool
	for _, f := range m.visibleFocus() {
		if f == FocusBattery {
			found = true
		}
	}
	if !found {
		t.Fatalf("battery missing from the focus cycle: %v", m.visibleFocus())
	}
	m.focus = FocusBattery
	if got := m.focus.String(); got != "battery" {
		t.Errorf("FocusBattery.String() = %q", got)
	}
}

func TestHumanMinutes(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{-1, "-"},
		{0, "<1m"},
		{42, "42m"},
		{60, "1h00m"},
		{185, "3h05m"},
	} {
		if got := humanMinutes(tc.in); got != tc.want {
			t.Errorf("humanMinutes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Fans, drives and the battery share the hardware row, and the three columns
// still reach the right edge exactly: a panel stopping short of the row above
// it reads as a mistake rather than a layout.
func TestMidRowSharesThreeColumns(t *testing.T) {
	m := New(nil, history.NewStore(256), DefaultConfig(), DefaultTheme())
	m.width, m.height = 120, 20
	m.applySnapshot(model.Snapshot{
		Metrics: []model.Metric{
			{ID: collect.MetricFanAvg, Category: model.CategorySensor, Kind: model.KindFan,
				Value: 1200, Min: 0, Max: 2400, Label: "Fan"},
			{ID: collect.MetricDiskTemp, Category: model.CategoryDisk, Kind: model.KindTemperature,
				Value: 40, Min: 0, Max: 100, Label: "Drives"},
			{ID: battery.MetricCharge, Category: model.CategoryBattery, Kind: model.KindUtilization,
				Value: 50, Min: 0, Max: 100, Warn: 30, Crit: 15, LowIsBad: true, Label: "Battery"},
		},
		Batteries: []model.Battery{laptopPack(50, model.BatteryDischarging)},
	})

	row := m.midRow(120, 14)
	plain := stripANSI(row)
	for _, want := range []string{"Fans", "Drives", "Battery"} {
		if !strings.Contains(plain, want) {
			t.Errorf("three-column row is missing the %s panel:\n%s", want, plain)
		}
	}
	for i, line := range strings.Split(plain, "\n") {
		if w := lineWidth(line); w != 120 {
			t.Errorf("row line %d is %d cells wide, want 120", i, w)
		}
	}
}

// The hardware row is drawn from whichever panels exist, so a machine with a
// battery and no fans still gets a row rather than an empty line.
func TestBatteryAloneStillGetsARow(t *testing.T) {
	m := batteryModel(t, laptopPack(64, model.BatteryDischarging))
	if !m.hasMidRow() {
		t.Fatal("hasMidRow = false with a battery present")
	}
	if m.hasFans() || m.hasDrives() {
		t.Skip("this machine reports fans or drives too; the row is shared")
	}
	view := stripANSI(m.midRow(120, 14))
	if !strings.Contains(view, "Battery") {
		t.Errorf("mid row has no battery panel:\n%s", view)
	}
}
