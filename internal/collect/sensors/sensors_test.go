package sensors

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nathan/sensorz/internal/collect/hwmon"
	"github.com/nathan/sensorz/internal/model"
)

// Two NVMe drives both publish a chip named "nvme". If the metric ID were keyed
// on the chip name they would collide, and one drive's temperature would
// overwrite the other's history every frame.
func TestDuplicateChipNamesGetUniqueIDs(t *testing.T) {
	chips := []hwmon.Chip{
		{Name: "nvme", Key: "nvme0-0000:00:06.0", Path: "/sys/class/hwmon/hwmon1"},
		{Name: "nvme", Key: "nvme1-0000:00:1a.0", Path: "/sys/class/hwmon/hwmon2"},
		{Name: "coretemp", Key: "coretemp", Path: "/sys/class/hwmon/hwmon3"},
	}
	labels := chipLabels(chips)

	if labels["/sys/class/hwmon/hwmon1"] != "nvme0" {
		t.Errorf("first nvme label = %q, want nvme0", labels["/sys/class/hwmon/hwmon1"])
	}
	if labels["/sys/class/hwmon/hwmon2"] != "nvme1" {
		t.Errorf("second nvme label = %q, want nvme1", labels["/sys/class/hwmon/hwmon2"])
	}
	// A chip with a unique name is left alone rather than given a redundant
	// suffix.
	if labels["/sys/class/hwmon/hwmon3"] != "coretemp" {
		t.Errorf("coretemp label = %q, want coretemp", labels["/sys/class/hwmon/hwmon3"])
	}
}

// Super-I/O boards register PCH temperature channels whose thermistors are not
// populated. They sit at a fixed 0.0C forever, so they are hidden by default;
// a "fixed zero" is never a real measurement, because an NTC thermistor at room
// temperature cannot read exactly zero.
func TestUnpopulatedChannelsAreExcludedByDefault(t *testing.T) {
	f := DefaultFilter()
	for _, ch := range []string{
		"nct6798/PCH_CPU_TEMP", "nct6798/PCH_CHIP_CPU_MAX_TEMP", "nct6775/PCH_CPU_TEMP",
		// The PECI calibration channel reports an offset, not a temperature.
		"nct6798/PECI Agent 0 Calibration",
	} {
		if !f.ExcludeChannels[ch] {
			t.Errorf("%s is not excluded by default", ch)
		}
	}
	if !f.ExcludeChips["acpitz"] {
		t.Error("acpitz is not excluded by default; it duplicates the board sensor")
	}
	// Exclusions must be case-insensitive on the label half, because sysfs
	// labels vary in case between drivers.
	c := New(f)
	if !c.excluded("nct6798", "PCH_CPU_TEMP") {
		t.Error("exclusion lookup is not case-insensitive")
	}
	if c.excluded("coretemp", "Package id 0") {
		t.Error("a real sensor was excluded")
	}
}

// sensorz reports temperature and fan speed, so nothing else may reach the UI:
// a board's voltage rails and current shunts are noise here, and the channels
// that carry them are never collected in the first place.
func TestOnlyTemperaturesAndFansAreCollected(t *testing.T) {
	c := New(DefaultFilter())
	ms, err := c.Collect()
	if err != nil {
		t.Skipf("no hwmon on this machine: %v", err)
	}
	for _, m := range ms {
		if m.Kind != model.KindTemperature && m.Kind != model.KindFan {
			t.Errorf("%s: kind %q, want a temperature or a fan speed", m.ID, m.Kind)
		}
	}
}

// A channel's own critical temperature is the hardware's real limit and must
// win over the generic guess, with the warning point derived from it.
func TestDriverCriticalOverridesGenericThresholds(t *testing.T) {
	m := model.Metric{Kind: model.KindTemperature, Value: 70}
	s := hwmon.Sensor{Value: 70, Crit: 85, HasCrit: true}
	applyDefaults(&m, s)

	if m.Crit != 85*0.97 {
		t.Errorf("crit = %v, want 97%% of the driver's 85C", m.Crit)
	}
	if m.Warn != 85*0.85 {
		t.Errorf("warn = %v, want 85%% of the driver's 85C", m.Warn)
	}
	if m.Max <= m.Crit {
		t.Errorf("graph ceiling %v must sit above the critical point %v", m.Max, m.Crit)
	}

	// With no driver critical, the generic values apply and the graph still has
	// to be plottable.
	m2 := model.Metric{Kind: model.KindTemperature, Value: 70}
	applyDefaults(&m2, hwmon.Sensor{Value: 70})
	if m2.Max <= m2.Min {
		t.Errorf("generic range [%v,%v] cannot be plotted", m2.Min, m2.Max)
	}
}

// An implausible critical value must not be trusted; some drivers report
// 65261C for channels with no real limit.
func TestImplausibleCritIsClamped(t *testing.T) {
	m := model.Metric{Kind: model.KindTemperature, Value: 40}
	applyDefaults(&m, hwmon.Sensor{Value: 40, Crit: 65261.8, HasCrit: true})
	if m.Max > 150 {
		t.Errorf("graph ceiling %v came from an implausible critical temperature", m.Max)
	}
	if m.Crit > 200 {
		t.Errorf("crit %v came from an implausible critical temperature", m.Crit)
	}
}

// The chip classification decides which panel a reading is drawn in, and the
// CPU average is only meaningful if the CPU's own chips are recognised as such.
func TestChipClassification(t *testing.T) {
	for _, c := range []string{"coretemp", "k10temp", "zenpower", "cpu_thermal", "soc_thermal"} {
		if !IsCPUChip(c) {
			t.Errorf("IsCPUChip(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"amdgpu", "i915", "nouveau", "xe", "nvme", "nct6798", "iwlwifi_1"} {
		if IsCPUChip(c) {
			t.Errorf("IsCPUChip(%q) = true, want false", c)
		}
	}
	if !IsGPUChip("amdgpu") || IsGPUChip("coretemp") {
		t.Error("IsGPUChip misclassified a chip")
	}
	if !IsDriveChip("nvme") || IsDriveChip("nct6798") {
		t.Error("IsDriveChip misclassified a chip")
	}
	// A GPU's chip belongs to the GPU panel, not the motherboard's.
	if ChipRank("amdgpu") == ChipRank("nct6798") {
		t.Error("a GPU chip and a motherboard chip rank the same")
	}
}

func TestKindMapping(t *testing.T) {
	cases := map[string]model.Kind{
		"temp":   model.KindTemperature,
		"fan":    model.KindFan,
		"in":     model.KindVoltage,
		"power":  model.KindPower,
		"energy": model.KindCount,
		"nope":   "",
	}
	for prefix, want := range cases {
		if got := kindOf(prefix); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", prefix, got, want)
		}
	}
}

// Collect must never crash on the live system, and must return only metrics
// that have a real reading.
func TestCollectReturnsOnlyValidReadings(t *testing.T) {
	c := New(DefaultFilter())
	ms, err := c.Collect()
	if err != nil {
		t.Skipf("no hwmon on this machine: %v", err)
	}
	if len(ms) == 0 {
		t.Skip("no sensors matched the default filter")
	}
	seen := map[string]bool{}
	for _, m := range ms {
		if !m.Valid() {
			t.Errorf("%s was returned without a valid reading", m.ID)
		}
		if seen[m.ID] {
			t.Errorf("duplicate metric id %q", m.ID)
		}
		seen[m.ID] = true
		if m.Max <= m.Min {
			t.Errorf("%s: range [%v,%v] cannot be plotted", m.ID, m.Min, m.Max)
		}
	}
	t.Logf("%d sensor metrics from the default filter", len(ms))
}

// writeChip lays out a fake hwmon chip with one temperature channel.
func writeChip(t *testing.T, root, dir, name, label string, milli int64) {
	t.Helper()
	d := filepath.Join(root, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	// hwmon values are written without a trailing newline, as the kernel does.
	for file, content := range map[string]string{
		"name":        name + "\n",
		"temp1_label": label + "\n",
		"temp1_input": strconv.FormatInt(milli, 10),
	} {
		if err := os.WriteFile(filepath.Join(d, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A sensor plugged in after sensorz started must turn up without a restart.
//
// The chip list is cached, and the cache is what makes a two second sample
// cheap, so the only way a hot-plugged device can appear is if something
// re-enumerates the tree. This drives that: the chip appears between the first
// and second pass, and the pass after the rescan interval must see it.
func TestHotPluggedSensorAppears(t *testing.T) {
	root := t.TempDir()
	writeChip(t, root, "hwmon0", "coretemp", "Package id 0", 40_000)

	old := hwmon.Root
	hwmon.Root = root
	t.Cleanup(func() { hwmon.Root = old })

	c := New(DefaultFilter())
	first, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first pass found %d metrics, want 1", len(first))
	}

	// Someone plugs in a USB sensor. Nothing else changes.
	writeChip(t, root, "hwmon1", "usb10x", "Sensor 1", 30_000)

	// Within the rescan interval the collector is still using its cached list,
	// which is the point of the cache.
	mid, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(mid) != 1 {
		t.Errorf("found %d metrics before the rescan, want the cached 1", len(mid))
	}

	// Past it, the new chip is picked up.
	c.lastScan = time.Now().Add(-2 * rescanInterval)
	after, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("found %d metrics after the rescan, want 2", len(after))
	}
}
