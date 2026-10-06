package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// parseHex converts a "#rrggbb" string into its components. It falls back to
// mid grey for anything it cannot parse, so a malformed theme entry degrades to
// a neutral colour rather than panicking mid-render.
func parseHex(s string) (r, g, b uint8) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 128, 128, 128
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 128, 128, 128
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}

// blend mixes two adaptive colours.
//
// Both variants are mixed, because the dashboard is drawn on whatever background
// the terminal has and the mid-tones have to stay readable on both.
func blend(a, b lipgloss.AdaptiveColor, t float64) lipgloss.AdaptiveColor {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	mix := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }

	ar, ag, ab := parseHex(a.Dark)
	br, bg, bb := parseHex(b.Dark)
	lr, lg, lb := parseHex(a.Light)
	mlr, mlg, mlb := parseHex(b.Light)
	return lipgloss.AdaptiveColor{
		Light: formatHex(mix(lr, mlr), mix(lg, mlg), mix(lb, mlb)),
		Dark:  formatHex(mix(ar, br), mix(ag, bg), mix(ab, bb)),
	}
}

// formatHex renders components back into a "#rrggbb" string.
func formatHex(r, g, b uint8) string {
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
