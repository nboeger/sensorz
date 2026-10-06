// Package cpu reports the static facts about the machine's processors.
//
// sensorz draws temperatures and fan speeds, not utilisation, so there is
// nothing here about how busy the CPU is: the CPU panel is built entirely from
// the temperature channels the hwmon collector reports. What this package
// still needs to supply is how many logical CPUs there are, which decides how
// the CPU temperature average is labelled, and the boot time for the header.
package cpu

import (
	"fmt"

	"github.com/prometheus/procfs"
)

// Sample is one look at the processor topology.
type Sample struct {
	// CoreCount is the number of logical CPUs.
	CoreCount int
	// BootTime is the system boot instant, shown in the header.
	BootTime uint64
}

// Collector reads /proc/stat. It is safe for concurrent use.
type Collector struct {
	fs procfs.FS
}

// New builds a CPU collector rooted at the given /proc mount point. An empty
// path uses the live filesystem.
func New(procPath string) (*Collector, error) {
	fs, err := procfs.NewFS(procPath)
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	return &Collector{fs: fs}, nil
}

// Collect reads /proc/stat.
//
// There is no baseline to establish: every fact it reports is a count rather
// than a rate between samples, so the very first pass is already meaningful.
func (c *Collector) Collect() (Sample, error) {
	stat, err := c.fs.Stat()
	if err != nil {
		return Sample{}, fmt.Errorf("cpu: read /proc/stat: %w", err)
	}
	return Sample{
		CoreCount: len(stat.CPU),
		BootTime:  stat.BootTime,
	}, nil
}
