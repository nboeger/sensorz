package ui

import (
	"fmt"
	"strconv"
	"strings"
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

// formatHex renders components back into a "#rrggbb" string.
func formatHex(r, g, b uint8) string {
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
