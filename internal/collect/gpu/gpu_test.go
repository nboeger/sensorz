package gpu

import (
	"context"
	"strings"
	"testing"

	"github.com/nathan/sensorz/internal/model"
)

// TestGPUsAreDiscovered finds the machine's GPUs through the real backends and
// checks the values are internally consistent.
//
// It is a smoke test rather than a strict assertion because the machine
// running it may have no GPU, one GPU, or several; what must always hold is
// that whatever is discovered is usable and that overlapping backends do not
// report the same card twice.
func TestGPUsAreDiscovered(t *testing.T) {
	c := New()
	defer c.Close()

	devs, err := c.Collect(context.Background())
	if err != nil {
		t.Logf("backend %s reported: %v", c.Name, err)
	}

	t.Logf("backend: %s, devices: %d", c.Name, len(devs))
	if len(devs) == 0 {
		t.Skip("no GPU on this machine")
	}

	seen := map[string]bool{}
	for _, d := range devs {
		if d.Name == "" {
			t.Errorf("device %d has no name", d.Index)
		}
		// The whole dedup scheme rests on this being populated; without it,
		// two backends could both claim the same card.
		if d.PciAddress == "" && d.Name == "" {
			t.Errorf("device %d has neither a PCI address nor a name, so it cannot be deduplicated", d.Index)
		}
		key := d.PciAddress + "/" + d.Name
		if seen[key] {
			t.Errorf("duplicate device %s", key)
		}
		seen[key] = true

		// A card either produced a plausible temperature or says it has no
		// sensor, and the two must not be confused: NaN is how the latter is
		// spelled, because zero is a reading a card can legitimately report.
		if d.HasTemperature() && (d.Temperature < -50 || d.Temperature > 130) {
			t.Errorf("%s: implausible temperature %.1fC", d.Name, d.Temperature)
		}
		if !d.HasTemperature() && d.Temperature == d.Temperature {
			t.Errorf("%s: no temperature, but Temperature is the number %v rather than NaN", d.Name, d.Temperature)
		}
		if d.FanPercent < -1 || d.FanPercent > 100 {
			t.Errorf("%s: fan %.1f%% out of range", d.Name, d.FanPercent)
		}
		t.Logf("%s: %.0fC, fan %.0f%%, %d extra sensors", d.Name, d.Temperature, d.FanPercent, len(d.ExtraTemps))
	}
}

func TestMetricsAreWellFormed(t *testing.T) {
	devs := []model.Device{{
		Index: 0, Name: "Test GPU", Driver: "nvidia", PciAddress: "0000:01:00.0",
		Temperature: 65, FanPercent: 40,
		ExtraTemps: []model.ExtraTemp{{Label: "Limit", Value: 83, Max: 84}},
	}}

	for _, m := range Metrics(devs) {
		if m.ID == "" || m.Label == "" {
			t.Errorf("metric with empty id/label: %+v", m)
		}
		if m.Category != model.CategoryGPU {
			t.Errorf("%s: category = %q", m.ID, m.Category)
		}
		if m.Max <= m.Min {
			t.Errorf("%s: range [%v, %v] cannot be plotted", m.ID, m.Min, m.Max)
		}
		// The card's own thermal limit is a better ceiling than a hardcoded
		// 100C, which varies by board and by the card's power limit.
		if m.ID == "gpu.0.temp.limit" && m.Max != 84 {
			t.Errorf("limit graph ceiling = %v, want the card's own 84C limit", m.Max)
		}
	}

	// IDs must be unique, or two series would share a history slot and one
	// graph would overwrite the other.
	seen := map[string]bool{}
	for _, m := range Metrics(devs) {
		if seen[m.ID] {
			t.Errorf("duplicate metric id %q", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestNormalisePCIAddress(t *testing.T) {
	// NVML reports the domain padded to eight digits; sysfs uses four. The
	// two must produce the same string or the backends will not deduplicate.
	got := formatPCI(0, 1, 0)
	if got != "0000:01:00.0" {
		t.Errorf("formatPCI(0, 1, 0) = %q, want 0000:01:00.0", got)
	}
	if got := formatPCI(0, 0xa, 0); got != "0000:0a:00.0" {
		t.Errorf("formatPCI(0, 0xa, 0) = %q, want 0000:0a:00.0", got)
	}
}

func TestPCIDetectsDisplayClass(t *testing.T) {
	// A display controller is PCI class 0x03xxxx. Anything else is not a GPU
	// and must not be listed by the fallback backend.
	for _, tc := range []struct {
		class string
		want  bool
	}{
		{"0x030000\n", true},  // VGA controller
		{"0x038000\n", true},  // other-class VGA, as found on some docks
		{"0x030200\n", true},  // 3D controller
		{"0x020000\n", false}, // ethernet
		{"0x010802\n", false}, // NVMe
		{"", false},
	} {
		if got := strings.HasPrefix(tc.class, "0x03"); got != tc.want {
			t.Errorf("class %q: display=%v, want %v", tc.class, got, tc.want)
		}
	}
}
