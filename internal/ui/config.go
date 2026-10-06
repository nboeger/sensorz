package ui

import "time"

// Config holds the runtime knobs. Values come from the config file and the
// command line; the defaults are what sensorz uses with no configuration.
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
	// SensorFilter decides which hwmon channels are surfaced.
	SensorFilter SensorFilter
	// Hostname overrides the name shown in the header. Empty uses the system
	// hostname.
	Hostname string
	// GPUBackend names the GPU telemetry source in use ("nvidia-nvml",
	// "drm-sysfs", "pci-id"), shown when a GPU has no readings.
	GPUBackend string
}

// SensorFilter mirrors sensors.Filter without importing it, so the config file
// package does not have to know about the collector internals.
type SensorFilter struct {
	// ExcludeChips are chip names to hide entirely.
	ExcludeChips []string `toml:"exclude_chips"`
	// ExcludeChannels are "<chip>/<label>" entries to hide.
	ExcludeChannels []string `toml:"exclude_channels"`
	// MaxChannelsPerChip caps how many channels of one kind are listed per
	// chip.
	MaxChannelsPerChip int `toml:"max_channels_per_chip"`
}

// DefaultConfig returns the configuration sensorz uses with no config file.
func DefaultConfig() Config {
	return Config{
		CollectInterval: 2 * time.Second,
		RefreshInterval: 2 * time.Second,
		HistoryCapacity: 256,
		SensorFilter: SensorFilter{
			// The ACPI thermal zone merely mirrors the motherboard sensor that
			// nct6798 already reports, so it would be a duplicate row.
			ExcludeChips:       []string{"acpitz"},
			MaxChannelsPerChip: 24,
		},
	}
}
