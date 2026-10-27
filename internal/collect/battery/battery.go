// Package battery reads the machine's battery from the kernel's power supply
// class.
//
// The numbers come from /sys/class/power_supply, the same files the desktop's
// status menu reads, so the panel agrees with what the rest of the system says.
// A machine with no battery - a desktop, a server, most VMs - simply reports
// nothing, and the UI drops the panel.
//
// Every field is optional. Firmware is wildly inconsistent about which of them
// it exposes: some packs report energy in Wh, some charge in Ah, some only a
// percentage, and some report power while others report current. Each reading
// is derived from whichever pair of files is present, and left unknown when
// none of them are.
package battery

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/nathan/sensorz/internal/model"
)

// Metric IDs for the battery panel.
const (
	// MetricCharge is the headline series: the state of charge in percent.
	MetricCharge = "battery.charge"
	// MetricPower is what flows in or out of the pack, in watts.
	MetricPower = "battery.power"
	// MetricVoltage is the pack voltage.
	MetricVoltage = "battery.voltage"
	// MetricHealth is the full-charge capacity as a percentage of design
	// capacity, which is the number that says how worn the pack is.
	MetricHealth = "battery.health"
)

// defaultRoot is the kernel's power supply class.
const defaultRoot = "/sys/class/power_supply"

// The micro- prefixes the kernel uses for its power supply figures. Charge is
// in microamp-hours, energy in microwatt-hours, and voltage and current in
// microvolts and microamps.
const (
	micro = 1e-6
)

// Collector reads the battery state from sysfs.
type Collector struct {
	root string
}

// New returns a collector rooted at the kernel's power supply class.
func New() *Collector { return &Collector{root: defaultRoot} }

// newAt returns a collector rooted at dir, for tests.
func newAt(dir string) *Collector { return &Collector{root: dir} }

// Collect reads every battery and reports them.
//
// A machine whose power supply class cannot be read at all is not an error: it
// means there is no battery, which is the normal case for a desktop. A pack
// that is present but reports nothing usable is skipped rather than graphed at
// zero, because a battery charted at 0% reads as empty instead of unreadable.
func (c *Collector) Collect() ([]model.Battery, error) {
	entries, err := os.ReadDir(c.root)
	if err != nil {
		// No power supply class means no battery, not a broken machine.
		return nil, nil
	}

	acOnline := mainsOnline(c.root)

	var out []model.Battery
	for _, e := range entries {
		dir := filepath.Join(c.root, e.Name())
		kind := readString(dir, "type")
		// UPS packs are batteries with a different name and the same files,
		// so they belong in the same panel.
		if kind != "Battery" && kind != "UPS" {
			continue
		}
		b := readBattery(dir, e.Name())
		if !b.Valid() {
			continue
		}
		b.Index = len(out)
		b.ACOnline = acOnline
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	for i := range out {
		out[i].Index = i
	}
	return out, nil
}

// readBattery builds one pack's reading from its sysfs directory.
func readBattery(dir, name string) model.Battery {
	b := model.Battery{
		Path:          name,
		Name:          firstNonEmpty(readString(dir, "model_name"), readString(dir, "manufacturer"), name),
		Present:       readBool(dir, "present", true),
		MinutesLeft:   -1,
		MinutesToFull: -1,
		Cycles:        -1,
		HealthPercent: math.NaN(),
		Power:         math.NaN(),
		Voltage:       math.NaN(),
	}

	b.Percent = percent(dir)
	b.State = state(readString(dir, "status"))
	b.Voltage = volts(dir)
	b.Power = watts(dir, b.Voltage)
	b.HealthPercent = health(dir)
	if n, ok := readInt(dir, "cycle_count"); ok {
		b.Cycles = int(n)
	}
	b.MinutesLeft, b.MinutesToFull = times(dir, b.State, b.Voltage)
	return b
}

// percent prefers the kernel's own percentage and derives one from charge or
// energy when the firmware does not report it.
func percent(dir string) float64 {
	if n, ok := readInt(dir, "capacity"); ok && n >= 0 && n <= 100 {
		return float64(n)
	}
	for _, p := range [][2]string{
		{"charge_now", "charge_full"},
		{"energy_now", "energy_full"},
	} {
		now, ok1 := readInt(dir, p[0])
		full, ok2 := readInt(dir, p[1])
		if ok1 && ok2 && full > 0 {
			return clampPct(float64(now) / float64(full) * 100)
		}
	}
	return math.NaN()
}

// health is the full-charge capacity against the design capacity, which is how
// much of the pack's original capacity it still holds.
//
// The two figures always share a unit, so the ratio is unitless and works for
// both the charge-in-Ah and energy-in-Wh families.
func health(dir string) float64 {
	for _, p := range [][2]string{
		{"charge_full", "charge_full_design"},
		{"energy_full", "energy_full_design"},
	} {
		full, ok1 := readInt(dir, p[0])
		design, ok2 := readInt(dir, p[1])
		if ok1 && ok2 && design > 0 {
			return clampPct(float64(full) / float64(design) * 100)
		}
	}
	return math.NaN()
}

// volts reads the pack voltage.
func volts(dir string) float64 {
	if n, ok := readInt(dir, "voltage_now"); ok {
		return float64(n) * micro
	}
	return math.NaN()
}

// watts reads the power flowing in or out of the pack.
//
// power_now is used when the firmware has it. Otherwise it is the product of
// voltage and current, which is the same number derived from the files the
// firmware does provide. Without a voltage there is nothing to multiply, and
// the reading stays unknown rather than being guessed.
func watts(dir string, voltage float64) float64 {
	if n, ok := readInt(dir, "power_now"); ok {
		return math.Abs(float64(n) * micro)
	}
	if current, ok := readInt(dir, "current_now"); ok && !math.IsNaN(voltage) {
		return math.Abs(float64(current) * micro * voltage)
	}
	return math.NaN()
}

// times estimates how long the pack has left, or how long until it is full.
//
// The kernel exposes time_to_empty_now on some hardware and nothing at all on
// the rest, so the estimate is computed from the counters rather than trusted
// to a file that is usually absent.
func times(dir string, st model.BatteryState, voltage float64) (left, toFull int) {
	left, toFull = -1, -1

	nowH, fullH, ok := hoursOfCharge(dir, voltage)
	if !ok {
		if n, ok := readInt(dir, "time_to_empty_now"); ok && st == model.BatteryDischarging {
			left = int(n / 60)
		}
		return left, toFull
	}

	minutes := func(h float64) int { return int(math.Round(h * 60)) }
	switch st {
	case model.BatteryDischarging:
		left = minutes(nowH)
	case model.BatteryCharging:
		if fullH > nowH {
			toFull = minutes(fullH - nowH)
		}
	}
	return left, toFull
}

// hoursOfCharge returns the charge in the pack and its full-charge capacity,
// both in hours at the current rate.
//
// The pair has to match: an energy counter divides by power, a charge counter
// divides by current, and dividing one by the other mixes watt-hours with
// amp-hours and lands off by the pack voltage. Where the firmware reports only
// one of each family the voltage converts between them, which is why it is
// threaded through here.
func hoursOfCharge(dir string, voltage float64) (nowH, fullH float64, ok bool) {
	// Same-family pairs need no conversion: microamp-hours over microamps is
	// hours, and microwatt-hours over microwatts is hours.
	for _, p := range [][3]string{
		{"charge_now", "current_now", "charge_full"},
		{"energy_now", "power_now", "energy_full"},
	} {
		now, okNow := readInt(dir, p[0])
		rate, okRate := readInt(dir, p[1])
		if !okNow || !okRate || rate <= 0 {
			continue
		}
		nowH = float64(now) / float64(rate)
		if full, okFull := readInt(dir, p[2]); okFull {
			fullH = float64(full) / float64(rate)
		}
		return nowH, fullH, true
	}

	if math.IsNaN(voltage) || voltage <= 0 {
		return 0, 0, false
	}

	// Cross-family pairs: convert the counter through the voltage into the unit
	// the rate is already expressed in.
	if now, okNow := readInt(dir, "energy_now"); okNow {
		if rate, okRate := readInt(dir, "current_now"); okRate && rate > 0 {
			watts := float64(rate) * micro * voltage
			nowH = float64(now) * micro / watts
			if full, okFull := readInt(dir, "energy_full"); okFull {
				fullH = float64(full) * micro / watts
			}
			return nowH, fullH, true
		}
	}
	if now, okNow := readInt(dir, "charge_now"); okNow {
		if rate, okRate := readInt(dir, "power_now"); okRate && rate > 0 {
			watts := float64(rate) * micro
			nowH = float64(now) * micro * voltage / watts
			if full, okFull := readInt(dir, "charge_full"); okFull {
				fullH = float64(full) * micro * voltage / watts
			}
			return nowH, fullH, true
		}
	}
	return 0, 0, false
}

// state maps the kernel's status strings onto the states the panel shows.
func state(s string) model.BatteryState {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "charging":
		return model.BatteryCharging
	case "discharging":
		return model.BatteryDischarging
	case "full":
		return model.BatteryFull
	case "not charging":
		return model.BatteryIdle
	default:
		return model.BatteryUnknown
	}
}

// mainsOnline reports whether any non-battery supply - the mains adapter, a
// USB-C charger, a dock - is connected. A pack cannot tell a full battery on
// the charger from one that has just been unplugged; this is what can.
func mainsOnline(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		kind := readString(dir, "type")
		if kind == "Battery" || kind == "UPS" {
			continue
		}
		if online, ok := readInt(dir, "online"); ok && online == 1 {
			return true
		}
	}
	return false
}

// Metrics turns the packs into the panel's series.
//
// A machine with several packs gets one averaged series rather than one graph
// each: a laptop with a slice battery and a main battery drains them together,
// and two near-identical graphs cost more than they say.
func Metrics(batteries []model.Battery) []model.Metric {
	var valid []model.Battery
	for _, b := range batteries {
		if b.Valid() {
			valid = append(valid, b)
		}
	}
	if len(valid) == 0 {
		return nil
	}

	label := "Battery"
	if len(valid) > 1 {
		label = fmt.Sprintf("Batteries ×%d", len(valid))
	}

	var (
		pctSum, voltSum, powerSum, healthSum float64
		nPct, nVolt, nPower, nHealth         int
	)
	for _, b := range valid {
		if b.Percent == b.Percent {
			pctSum += b.Percent
			nPct++
		}
		if b.Voltage == b.Voltage {
			voltSum += b.Voltage
			nVolt++
		}
		if b.Power == b.Power {
			powerSum += b.Power
			nPower++
		}
		if b.HealthPercent == b.HealthPercent {
			healthSum += b.HealthPercent
			nHealth++
		}
	}

	charge := model.Metric{
		ID:       MetricCharge,
		Label:    label,
		Group:    label,
		Category: model.CategoryBattery,
		Kind:     model.KindUtilization,
		Value:    pctSum / float64(nPct),
		Min:      0,
		Max:      100,
		// The low-water marks: warn when the pack drops to 30%, red at 15%.
		// LowIsBad makes them lower bounds rather than the usual upper ones.
		Warn:     30,
		Crit:     15,
		LowIsBad: true,
	}
	out := []model.Metric{charge}

	if nVolt > 0 {
		out = append(out, model.Metric{
			ID: MetricVoltage, Label: "Voltage", Group: label,
			Category: model.CategoryBattery, Kind: model.KindVoltage,
			Value: voltSum / float64(nVolt), Max: 20,
		})
	}
	if nPower > 0 {
		// Power is summed rather than averaged: two packs charging at 20W each
		// draw 40W from the adapter, and the figure a user wants is the total.
		out = append(out, model.Metric{
			ID: MetricPower, Label: "Power", Group: label,
			Category: model.CategoryBattery, Kind: model.KindPower,
			Value: powerSum, Max: 100,
		})
	}
	if nHealth > 0 {
		out = append(out, model.Metric{
			ID: MetricHealth, Label: "Health", Group: label,
			Category: model.CategoryBattery, Kind: model.KindUtilization,
			Value: healthSum / float64(nHealth), Max: 100,
			// A pack that has lost half its capacity is worn, not broken; the
			// warn point is where a replacement starts to be worth considering.
			Warn:     80,
			Crit:     50,
			LowIsBad: true,
		})
	}
	return out
}

func readInt(dir, name string) (int64, bool) {
	s := readString(dir, name)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readString(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readBool reads a sysfs 0/1 flag, falling back when the file is absent.
func readBool(dir, name string, fallback bool) bool {
	v, ok := readInt(dir, name)
	if !ok {
		return fallback
	}
	return v != 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func clampPct(v float64) float64 {
	return math.Max(0, math.Min(100, v))
}
