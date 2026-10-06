package gpu

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/nathan/sensorz/internal/model"
)

// nvmlBackend talks to the NVIDIA driver through NVML.
//
// NVML is the only way to read temperature, fan and power on an NVIDIA card
// under the proprietary driver: the driver registers no hwmon device, so
// /sys/class/hwmon has nothing to offer. That makes this backend mandatory
// rather than optional on NVIDIA machines.
type nvmlBackend struct {
	lib       nvml.Interface
	dev       []nvmlDevice
	closeOnce sync.Once
}

type nvmlDevice struct {
	handle nvml.Device
	info   model.Device
}

// newNVMLBackend initialises NVML and enumerates the visible NVIDIA devices.
// It returns nil when NVML is unavailable, which is the normal case on a
// machine with no NVIDIA card or with the open kernel modules unloaded.
func newNVMLBackend() *nvmlBackend {
	lib := nvml.New()
	if r := lib.Init(); r != nvml.SUCCESS {
		return nil
	}
	count, r := lib.DeviceGetCount()
	if r != nvml.SUCCESS || count == 0 {
		lib.Shutdown()
		return nil
	}

	b := &nvmlBackend{lib: lib}
	for i := 0; i < count; i++ {
		h, r := lib.DeviceGetHandleByIndex(i)
		if r != nvml.SUCCESS {
			continue
		}
		d := model.Device{Index: len(b.dev)}
		// Start from "no sensor" rather than zero: the UI keys the GPU panel
		// off whether a temperature was actually read, and a zeroed card would
		// draw a plausible but completely fictional graph.
		d.Temperature = math.NaN()
		if name, r := h.GetName(); r == nvml.SUCCESS {
			d.Name = strings.TrimSpace(string(name[:]))
		}
		d.Driver = "nvidia"
		if pci, r := h.GetPciInfo(); r == nvml.SUCCESS {
			d.PciAddress = formatPCI(pci.Domain, pci.Bus, pci.Device)
		}
		// -1 rather than 0: the card has a fan we have not read yet, which is
		// not the same as a fan we read as stopped.
		d.FanPercent = -1
		b.dev = append(b.dev, nvmlDevice{handle: h, info: d})
	}
	if len(b.dev) == 0 {
		lib.Shutdown()
		return nil
	}
	return b
}

func (b *nvmlBackend) Name() string { return BackendNVML }

// Close shuts NVML down. Calling it more than once is a no-op.
func (b *nvmlBackend) Close() {
	b.closeOnce.Do(func() { b.lib.Shutdown() })
}

// Collect refreshes every NVIDIA device.
func (b *nvmlBackend) Collect(ctx context.Context) ([]model.Device, error) {
	out := make([]model.Device, 0, len(b.dev))
	var firstErr error

	for _, nd := range b.dev {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		d := nd.info

		if t, r := nd.handle.GetTemperature(nvml.TEMPERATURE_GPU); r == nvml.SUCCESS {
			d.Temperature = float64(t)
		} else if firstErr == nil {
			firstErr = fmt.Errorf("temperature: %s", nvml.ErrorString(r))
		}
		if f, r := nd.handle.GetFanSpeed(); r == nvml.SUCCESS {
			d.FanPercent = float64(f)
		}

		// TEMPERATURE_GPU_MAX is the thermal target the card is allowed to
		// reach. It is a more useful ceiling than a hardcoded 100C because it
		// varies by board and by the card's power limit.
		d.ExtraTemps = append(d.ExtraTemps, nvmlTemp(nd.handle, nvml.TEMPERATURE_GPU_MAX, "Limit", 100))
		out = append(out, d)
	}
	return out, firstErr
}

func nvmlTemp(h nvml.Device, sensor nvml.TemperatureSensors, label string, fallbackMax float64) model.ExtraTemp {
	t, r := h.GetTemperature(sensor)
	if r != nvml.SUCCESS {
		return model.ExtraTemp{}
	}
	return model.ExtraTemp{Label: label, Value: float64(t), Max: fallbackMax}
}

// formatPCI renders a PCI address in the canonical sysfs/lspci spelling,
// "0000:01:00.0", so addresses from NVML and from sysfs compare equal and the
// two backends dedupe against each other.
func formatPCI(domain, bus, device uint32) string {
	return fmt.Sprintf("%04x:%02x:%02x.0", domain&0xffff, bus&0xff, device&0x1f)
}
