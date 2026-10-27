package collect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHWMon lays out a fake /sys/class/hwmon tree with one name file per
// device, each with the given mode. Mode 0 simulates the AppArmor denial of
// the strictly confined snap: present but unreadable.
func writeHWMon(t *testing.T, root string, modes ...os.FileMode) {
	t.Helper()
	for i, m := range modes {
		dir := filepath.Join(root, "hwmon"+string(rune('0'+i)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "name"), []byte("coretemp\n"), m); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProbeAccessAllReadable(t *testing.T) {
	hwmon := t.TempDir()
	proc := t.TempDir()
	writeHWMon(t, hwmon, 0o444, 0o444)
	if err := os.WriteFile(filepath.Join(proc, "diskstats"), []byte("stats"), 0o444); err != nil {
		t.Fatal(err)
	}

	rep := probeAccess(hwmon, proc)
	if rep.HWMonDevices != 2 || rep.HWMonDenied != 0 {
		t.Fatalf("got %+v, want 2 devices 0 denied", rep)
	}
	if rep.AllDenied() {
		t.Fatal("AllDenied true on a readable tree")
	}
	if rep.DiskStatsDenied {
		t.Fatal("DiskStatsDenied true on a readable diskstats")
	}
}

func TestProbeAccessAllDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads past file permissions")
	}
	hwmon := t.TempDir()
	proc := t.TempDir()
	writeHWMon(t, hwmon, 0, 0, 0)
	if err := os.WriteFile(filepath.Join(proc, "diskstats"), []byte("stats"), 0); err != nil {
		t.Fatal(err)
	}

	rep := probeAccess(hwmon, proc)
	if rep.HWMonDevices != 3 || rep.HWMonDenied != 3 {
		t.Fatalf("got %+v, want 3 devices 3 denied", rep)
	}
	if !rep.AllDenied() {
		t.Fatal("AllDenied false on a fully denied tree")
	}
	if !rep.DiskStatsDenied {
		t.Fatal("DiskStatsDenied false on an unreadable diskstats")
	}
}

func TestProbeAccessPartiallyDeniedIsNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads past file permissions")
	}
	hwmon := t.TempDir()
	proc := t.TempDir()
	writeHWMon(t, hwmon, 0o444, 0)

	rep := probeAccess(hwmon, proc)
	if rep.AllDenied() {
		t.Fatal("AllDenied true with one of two devices readable")
	}
}

func TestProbeAccessNoDevicesIsNotFatal(t *testing.T) {
	rep := probeAccess(t.TempDir(), t.TempDir())
	if rep.HWMonDevices != 0 {
		t.Fatalf("got %d devices on an empty tree", rep.HWMonDevices)
	}
	if rep.AllDenied() {
		t.Fatal("AllDenied true on a machine with no hwmon at all")
	}
}

func TestGuidanceSnapBranch(t *testing.T) {
	rep := AccessReport{HWMonDevices: 2, HWMonDenied: 2, DiskStatsDenied: true}
	msg := rep.guidance("sensorz")
	for _, want := range []string{
		"permission denied on every device under /sys/class/hwmon",
		"/proc/diskstats",
		"sudo snap connect sensorz:hardware-observe",
		"sudo snap connect sensorz:system-observe",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("snap guidance missing %q:\n%s", want, msg)
		}
	}
}

func TestGuidanceGenericBranch(t *testing.T) {
	rep := AccessReport{HWMonDevices: 1, HWMonDenied: 1}
	msg := rep.guidance("")
	if !strings.Contains(msg, "world-readable") {
		t.Errorf("generic guidance missing the permission hint:\n%s", msg)
	}
	if strings.Contains(msg, "snap connect") {
		t.Errorf("generic guidance mentions snap commands:\n%s", msg)
	}
}
