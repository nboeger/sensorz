package hwmon

import (
	"os"
	"path/filepath"
	"testing"
)

// newFixture builds a fake /sys/class/hwmon tree. Tests point Root at it so
// they exercise the real parsing code against known contents.
func newFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Point the package at the fixture and restore afterwards.
	old := Root
	Root = dir
	t.Cleanup(func() { Root = old })
	return dir
}

func TestReadScalesAndLabels(t *testing.T) {
	newFixture(t, map[string]string{
		// hwmon entries in sysfs are symlinks, so the fixture builds a real
		// device directory and points a class symlink at its hwmon child,
		// exactly the shape the kernel creates.
		"devices/coretemp/hwmon/hwmon3/name":         "coretemp\n",
		"devices/coretemp/hwmon/hwmon3/temp1_input":  "84000\n",
		"devices/coretemp/hwmon/hwmon3/temp1_label":  "Package id 0\n",
		"devices/coretemp/hwmon/hwmon3/temp1_crit":   "100000\n",
		"devices/coretemp/hwmon/hwmon3/temp2_input":  "52300\n",
		"devices/coretemp/hwmon/hwmon3/temp2_label":  "Core 0\n",
		"devices/coretemp/hwmon/hwmon3/fan1_input":   "1400\n",
		"devices/coretemp/hwmon/hwmon3/power1_input": "25000000\n",
		"devices/coretemp/hwmon/hwmon3/in0_input":    "1250\n",
	})

	// The kernel's device and driver symlinks. The class symlink has to come
	// first: the other two are created *through* it, which only works once it
	// resolves to a real directory.
	mustSymlink(t, filepath.Join(Root, "devices/coretemp/hwmon/hwmon3"), filepath.Join(Root, "hwmon3"))
	mustSymlink(t, filepath.Join(Root, "devices/coretemp/driver"),
		filepath.Join(Root, "hwmon3", "driver"))
	mustSymlink(t, filepath.Join(Root, "devices/coretemp"),
		filepath.Join(Root, "hwmon3", "device"))

	chips, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(chips) != 1 {
		t.Fatalf("got %d chips, want 1: %+v", len(chips), chips)
	}
	c := chips[0]
	if c.Name != "coretemp" {
		t.Errorf("name = %q", c.Name)
	}
	if c.Key != "coretemp" {
		t.Errorf("key = %q, want coretemp", c.Key)
	}
	if c.Driver != "coretemp" {
		t.Errorf("driver = %q, want coretemp", c.Driver)
	}
	if len(c.Sensors) != 5 {
		t.Fatalf("got %d sensors, want 5: %+v", len(c.Sensors), c.Sensors)
	}

	// Index by prefix and channel number: a chip carries several channels of
	// each kind, so keying on the prefix alone loses all but the last.
	byKey := map[string]Sensor{}
	for _, s := range c.Sensors {
		byKey[s.Prefix+itoa(s.Index)] = s
	}

	pkg, ok := byKey["temp1"]
	if !ok {
		t.Fatalf("temp1 missing from %+v", c.Sensors)
	}
	if pkg.Value != 84 {
		t.Errorf("temperature = %v, want 84 (84000 millidegrees)", pkg.Value)
	}
	if pkg.Label != "Package id 0" {
		t.Errorf("label = %q", pkg.Label)
	}
	if !pkg.HasCrit || pkg.Crit != 100 {
		t.Errorf("crit = %v has=%v, want 100 true", pkg.Crit, pkg.HasCrit)
	}
	if got := byKey["temp2"].Value; got != 52.3 {
		t.Errorf("core temperature = %v, want 52.3", got)
	}
	if got := byKey["fan1"].Value; got != 1400 {
		t.Errorf("fan = %v, want 1400", got)
	}
	if got := byKey["power1"].Value; got != 25 {
		t.Errorf("power = %v, want 25 watts (25000000 microwatts)", got)
	}
	if got := byKey["in0"].Value; got != 1.25 {
		t.Errorf("voltage = %v, want 1.25", got)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
}

// An unreadable sensor must never be reported as a valid zero reading. The
// iwlwifi temperature returns ENODATA until the radio associates, and treating
// that as 0C would drag the display's temperature baselines to the floor.
func TestUnreadableSensorIsInvalid(t *testing.T) {
	dir := newFixture(t, map[string]string{
		"hwmon7/name":        "iwlwifi_1\n",
		"hwmon7/temp1_input": "",
		"hwmon7/temp2_input": "41000\n",
		"hwmon7/temp2_label": "radio\n",
	})
	_ = dir

	chips, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(chips) != 1 {
		t.Fatalf("got %d chips, want 1", len(chips))
	}
	var idle, good Sensor
	for _, s := range chips[0].Sensors {
		if s.Index == 1 {
			idle = s
		} else {
			good = s
		}
	}
	if idle.Valid() {
		t.Errorf("an empty temp1_input was accepted as a valid reading of %v", idle.Value)
	}
	if !good.Valid() || good.Value != 41 {
		t.Errorf("readable sensor = %v valid=%v, want 41 true", good.Value, good.Valid())
	}
}

// A channel with a label but no readable value is still listed, so the panel
// layout does not jump when the sensor wakes up.
func TestLabelledButIdleChannelIsListed(t *testing.T) {
	newFixture(t, map[string]string{
		"hwmon2/name":        "nvme\n",
		"hwmon2/temp1_label": "Composite\n",
		"hwmon2/temp1_input": "43850\n",
	})
	chips, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(chips) != 1 || len(chips[0].Sensors) != 1 {
		t.Fatalf("expected one chip with one channel, got %+v", chips)
	}
	if got := chips[0].Sensors[0].Value; got != 43.85 {
		t.Errorf("composite = %v, want 43.85", got)
	}
	if got := chips[0].Sensors[0].Name(); got != "Composite" {
		t.Errorf("Name() = %q, want Composite", got)
	}
}

func TestDefaultNameWhenNoLabel(t *testing.T) {
	cases := []struct {
		prefix string
		index  int
		want   string
	}{
		{"temp", 3, "Temp 3"},
		{"fan", 2, "Fan 2"},
		{"in", 5, "Volt 5"},
		{"curr", 1, "Current 1"},
		{"power", 1, "Power 1"},
	}
	for _, tc := range cases {
		s := Sensor{Prefix: tc.prefix, Index: tc.index}
		if got := s.Name(); got != tc.want {
			t.Errorf("Sensor{%s,%d}.Name() = %q, want %q", tc.prefix, tc.index, got, tc.want)
		}
	}
}

func TestMissingRootIsAnError(t *testing.T) {
	old := Root
	Root = filepath.Join(t.TempDir(), "does-not-exist")
	defer func() { Root = old }()

	if _, err := Read(); err == nil {
		t.Fatal("expected an error for a missing hwmon root")
	}
}

// itoa keeps the test's channel keys readable without pulling in strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
