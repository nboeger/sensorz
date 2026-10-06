// Package collect orchestrates the individual collectors into a single
// snapshot per tick.
package collect

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nathan/sensorz/internal/collect/cpu"
	"github.com/nathan/sensorz/internal/collect/disk"
	"github.com/nathan/sensorz/internal/collect/gpu"
	"github.com/nathan/sensorz/internal/collect/sensors"
	"github.com/nathan/sensorz/internal/model"
)

// defaultProcPath is where the collectors read kernel counters from.
const defaultProcPath = "/proc"

// Options configures the aggregator.
type Options struct {
	// Interval is how often a snapshot is produced.
	Interval time.Duration
	// ProcPath overrides the /proc mount point, empty for the live system.
	ProcPath string
	// SensorFilter decides which hwmon channels are surfaced.
	SensorFilter sensors.Filter
	// HistoryCapacity is how many samples the history store retains per
	// series. Defaults to 256.
	HistoryCapacity int
}

// Aggregator runs every collector on a ticker and publishes snapshots.
//
// There is no memory, disk throughput or network collector here: sensorz
// reports temperatures and fan speeds, so a machine with no hwmon sensors at
// all still gets whatever the CPU, GPU and drive collectors can read.
type Aggregator struct {
	opts Options

	cpu  *cpu.Collector
	disk *disk.Collector
	gpu  *gpu.Collector
	sens *sensors.Collector

	// GPUBackend names the telemetry source in use, for the diagnostics line.
	GPUBackend string

	snap *model.Snapshot

	mu   sync.Mutex
	done chan struct{}
}

// New builds an Aggregator, probing every source. Individual source failures
// are tolerated: a machine without NVML, without hwmon or without GPUs still
// gets a working dashboard for everything else.
func New(opts Options) (*Aggregator, error) {
	if opts.Interval <= 0 {
		opts.Interval = 2 * time.Second
	}
	if opts.HistoryCapacity <= 0 {
		opts.HistoryCapacity = 256
	}
	// procfs rejects an empty mount point outright, so normalise the zero
	// value to the live filesystem here rather than in every collector.
	if opts.ProcPath == "" {
		opts.ProcPath = defaultProcPath
	}

	a := &Aggregator{opts: opts, done: make(chan struct{})}

	var err error
	if a.cpu, err = cpu.New(opts.ProcPath); err != nil {
		return nil, err
	}
	if a.disk, err = disk.New(opts.ProcPath); err != nil {
		return nil, err
	}
	a.gpu = gpu.New()
	a.GPUBackend = a.gpu.Name
	a.sens = sensors.New(opts.SensorFilter)
	return a, nil
}

// Close releases driver resources.
func (a *Aggregator) Close() {
	close(a.done)
	if a.gpu != nil {
		a.gpu.Close()
	}
}

// Latest returns the most recent snapshot, never nil.
func (a *Aggregator) Latest() model.Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snap == nil {
		return model.Snapshot{Time: time.Now()}
	}
	return *a.snap
}

// Run produces a snapshot every interval until ctx is cancelled or Close is
// called. Each snapshot is handed to onSnapshot.
func (a *Aggregator) Run(ctx context.Context, onSnapshot func(model.Snapshot)) {
	t := time.NewTicker(a.opts.Interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.done:
			return
		case <-t.C:
			s := a.Once(ctx)
			a.mu.Lock()
			a.snap = &s
			a.mu.Unlock()
			if onSnapshot != nil {
				onSnapshot(s)
			}
		}
	}
}

// Once runs a single collection pass across every source.
//
// Sources run concurrently because they are independent: reading the NVMe
// temperature must not delay the CPU numbers. A source that fails contributes
// a warning and no data rather than aborting the pass, since a monitoring tool
// that goes blank on one transient read error is worse than one that shows
// slightly stale data.
func (a *Aggregator) Once(ctx context.Context) model.Snapshot {
	type result struct {
		metrics []model.Metric
		warns   []string
	}
	results := make([]result, 3)
	names := []string{"cpu", "disk", "sensors"}

	var wg sync.WaitGroup
	run := func(i int, fn func() ([]model.Metric, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := fn()
			if err != nil {
				results[i].warns = append(results[i].warns, err.Error())
				return
			}
			results[i].metrics = m
		}()
	}

	var cores int
	run(0, func() ([]model.Metric, error) {
		s, err := a.cpu.Collect()
		if err != nil {
			return nil, err
		}
		cores = s.CoreCount
		return nil, nil
	})
	run(1, func() ([]model.Metric, error) {
		s, err := a.disk.Collect()
		if err != nil {
			return nil, err
		}
		return s.Metrics(), nil
	})
	run(2, func() ([]model.Metric, error) {
		return a.sens.Collect()
	})

	// GPU collection is separate because it owns a driver handle and is the
	// slowest of the bunch.
	var (
		devs []model.Device
		gpuW []string
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		devs, err = a.gpu.Collect(ctx)
		if err != nil {
			gpuW = append(gpuW, err.Error())
		}
	}()

	wg.Wait()

	snap := model.Snapshot{Time: time.Now(), Devices: devs, Cores: cores}
	for i, r := range results {
		snap.Metrics = append(snap.Metrics, r.metrics...)
		for _, w := range r.warns {
			snap.Warnings = append(snap.Warnings, names[i]+": "+w)
		}
	}
	for _, w := range gpuW {
		snap.Warnings = append(snap.Warnings, "gpu: "+w)
	}

	// GPU temperatures are appended rather than sorted so the GPU graphs sit
	// directly under the CPU ones.
	snap.Metrics = append(snap.Metrics, gpu.Metrics(devs)...)

	// The headline averages go last so they are easy to find, and so their
	// thresholds can be derived from every individual reading.
	derive(&snap)

	for _, d := range devs {
		if d.Err != "" {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("gpu%d (%s): %s", d.Index, d.Name, d.Err))
		}
	}
	return snap
}
