package ui

import (
	"fmt"
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

// Brighten washes a colour toward white.
//
// The focused panel is drawn in a brightened version of its own hue rather than
// in a fixed highlight colour: a box keeps the identity its colour gave it, and
// the cursor is still obvious because it is the only bright box on screen.
func (t Theme) Brighten(c lipgloss.AdaptiveColor, amount float64) lipgloss.AdaptiveColor {
	white := lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#ffffff"}
	return blend(c, white, amount)
}

// Style builds a lipgloss style in the given colour.
func (t Theme) Style(c lipgloss.AdaptiveColor) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c)
}

// meterFill is the solid part of a meter.
const meterFill = '█'

// meterTrack is the empty part of a meter: btop draws the unfilled remainder as
// a row of dots, which reads as "there is room left" instead of as nothing.
const meterTrack = '·'

// Meter renders a horizontal bar of the given width for a 0..1 fill level, the
// way btop draws them: solid blocks up to the level, then a dim dotted track to
// the end of the cell.
//
// The last filled cell uses a partial block, so the bar is smooth at any width
// rather than stepping in whole-cell increments.
func Meter(width int, frac float64, th Theme, color lipgloss.AdaptiveColor) string {
	if width <= 0 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}

	// Total bar capacity in eighths.
	eighths := int(frac*float64(width)*8 + 0.5)
	full := eighths / 8
	rem := eighths % 8

	if full >= width {
		return th.Style(color).Render(repeatRune(meterFill, width))
	}

	filled := repeatRune(meterFill, full)
	if rem > 0 && full < width {
		filled += string(meterRunes[rem])
		full++
	}
	track := strings_Repeat(meterTrack, width-full)
	return th.Style(color).Render(filled) + th.Style(th.Dim).Render(track)
}

// SparkMeter renders a compact single-cell meter, the "▂▅▇" style indicator
// btop puts next to a value.
func SparkMeter(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	if frac == 0 {
		return " "
	}
	return string(meterRunes[int(frac*8+0.5)])
}

func repeatRune(r rune, n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]rune, n)
	for i := range buf {
		buf[i] = r
	}
	return string(buf)
}

// strings_Repeat exists so Meter can build the dotted track without importing
// strings into this file, which otherwise deals only in colours.
func strings_Repeat(r rune, n int) string { return repeatRune(r, n) }

// blend mixes two adaptive colours. Both variants are mixed, because the
// dashboard is drawn on whatever background the terminal has and the
// mid-tones have to stay readable on both.
func blend(a, b lipgloss.AdaptiveColor, t float64) lipgloss.AdaptiveColor {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ar, ag, ab := parseHex(a.Dark)
	br, bg, bb := parseHex(b.Dark)
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }

	lr, lg, lb := parseHex(a.Light)
	mlr, mlg, mlb := parseHex(b.Light)
	return lipgloss.AdaptiveColor{
		Light: formatHex(mix(lr, mlr), mix(lg, mlg), mix(lb, mlb)),
		Dark:  formatHex(mix(ar, br), mix(ag, bg), mix(ab, bb)),
	}
}

// ThresholdLabel returns the warn/crit thresholds as a short string, e.g.
// "70°/90°", for the panel footer.
func ThresholdLabel(warn, crit float64, unit string) string {
	switch {
	case warn <= 0 && crit <= 0:
		return ""
	case crit <= 0:
		return fmt.Sprintf("warn %g%s", warn, unit)
	case warn <= 0:
		return fmt.Sprintf("crit %g%s", crit, unit)
	default:
		return fmt.Sprintf("warn %g%s  crit %g%s", warn, unit, crit, unit)
	}
}

// meterRunes are the eighth-height block characters used for the fractional
// last cell of a meter, from empty to full.
var meterRunes = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
