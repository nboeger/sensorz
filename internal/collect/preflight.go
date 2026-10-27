package collect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultSysPath is where the hwmon devices live.
const defaultSysPath = "/sys"

// AccessReport describes which of the kernel interfaces sensorz depends on
// are unreadable because of file permissions.
//
// It exists for one failure mode in particular: the strictly confined snap
// whose hardware-observe and system-observe plugs are not connected. In that
// state every read of /sys/class/hwmon and /proc/diskstats is denied, and
// without a startup check the dashboard opens with every panel empty and no
// hint why. A snap in that state wants a fix-it command, not a blank screen.
type AccessReport struct {
	// HWMonDevices is the number of hwmon devices present on the machine.
	HWMonDevices int
	// HWMonDenied is the number of those devices unreadable due to
	// permissions (EACCES).
	HWMonDenied int
	// DiskStatsDenied reports /proc/diskstats being unreadable due to
	// permissions.
	DiskStatsDenied bool
}

// AllDenied reports the broken-snap state: the machine has hwmon devices and
// every single one of them is unreadable.
func (r AccessReport) AllDenied() bool {
	return r.HWMonDevices > 0 && r.HWMonDenied == r.HWMonDevices
}

// ProbeAccess reads one file per hwmon device and /proc/diskstats, recording
// permission failures only. Other read errors (a sensor that vanished
// between glob and read, say) are not access problems and are ignored.
func ProbeAccess(procPath string) AccessReport {
	if procPath == "" {
		procPath = defaultProcPath
	}
	return probeAccess(filepath.Join(defaultSysPath, "class", "hwmon"), procPath)
}

// probeAccess is ProbeAccess with the roots parameterised for tests.
func probeAccess(hwmonPath, procPath string) AccessReport {
	var rep AccessReport
	names, _ := filepath.Glob(filepath.Join(hwmonPath, "hwmon*", "name"))
	rep.HWMonDevices = len(names)
	for _, n := range names {
		if _, err := os.ReadFile(n); errors.Is(err, os.ErrPermission) {
			rep.HWMonDenied++
		}
	}
	if _, err := os.ReadFile(filepath.Join(procPath, "diskstats")); errors.Is(err, os.ErrPermission) {
		rep.DiskStatsDenied = true
	}
	return rep
}

// Guidance explains how to repair a fully denied AccessReport. The message
// leads with the fix when the process is a snap, because the strictly
// confined snap with unconnected interfaces is the case that ships broken
// out of the box; anywhere else it falls back to generic permission advice.
func (r AccessReport) Guidance() string {
	return r.guidance(os.Getenv("SNAP_NAME"))
}

// guidance composes the message; separated from Guidance so tests can pick
// the branch without faking the snap environment.
func (r AccessReport) guidance(snapName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "cannot read any sensor data: permission denied on every device under /sys/class/hwmon")
	if r.DiskStatsDenied {
		fmt.Fprintf(&b, " and on /proc/diskstats")
	}
	if snapName != "" {
		// SNAP_NAME is set for classic and devmode snaps too, but nothing
		// denies their reads, so guidance only runs for the strictly
		// confined snap whose interfaces are not connected.
		fmt.Fprintf(&b, `.

The %[1]s snap is strictly confined and its hardware-observe and
system-observe interfaces are not connected, so AppArmor refuses every read
of the sensor files. Connect them, then start %[1]s again:

    sudo snap connect %[1]s:hardware-observe
    sudo snap connect %[1]s:system-observe
`, snapName)
	} else {
		fmt.Fprintf(&b, `.

The hwmon sysfs files are normally world-readable. Check for a mandatory
access control policy denying the read (SELinux or AppArmor) or a udev rule
that tightened the permissions.
`)
	}
	return b.String()
}
