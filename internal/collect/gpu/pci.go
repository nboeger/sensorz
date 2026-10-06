package gpu

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nathan/sensorz/internal/model"
)

const pciRoot = "/sys/bus/pci/devices"

// pciBackend lists display adapters that expose no telemetry at all, so the
// UI can still show "there is a GPU here and it is idle" instead of silently
// omitting a card. It only reports devices that no other backend claimed.
type pciBackend struct {
	claimed map[string]bool
	cards   []pciCard
}

type pciCard struct {
	Dev    string
	Name   string
	Vendor string
	Driver string
	Pci    string
	index  int
}

// newPCIBackend scans the PCI bus for display class devices that the
// telemetry backends passed in did not already take.
func newPCIBackend(taken []backend) *pciBackend {
	entries, err := os.ReadDir(pciRoot)
	if err != nil {
		return nil
	}

	b := &pciBackend{claimed: claimedPCI(taken)}

	for _, e := range entries {
		dev := e.Name() // 0000:01:00.0
		class := readFile(filepath.Join(pciRoot, dev, "class"))
		// 0x03xxxx is the display controller class; 0x038000 covers the
		// other-class VGA devices found on some docks.
		if !strings.HasPrefix(class, "0x03") {
			continue
		}
		if b.claimed[dev] {
			continue
		}
		c := pciCard{
			Dev:    dev,
			Pci:    dev,
			Vendor: vendorOf(filepath.Join(pciRoot, dev)),
			Driver: basename(readFile(filepath.Join(pciRoot, dev, "driver"))),
			Name:   pciCardName(dev),
		}
		c.index = len(b.cards)
		b.cards = append(b.cards, c)
	}
	if len(b.cards) == 0 {
		return nil
	}
	sort.Slice(b.cards, func(i, j int) bool { return b.cards[i].Pci < b.cards[j].Pci })
	// Renumber after the sort so indices ascend with the listing.
	for i := range b.cards {
		b.cards[i].index = i
	}
	return b
}

func (b *pciBackend) Name() string { return BackendPCI }

// Collect returns the unmonitored adapters with zeroed telemetry. The UI marks
// these explicitly so an idle GPU is not mistaken for a broken sensor.
func (b *pciBackend) Collect(ctx context.Context) ([]model.Device, error) {
	out := make([]model.Device, 0, len(b.cards))
	for _, c := range b.cards {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out = append(out, model.Device{
			Index: c.index,
			Name:  c.Name,
			// No telemetry means no temperature; NaN says so honestly where a
			// zero would be indistinguishable from a real reading.
			Temperature: math.NaN(),
			FanPercent:  -1,
			Vendor:      c.Vendor,
			Driver:      c.Driver,
			PciAddress:  c.Pci,
			Err:         "no telemetry exposed by this driver",
		})
	}
	return out, nil
}

// claimedPCI lists the PCI addresses already owned by the telemetry backends,
// so the PCI backend only reports what nobody else covers.
func claimedPCI(taken []backend) map[string]bool {
	out := map[string]bool{}
	for _, b := range taken {
		switch v := b.(type) {
		case *nvmlBackend:
			for _, d := range v.dev {
				if d.info.PciAddress != "" {
					out[d.info.PciAddress] = true
				}
			}
		case *drmBackend:
			for _, c := range v.cards {
				if c.Pci != "" {
					out[c.Pci] = true
				}
			}
		}
	}
	return out
}

// pciCardName builds a readable name from the device id, falling back to the
// vendor and PCI address when the id is unknown.
func pciCardName(dev string) string {
	id := readFile(filepath.Join(pciRoot, dev, "device"))
	vendor := readFile(filepath.Join(pciRoot, dev, "vendor"))
	if id == "" {
		if v := pciVendorName(vendor); v != "" {
			return v + " " + dev
		}
		return "Display adapter " + dev
	}
	if name := pciVendorName(vendor); name != "" {
		return name + " " + strings.ToUpper(strings.TrimPrefix(id, "0x"))
	}
	return "GPU " + dev
}

func pciVendorName(vendorID string) string {
	switch vendorID {
	case "0x1002", "0x1022":
		return "AMD"
	case "0x10de":
		return "NVIDIA"
	case "0x8086":
		return "Intel"
	case "0x1af4":
		return "VMware"
	case "0x15ad":
		return "VMware"
	case "0x1234":
		return "QEMU"
	case "0x80ee":
		return "VirtualBox"
	case "0x1414":
		return "Microsoft"
	default:
		return ""
	}
}
