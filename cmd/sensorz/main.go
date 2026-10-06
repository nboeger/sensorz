// Command sensorz is a terminal temperature and fan monitor for Linux, in the
// spirit of btop: live braille graphs for the CPU, the GPUs, the drives, the
// motherboard sensors and every fan the kernel reports.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nathan/sensorz/internal/collect"
	"github.com/nathan/sensorz/internal/collect/sensors"
	"github.com/nathan/sensorz/internal/history"
	"github.com/nathan/sensorz/internal/ui"
)

// version is the release this binary was built from. It is a variable rather
// than a constant so it can be stamped at build time with
// -ldflags "-X main.version=$(git describe --tags)".
var version = "1.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sensorz:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		interval    = flag.Duration("interval", 2*time.Second, "sensor sampling interval")
		capacity    = flag.Int("history", 256, "samples retained per graph")
		procPath    = flag.String("proc", "/proc", "procfs mount point")
		showAll     = flag.Bool("all-sensors", false, "show every hwmon channel, not just the important ones")
		listSensors = flag.Bool("list-sensors", false, "print the detected sensors and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
		showV      = flag.Bool("v", false, "print the version and exit (shorthand)")
	)
	flag.Parse()

	if *showVersion || *showV {
		fmt.Println("sensorz", version)
		return nil
	}

	filter := sensors.DefaultFilter()
	for _, f := range flag.Args() {
		if len(f) > 0 && f[0] == '-' {
			return fmt.Errorf("unknown flag %q (run sensorz -h)", f)
		}
	}

	agg, err := collect.New(collect.Options{
		Interval:     *interval,
		ProcPath:     *procPath,
		SensorFilter: filter,
	})
	if err != nil {
		return err
	}
	defer agg.Close()

	if *listSensors {
		return listSensorsOnce(agg)
	}

	cfg := ui.DefaultConfig()
	cfg.CollectInterval = *interval
	cfg.RefreshInterval = *interval
	cfg.HistoryCapacity = *capacity
	cfg.GPUBackend = agg.GPUBackend

	hist := history.NewStore(*capacity)
	m := ui.New(agg, hist, cfg, ui.DefaultTheme())
	m.SetShowAllSensors(*showAll)
	defer m.Close()

	// bubbletea takes over the terminal; a second Ctrl-C should still kill the
	// process if something goes wrong during teardown.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithContext(ctx),
		tea.WithMouseCellMotion(),
	)
	return p.Start()
}

// listSensorsOnce prints every sensor sensorz found and exits, which is the
// quickest way to answer "why isn't my temperature showing up".
func listSensorsOnce(agg *collect.Aggregator) error {
	s := agg.Once(context.Background())

	fmt.Printf("gpu backend: %s\n", agg.GPUBackend)
	for _, d := range s.Devices {
		if d.HasTemperature() {
			fmt.Printf("gpu%d  %-28s driver=%-8s pci=%s temp=%.0f°C\n",
				d.Index, d.Name, d.Driver, d.PciAddress, d.Temperature)
		} else {
			fmt.Printf("gpu%d  %-28s driver=%-8s pci=%s no temperature sensor\n",
				d.Index, d.Name, d.Driver, d.PciAddress)
		}
	}

	var counts = map[string]int{}
	for _, mt := range s.Metrics {
		counts[string(mt.Category)]++
	}
	fmt.Println()
	for _, mt := range s.Metrics {
		fmt.Printf("%-34s %-10s %-18s %s\n", mt.ID, mt.Category, mt.Group, mt.Label)
	}
	fmt.Println()
	for _, cat := range []string{"cpu", "gpu", "disk", "sensor"} {
		if counts[cat] == 0 {
			continue
		}
		fmt.Printf("%-10s %d series\n", cat, counts[cat])
	}
	for _, w := range s.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	return nil
}
