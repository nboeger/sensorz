package cpu

import "testing"

// The CPU package supplies the core count, which is what decides whether the
// temperature graph is labelled "CPU" or "CPU x16", so a wrong count is
// directly visible in the UI.
func TestCollectReportsTopology(t *testing.T) {
	c, err := New("/proc")
	if err != nil {
		t.Skipf("no procfs: %v", err)
	}
	s, err := c.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if s.CoreCount <= 0 {
		t.Errorf("core count = %d, want at least 1", s.CoreCount)
	}
	if s.BootTime == 0 {
		t.Error("boot time was not reported")
	}
	t.Logf("%d logical CPUs, booted at unix %d", s.CoreCount, s.BootTime)
}
