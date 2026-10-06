package ui

import (
	"math"

	"github.com/charmbracelet/lipgloss"
)

// Theme holds every colour the dashboard uses. Colours are lipgloss
// AdaptiveColor values so sensorz follows the terminal's light or dark
// background instead of washing out in one of them.
//
// The palette is btop's: a pale green for anything healthy, ramping through
// yellow to red as a reading approaches its limit, and one hue per panel box so
// the dashboard reads as a set of separate instruments rather than one grid.
type Theme struct {
	Border lipgloss.AdaptiveColor
	Title  lipgloss.AdaptiveColor
	Label  lipgloss.AdaptiveColor
	Value  lipgloss.AdaptiveColor
	Dim    lipgloss.AdaptiveColor

	// PanelCPU, PanelGPU, PanelFans, PanelDrives and PanelBoard colour each
	// box's border and title. Compute boxes are green and the rest take a
	// second and third hue, which is what makes the layout legible at a
	// glance in a glance-heavy terminal.
	PanelCPU    lipgloss.AdaptiveColor
	PanelGPU    lipgloss.AdaptiveColor
	PanelFans   lipgloss.AdaptiveColor
	PanelDrives lipgloss.AdaptiveColor
	PanelBoard  lipgloss.AdaptiveColor

	// Good, Warn and Bad are the meter and graph ramp. A reading at or below
	// the warning threshold is Good, between Warn and Bad is Warn, above is
	// Bad.
	Good lipgloss.AdaptiveColor
	Warn lipgloss.AdaptiveColor
	Bad  lipgloss.AdaptiveColor

	// Accent highlights the selected panel and the header rule.
	Accent lipgloss.AdaptiveColor
}

// DefaultTheme returns the palette sensorz ships with: btop's pale green for a
// healthy reading, ramping through yellow to red as it nears the limit.
//
// Everything is deliberately pale. A saturated green on a black terminal reads
// as an alarm, and this dashboard is mostly showing temperatures that are
// perfectly fine; the colours have to stay quiet enough that the ones that do
// mean something stand out.
func DefaultTheme() Theme {
	// Each colour needs a light and a dark variant: the light variant has to be
	// dark enough to read on white, the dark one bright enough to read on
	// black, so they are not simply inversions of each other.
	//
	// Both hues are desaturated and darkened on purpose. A saturated green is
	// an alarm, and this dashboard spends most of its time showing readings
	// that are fine; the box colours are structure, not status, and the status
	// is carried by the reading's own colour.
	//
	// Every box wears the same dark pale purple. One hue for the whole
	// dashboard keeps the chrome out of the way: the eye should find the
	// reading, not the frame around it, and the only colour on screen that
	// means anything is the one the reading is drawn in.
	purple := lipgloss.AdaptiveColor{Light: "#56456a", Dark: "#8f7fb0"}

	return Theme{
		Border: lipgloss.AdaptiveColor{Light: "#bcbcbc", Dark: "#4a4a4a"},
		Title:  lipgloss.AdaptiveColor{Light: "#4d6b38", Dark: "#b8d3a8"},
		Label:  lipgloss.AdaptiveColor{Light: "#6f6f6f", Dark: "#d4d4d4"},
		Value:  lipgloss.AdaptiveColor{Light: "#2a2a2a", Dark: "#f2f2f2"},
		Dim:    lipgloss.AdaptiveColor{Light: "#9a9a9a", Dark: "#8a8a8a"},

		PanelCPU:    purple,
		PanelGPU:    purple,
		PanelFans:   purple,
		PanelDrives: purple,
		PanelBoard:  purple,

		Good: lipgloss.AdaptiveColor{Light: "#4d6b38", Dark: "#79a86a"},
		Warn: lipgloss.AdaptiveColor{Light: "#a38600", Dark: "#ffe98a"},
		Bad:  lipgloss.AdaptiveColor{Light: "#b8453f", Dark: "#ffa0a0"},

		Accent: lipgloss.AdaptiveColor{Light: "#4d6b38", Dark: "#b8d3a8"},
	}
}

// Color returns the colour a metric's current value should be drawn in.
func (t Theme) Color(value, warn, crit float64) lipgloss.AdaptiveColor {
	switch {
	case crit > 0 && value >= crit:
		return t.Bad
	case warn > 0 && value >= warn:
		return t.Warn
	default:
		return t.Good
	}
}

// Ramp colours a reading by how close it is to the danger zone rather than in
// three flat bands: pale green while there is room, warming through yellow as
// the warning point comes into range, and red on the way to the critical
// temperature.
//
// This is what makes a graph readable as a temperature rather than as a shape:
// a line that is yellow has already left the comfortable range, even if it has
// not yet reached the number people call "hot".
//
// A metric with no thresholds (fan speed) stays green, because more of it is
// not worse.
func (t Theme) Ramp(value, warn, crit float64) lipgloss.AdaptiveColor {
	if crit <= 0 || warn <= 0 || crit <= warn {
		return t.Color(value, warn, crit)
	}
	// Start warming at the warning point rather than at zero: a CPU sitting at
	// 40C on a 100C part is not half way to a problem.
	span := crit - warn
	r := (value - warn) / span
	switch {
	case r <= 0:
		return t.Good
	case r < 0.5:
		return blend(t.Good, t.Warn, r*2)
	default:
		return blend(t.Warn, t.Bad, math.Min(1, (r-0.5)*2))
	}
}

// Brighten washes a colour toward white. The focused panel is drawn in a
// brightened version of its own hue, so a box keeps the identity its colour gave
// it and the cursor is still the only bright box on screen.
func (t Theme) Brighten(c lipgloss.AdaptiveColor, amount float64) lipgloss.AdaptiveColor {
	return blend(c, lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#ffffff"}, amount)
}

// Style builds a lipgloss style in the given colour.
func (t Theme) Style(c lipgloss.AdaptiveColor) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c)
}

// ThemeByName returns the theme with the given name, or DefaultTheme if not found.
func ThemeByName(name string) Theme {
	switch name {
	case "dracula":
		return draculaTheme()
	case "nord":
		return nordTheme()
	case "solarized-dark":
		return solarizedDarkTheme()
	case "solarized-light":
		return solarizedLightTheme()
	default:
		return DefaultTheme()
	}
}

// AvailableThemes returns the list of available theme names.
func AvailableThemes() []string {
	return []string{"default", "dracula", "nord", "solarized-dark", "solarized-light"}
}

// draculaTheme is a vibrant theme with distinct colors per element.
func draculaTheme() Theme {
	return Theme{
		Border:      lipgloss.AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
		Title:       lipgloss.AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
		Label:       lipgloss.AdaptiveColor{Light: "#f8f8f2", Dark: "#f8f8f2"},
		Value:       lipgloss.AdaptiveColor{Light: "#f8f8f2", Dark: "#f8f8f2"},
		Dim:         lipgloss.AdaptiveColor{Light: "#6272a4", Dark: "#6272a4"},
		PanelCPU:    lipgloss.AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
		PanelGPU:    lipgloss.AdaptiveColor{Light: "#bd93f9", Dark: "#bd93f9"},
		PanelFans:   lipgloss.AdaptiveColor{Light: "#8be9fd", Dark: "#8be9fd"},
		PanelDrives: lipgloss.AdaptiveColor{Light: "#ffb86c", Dark: "#ffb86c"},
		PanelBoard:  lipgloss.AdaptiveColor{Light: "#ff79c6", Dark: "#ff79c6"},
		Good:        lipgloss.AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
		Warn:        lipgloss.AdaptiveColor{Light: "#f1fa8c", Dark: "#f1fa8c"},
		Bad:         lipgloss.AdaptiveColor{Light: "#ff5555", Dark: "#ff5555"},
		Accent:      lipgloss.AdaptiveColor{Light: "#50fa7b", Dark: "#50fa7b"},
	}
}

// nordTheme is a minimalist, cool theme with a consistent color palette.
func nordTheme() Theme {
	return Theme{
		Border:      lipgloss.AdaptiveColor{Light: "#4c566a", Dark: "#4c566a"},
		Title:       lipgloss.AdaptiveColor{Light: "#a3be8c", Dark: "#a3be8c"},
		Label:       lipgloss.AdaptiveColor{Light: "#d8dee9", Dark: "#d8dee9"},
		Value:       lipgloss.AdaptiveColor{Light: "#eceff4", Dark: "#eceff4"},
		Dim:         lipgloss.AdaptiveColor{Light: "#4c566a", Dark: "#4c566a"},
		PanelCPU:    lipgloss.AdaptiveColor{Light: "#a3be8c", Dark: "#a3be8c"},
		PanelGPU:    lipgloss.AdaptiveColor{Light: "#81a1c1", Dark: "#81a1c1"},
		PanelFans:   lipgloss.AdaptiveColor{Light: "#88c0d0", Dark: "#88c0d0"},
		PanelDrives: lipgloss.AdaptiveColor{Light: "#b48ead", Dark: "#b48ead"},
		PanelBoard:  lipgloss.AdaptiveColor{Light: "#d08770", Dark: "#d08770"},
		Good:        lipgloss.AdaptiveColor{Light: "#a3be8c", Dark: "#a3be8c"},
		Warn:        lipgloss.AdaptiveColor{Light: "#ebcb8b", Dark: "#ebcb8b"},
		Bad:         lipgloss.AdaptiveColor{Light: "#bf616a", Dark: "#bf616a"},
		Accent:      lipgloss.AdaptiveColor{Light: "#a3be8c", Dark: "#a3be8c"},
	}
}

// solarizedDarkTheme uses the scientific Solarized palette (dark variant).
func solarizedDarkTheme() Theme {
	return Theme{
		Border:      lipgloss.AdaptiveColor{Light: "#586e75", Dark: "#586e75"},
		Title:       lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
		Label:       lipgloss.AdaptiveColor{Light: "#93a1a1", Dark: "#93a1a1"},
		Value:       lipgloss.AdaptiveColor{Light: "#eee8d5", Dark: "#eee8d5"},
		Dim:         lipgloss.AdaptiveColor{Light: "#657b83", Dark: "#657b83"},
		PanelCPU:    lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
		PanelGPU:    lipgloss.AdaptiveColor{Light: "#268bd2", Dark: "#268bd2"},
		PanelFans:   lipgloss.AdaptiveColor{Light: "#2aa198", Dark: "#2aa198"},
		PanelDrives: lipgloss.AdaptiveColor{Light: "#b58900", Dark: "#b58900"},
		PanelBoard:  lipgloss.AdaptiveColor{Light: "#d33682", Dark: "#d33682"},
		Good:        lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
		Warn:        lipgloss.AdaptiveColor{Light: "#b58900", Dark: "#b58900"},
		Bad:         lipgloss.AdaptiveColor{Light: "#dc322f", Dark: "#dc322f"},
		Accent:      lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
	}
}

// solarizedLightTheme uses the scientific Solarized palette (light variant).
func solarizedLightTheme() Theme {
	return Theme{
		Border:      lipgloss.AdaptiveColor{Light: "#93a1a1", Dark: "#586e75"},
		Title:       lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
		Label:       lipgloss.AdaptiveColor{Light: "#657b83", Dark: "#93a1a1"},
		Value:       lipgloss.AdaptiveColor{Light: "#002b36", Dark: "#eee8d5"},
		Dim:         lipgloss.AdaptiveColor{Light: "#93a1a1", Dark: "#657b83"},
		PanelCPU:    lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
		PanelGPU:    lipgloss.AdaptiveColor{Light: "#268bd2", Dark: "#268bd2"},
		PanelFans:   lipgloss.AdaptiveColor{Light: "#2aa198", Dark: "#2aa198"},
		PanelDrives: lipgloss.AdaptiveColor{Light: "#b58900", Dark: "#b58900"},
		PanelBoard:  lipgloss.AdaptiveColor{Light: "#d33682", Dark: "#d33682"},
		Good:        lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
		Warn:        lipgloss.AdaptiveColor{Light: "#b58900", Dark: "#b58900"},
		Bad:         lipgloss.AdaptiveColor{Light: "#dc322f", Dark: "#dc322f"},
		Accent:      lipgloss.AdaptiveColor{Light: "#859900", Dark: "#859900"},
	}
}
