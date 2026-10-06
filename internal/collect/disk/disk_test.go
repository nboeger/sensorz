package disk

import "testing"

// Partition and virtual-device filtering decides which drives appear at all.
// Getting it wrong either floods the panel with every mounted slice or, worse,
// silently drops a real drive.
func TestIsVirtual(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Whole devices: must be listed.
		{"nvme0n1", false},
		{"nvme1n1", false},
		{"sda", false},
		{"sdb", false},
		{"vda", false},
		{"hda", false},
		{"mmcblk0", false},

		// Partitions: must not be listed, or throughput is double counted
		// against their parent.
		{"nvme0n1p1", true},
		{"nvme0n1p12", true},
		{"mmcblk0p1", true},
		{"sda1", true},
		{"sda15", true},
		{"vdb2", true},

		// Virtual and non-physical devices: never interesting.
		{"loop0", true},
		{"loop12", true},
		{"ram0", true},
		{"zram0", true},
		{"dm-0", true},
		{"sr0", true},
		{"md0", true},
		{"nbd0", true},
	}
	for _, tc := range cases {
		if got := isVirtual(tc.name); got != tc.want {
			t.Errorf("isVirtual(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMetricsSkipDrivesWithoutTemperatures(t *testing.T) {
	// A drive with no temperature sensor must not produce a 0C graph, which
	// would look like a dangerously cold drive rather than a missing sensor.
	s := Sample{Devices: []Device{
		{Name: "nvme0n1", HasTemp: true, Temperature: 42, TempCritical: 85},
		{Name: "sda"}, // no temperature
	}}
	ms := s.Metrics()
	for _, m := range ms {
		if m.Label == "sda" {
			t.Errorf("sda produced a %s series despite having no sensor", m.Kind)
		}
		if m.Kind != "temperature" {
			t.Errorf("%s: only temperatures are collected, got kind %q", m.ID, m.Kind)
		}
	}
	// The NVMe drive's critical temperature should scale its thresholds.
	for _, m := range ms {
		if m.ID == "disk.nvme0n1.temp" {
			if m.Crit <= m.Warn {
				t.Errorf("temp thresholds are not ordered: warn=%v crit=%v", m.Warn, m.Crit)
			}
			if m.Warn != 85*0.8 {
				t.Errorf("warn = %v, want 80%% of the drive's 85C critical", m.Warn)
			}
		}
	}
}
