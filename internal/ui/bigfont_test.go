package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The big figure is drawn, not typeset, so every row has to be exactly as wide
// as the figure or the panel border will not line up.
func TestBigNumberGeometry(t *testing.T) {
	for _, text := range []string{"0", "78°C", "133 RPM", "1950"} {
		for _, scale := range []int{1, 2} {
			rows := BigNumber(text, scale)
			if len(rows) != bigGlyphHeight {
				t.Errorf("BigNumber(%q) has %d rows, want %d", text, len(rows), bigGlyphHeight)
			}
			want := BigNumberWidth(text, scale)
			for i, r := range rows {
				if got := lipgloss.Width(r); got != want {
					t.Errorf("BigNumber(%q, %d) row %d is %d cells, want %d", text, scale, i, got, want)
				}
			}
		}
	}
	if got := BigNumberWidth("", 2); got != 0 {
		t.Errorf("BigNumberWidth(\"\") = %d, want 0", got)
	}
	// A scale below one would divide the width away; it has to clamp instead.
	if lipgloss.Width(BigNumber("8", 0)[0]) != BigNumberWidth("8", 1) {
		t.Error("scale 0 did not clamp to 1")
	}
}

// An unknown rune must render as blank space, never as a box: a missing degree
// sign would otherwise turn the headline number into a row of tofu.
func TestBigNumberUnknownRunesAreBlank(t *testing.T) {
	rows := BigNumber("~", 2)
	for i, r := range rows {
		if strings.TrimSpace(r) != "" {
			t.Errorf("row %d drew something for an unknown rune: %q", i, r)
		}
	}
}

// The digits have to be distinguishable from each other, which is the whole
// point of a bitmap font: a 1 that looks like a 7, or a 0 with a slash missing,
// is unreadable at a glance no matter how big it is.
func TestBigDigitsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, d := range []rune("0123456789") {
		lit := ""
		for _, row := range BigNumber(string(d), 1) {
			lit += row
		}
		if strings.TrimSpace(lit) == "" {
			t.Errorf("digit %q draws nothing", string(d))
		}
		if other, ok := seen[lit]; ok {
			t.Errorf("digits %q and %q draw the same pixels", other, string(d))
		}
		seen[lit] = string(d)
	}
}
