package ui

import "time"

// Config holds the runtime knobs. The command line sets them; the defaults are
// what sensorz uses when it does not.
type Config struct {
	// CollectInterval is how often sensors are sampled. Two seconds is the
	// btop default: fast enough that a burst of load is visible, slow enough
	// that reading a few hundred sysfs files is not measurable load.
	CollectInterval time.Duration
	// RefreshInterval is how often the screen repaints. It is normally equal
	// to CollectInterval; separating them lets the graphs animate smoothly
	// between samples on a slow sensor.
	RefreshInterval time.Duration
	// HistoryCapacity is how many samples are retained per series.
	HistoryCapacity int
	// ShowHelp toggles the keybinding overlay.
	ShowHelp bool
	// Hostname overrides the name shown in the header. Empty uses the system
	// hostname.
	Hostname string
	// GPUBackend names the GPU telemetry source in use ("nvidia-nvml",
	// "drm-sysfs", "pci-id"), shown when a GPU has no readings.
	GPUBackend string
}

// DefaultConfig returns the configuration sensorz uses with no config file.
func DefaultConfig() Config {
	return Config{
		CollectInterval: 2 * time.Second,
		RefreshInterval: 2 * time.Second,
		HistoryCapacity: 256,
	}
}
