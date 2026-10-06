// Package gpu discovers GPUs and reports their temperatures and fan speeds.
//
// Three backends are tried, in order of fidelity:
//
//  1. NVIDIA  - NVML via the official go-nvml bindings. Required here, because
//     the proprietary driver does not expose hwmon temperature at all, so
//     there is no sysfs route to a GeForge card's temperature.
//  2. DRM     - /sys/class/drm/card*/device for anything that exports hwmon
//     and gpu_busy_percent: amdgpu, recent i915, nouveau, and most SoC GPUs.
//  3. PCI     - vendor/device identification for display-only adapters with no
//     telemetry of their own, so the UI can still list them as idle.
package gpu

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nathan/sensorz/internal/model"
)

// Backend names, exposed for the diagnostics line in the UI.
const (
	BackendNVML = "nvidia-nvml"
	BackendDRM  = "drm-sysfs"
	BackendPCI  = "pci-id"
	BackendNone = "none"
)

// Collector gathers GPU telemetry. Discovery happens once; each Collect only
// refreshes values.
type Collector struct {
	backends []backend
	// Name is the backend that is actually producing data.
	Name string
}

// New discovers the GPUs present on this machine and picks a backend for each
// one. It never fails: a machine with no GPU simply yields an empty collector
// that reports BackendNone.
func New() *Collector {
	c := &Collector{}

	if nb := newNVMLBackend(); nb != nil {
		c.backends = append(c.backends, nb)
		c.Name = BackendNVML
	}
	if db := newDRMBackend(); db != nil {
		c.backends = append(c.backends, db)
		if c.Name == "" {
			c.Name = BackendDRM
		}
	}
	if pb := newPCIBackend(c.backends); pb != nil {
		c.backends = append(c.backends, pb)
		if c.Name == "" {
			c.Name = BackendPCI
		}
	}
	if len(c.backends) == 0 {
		c.Name = BackendNone
	}
	return c
}

// Close releases any driver resources held by the collector.
func (c *Collector) Close() {
	for _, b := range c.backends {
		if cl, ok := b.(interface{ Close() }); ok {
			cl.Close()
		}
	}
}

// backend is one telemetry source for a set of devices.
type backend interface {
	// Name identifies the backend for diagnostics.
	Name() string
	// Collect refreshes and returns the devices this backend owns.
	Collect(ctx context.Context) ([]model.Device, error)
}

// Collect returns one entry per GPU, sorted by index. Backends that overlap
// with an earlier one are suppressed, so a machine where NVML and sysfs both
// see the same card reports it exactly once.
func (c *Collector) Collect(ctx context.Context) ([]model.Device, error) {
	var (
		out   []model.Device
		seen  = map[string]bool{}
		warns []error
	)
	for _, b := range c.backends {
		devs, err := b.Collect(ctx)
		if err != nil {
			warns = append(warns, fmt.Errorf("%s: %w", b.Name(), err))
		}
		for _, d := range devs {
			// Key on the PCI address when we have one, else the name, so
			// overlapping backends collapse onto the same device.
			key := strings.ToLower(d.PciAddress)
			if key == "" {
				key = strings.ToLower(d.Name + "/" + d.Driver)
			}
			if key != "" {
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })

	// Whatever was read is returned even when a backend failed: one card that
	// will not report a temperature must not hide the two that will. The error
	// rides along so the caller can say which backend is unhappy.
	return out, errors.Join(warns...)
}

// Metrics turns devices into the graphable temperature series the UI renders.
//
// Only temperatures are reported: sensorz is a thermal and airflow monitor, so
// utilisation, memory and power are not collected. A card with no readable
// temperature contributes no series at all rather than a zeroed one.
func Metrics(devs []model.Device) []model.Metric {
	var out []model.Metric
	for _, d := range devs {
		label := deviceLabel(d)
		if d.HasTemperature() {
			out = append(out, model.Metric{
				ID:       "gpu." + strconv.Itoa(d.Index) + ".temp",
				Label:    label,
				Group:    "Temperature",
				Category: model.CategoryGPU,
				Kind:     model.KindTemperature,
				Value:    d.Temperature,
				Min:      20,
				Max:      100,
				Warn:     80,
				Crit:     90,
			})
		}
		for _, t := range d.ExtraTemps {
			if t.Value != t.Value || t.Label == "" {
				continue
			}
			maxT := t.Max
			if maxT <= 0 {
				maxT = 100
			}
			out = append(out, model.Metric{
				ID:       "gpu." + strconv.Itoa(d.Index) + ".temp." + strings.ToLower(t.Label),
				Label:    label,
				Group:    t.Label,
				Category: model.CategoryGPU,
				Kind:     model.KindTemperature,
				Value:    t.Value,
				Min:      20,
				Max:      maxT,
				Warn:     90,
				Crit:     100,
			})
		}
	}
	return out
}

// ReadableDevices returns the GPUs that produced a temperature this tick, which
// is what decides whether a GPU panel is worth drawing at all.
func ReadableDevices(devs []model.Device) []model.Device {
	var out []model.Device
	for _, d := range devs {
		if d.HasTemperature() {
			out = append(out, d)
		}
	}
	return out
}

func deviceLabel(d model.Device) string {
	if d.Name == "" {
		return "GPU " + strconv.Itoa(d.Index)
	}
	return d.Name
}
