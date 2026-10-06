package collect

import (
	"context"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nathan/sensorz/internal/collect/hwmon"
	"github.com/nathan/sensorz/internal/collect/sensors"
)

// TestMatchesLmSensors cross-checks the hwmon readings against the lm-sensors
// `sensors` utility.
//
// This is the ground truth test for the whole hwmon layer. The kernel ABI is
// what both tools read, so any disagreement is a scaling, labelling or channel
// selection bug in one of them. The test skips where lm-sensors is not
// installed, which is common in containers.
func TestMatchesLmSensors(t *testing.T) {
	if _, err := exec.LookPath("sensors"); err != nil {
		t.Skip("lm-sensors not installed")
	}

	// Read the hardware ourselves either side of lm-sensors, and accept a
	// reading that matches either of ours. Both tools sample live hardware and
	// the two reads are milliseconds apart at best, so on a busy machine - a
	// core can climb ten degrees while the test binary is being linked - the
	// tools can disagree about a channel that neither is misreading. A single
	// reading before or after is a coin toss on a loaded machine; bracketing the
	// reference is not.
	before, err := readTemps()
	if err != nil {
		t.Fatalf("hwmon.Read: %v", err)
	}
	out, err := exec.Command("sensors").Output()
	if err != nil {
		t.Skipf("sensors failed: %v", err)
	}
	after, err := readTemps()
	if err != nil {
		t.Fatalf("hwmon.Read: %v", err)
	}

	// lm-sensors output looks like "    Composite:    +43.9°C  (low = ...)".
	re := regexp.MustCompile(`^(\S[^:]*):\s+\+?(-?[0-9]+\.[0-9])°C`)
	chip := ""
	compared, missing, ambiguous := 0, 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		// A chip heading is the only line that starts in column zero, and it
		// never contains a colon - "Adapter: ISA adapter" is indented but
		// would otherwise be mistaken for one.
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.Contains(line, ":") {
			chip = strings.Fields(line)[0]
			continue
		}
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		label, raw := m[1], m[2]
		ref, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			t.Fatalf("parsing %q: %v", raw, err)
		}

		// Try the chip name, then the driver name, since lm-sensors prints the
		// driver name and the hwmon chip name can differ (nct6798 vs nct6775).
		candidates := []string{chip + "/" + label}
		if i := strings.Index(chip, "-"); i > 0 {
			candidates = append(candidates, chip[:i]+"/"+label)
		}
		var got []float64
		var ok bool
		for _, c := range candidates {
			if v, found := before[c]; found {
				got, ok = v, true
				break
			}
		}
		if !ok {
			missing++
			continue
		}
		if len(got) > 1 {
			// Two of our chips share this name (two NVMe drives both publish
			// a chip called "nvme"), so lm-sensors' single figure cannot be
			// attributed to one of them. Recording it without asserting.
			ambiguous++
			continue
		}
		// Half a degree of tolerance covers the different rounding the two
		// tools apply to the same millidegree integer. Anything larger has to be
		// explained by the hardware moving, so it is allowed if either of our
		// own readings - before or after lm-sensors ran - is that close.
		if d := math.Abs(got[0] - ref); d > 0.55 {
			if !matchesAfter(after, candidates, ref, 0.55) {
				t.Errorf("%s/%s: sensorz=%.2f°C then %.2f°C, lm-sensors=%.2f°C",
					chip, label, got[0], closestTo(after, candidates, ref), ref)
			}
		}
		compared++
	}
	if compared == 0 {
		t.Fatal("no temperature channels were cross-checked; the lookup is broken")
	}
	t.Logf("cross-checked %d readings against lm-sensors (%d had no counterpart, %d were ambiguous duplicate chip names)",
		compared, missing, ambiguous)
}

// readTemps reads every temperature channel the kernel is currently exporting,
// keyed by "<chip>/<label>".
func readTemps() (map[string][]float64, error) {
	chips, err := hwmon.Read()
	if err != nil {
		return nil, err
	}
	out := map[string][]float64{}
	for _, c := range chips {
		if c.Name == "acpitz" {
			continue // excluded by the default filter as a duplicate
		}
		for _, s := range c.Sensors {
			if s.Prefix != "temp" || !s.Valid() {
				continue
			}
			out[c.Name+"/"+s.Name()] = append(out[c.Name+"/"+s.Name()], s.Value)
		}
	}
	return out, nil
}

// matchesAfter reports whether any of our readings taken after lm-sensors ran is
// within tolerance of the reference.
func matchesAfter(ours map[string][]float64, candidates []string, ref, tol float64) bool {
	for _, c := range candidates {
		for _, v := range ours[c] {
			if math.Abs(v-ref) <= tol {
				return true
			}
		}
	}
	return false
}

// closestTo returns the of our readings nearest the reference, for the failure
// message.
func closestTo(ours map[string][]float64, candidates []string, ref float64) float64 {
	best, bestDist := math.NaN(), math.Inf(1)
	for _, c := range candidates {
		for _, v := range ours[c] {
			if d := math.Abs(v - ref); d < bestDist {
				best, bestDist = v, d
			}
		}
	}
	return best
}

// TestDiskTempsMatchLmSensors is the same cross-check for NVMe drive
// temperatures, which go through a different path: they are matched from the
// hwmon chip back to the /dev node by resolving symlinks.
func TestDiskTempsMatchLmSensors(t *testing.T) {
	if _, err := exec.LookPath("sensors"); err != nil {
		t.Skip("lm-sensors not installed")
	}
	agg, err := New(Options{SensorFilter: sensors.DefaultFilter()})
	if err != nil {
		t.Fatal(err)
	}
	defer agg.Close()

	ctx := context.Background()
	agg.Once(ctx) // establish counter baselines
	time.Sleep(100 * time.Millisecond)
	snap := agg.Once(ctx)

	ours := map[string]float64{}
	for _, mt := range snap.Metrics {
		if mt.Category != "disk" || mt.Kind != "temperature" || mt.Group != "SSD Temp" {
			continue
		}
		ours[mt.Label] = mt.Value
	}
	ref := referenceNVMeTemps(t)
	if len(ref) == 0 {
		t.Skip("lm-sensors reported no NVMe composite temperatures")
	}

	// `sensors -u` identifies a controller by PCI slot rather than by device
	// node, so the two sets cannot be joined on name. Both tools read the same
	// hwmon channel, so sorting both sides and comparing pairwise is still a
	// real check on the values, and it catches a drive being dropped or
	// mismatched - which is exactly the failure the symlink matching could have.
	if len(ours) != len(ref) {
		t.Errorf("sensorz found %d drive temperatures (%v), lm-sensors found %d (%v)",
			len(ours), ours, len(ref), ref)
	}

	mine := make([]float64, 0, len(ours))
	for _, v := range ours {
		mine = append(mine, v)
	}
	sort.Float64s(mine)
	sort.Float64s(ref)

	for i := range mine {
		if i >= len(ref) {
			break
		}
		if math.Abs(mine[i]-ref[i]) > 0.55 {
			t.Errorf("drive temperature %d: sensorz=%.2f°C lm-sensors=%.2f°C", i, mine[i], ref[i])
		}
		t.Logf("drive temperature %d: %.1f°C agrees with lm-sensors", i, mine[i])
	}
}

// referenceNVMeTemps scrapes every NVMe Composite reading out of `sensors -u`.
// That output is already in degrees Celsius, unlike the raw sysfs values.
func referenceNVMeTemps(t *testing.T) []float64 {
	t.Helper()
	out, err := exec.Command("sensors", "-u").Output()
	if err != nil {
		return nil
	}
	var (
		vals   []float64
		inNVMe bool
	)
	// A chip heading is a bare token in column zero with no spaces, no colon
	// and no indentation. "Adapter: PCI adapter" and "Composite:" both sit in
	// column zero too, so column position alone is not enough to spot a
	// heading.
	heading := regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, " ") && heading.MatchString(line) {
			inNVMe = strings.HasPrefix(line, "nvme-")
			continue
		}
		if !inNVMe {
			continue
		}
		m := regexp.MustCompile(`^\s*temp1_input:\s*(-?[0-9.]+)`).FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			vals = append(vals, v)
		}
	}
	return vals
}

func TestCollectorsProduceData(t *testing.T) {
	agg, err := New(Options{SensorFilter: sensors.DefaultFilter()})
	if err != nil {
		t.Fatal(err)
	}
	defer agg.Close()

	ctx := context.Background()
	// Three passes: the first establishes the counter baselines, and rates
	// need at least two.
	for i := 0; i < 3; i++ {
		agg.Once(ctx)
		time.Sleep(80 * time.Millisecond)
	}
	snap := agg.Once(ctx)

	if len(snap.Metrics) == 0 {
		t.Fatal("no metrics at all")
	}
	if len(snap.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", snap.Warnings)
	}

	byCat := map[string]int{}
	for _, m := range snap.Metrics {
		byCat[string(m.Category)]++
	}
	// Every series sensorz draws must be there: a temperature, or a fan speed.
	for _, want := range []string{"cpu", "disk", "sensor"} {
		if byCat[want] == 0 {
			t.Errorf("no metrics for category %q (got %v)", want, byCat)
		}
	}
	t.Logf("metrics by category: %v", byCat)

	// Nothing but temperatures and fan speeds may be published: utilisation,
	// memory and throughput are deliberately not collected.
	for _, m := range snap.Metrics {
		if m.Kind != "temperature" && m.Kind != "fan" {
			t.Errorf("%s: kind %q, want a temperature or a fan speed", m.ID, m.Kind)
		}
	}

	// Every series must be renderable: a finite value within its own bounds.
	for _, m := range snap.Metrics {
		if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			t.Errorf("%s has non-finite value %v", m.ID, m.Value)
		}
		if m.Max <= m.Min {
			t.Errorf("%s has an unusable range [%v, %v]", m.ID, m.Min, m.Max)
		}
	}

	// Metric IDs must be unique, or two series would share a history slot.
	seen := map[string]bool{}
	for _, m := range snap.Metrics {
		if seen[m.ID] {
			t.Errorf("duplicate metric id %q", m.ID)
		}
		seen[m.ID] = true
	}
}
