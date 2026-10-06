package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// colorOf renders a theme colour as the escape sequence lipgloss would emit for
// it, so a test can tell a filled row of a bar from an empty one.
func colorOf(c lipgloss.AdaptiveColor) string {
	return strings.Join(colorCodes(lipgloss.NewStyle().Foreground(c).Render("x")), ",")
}

// forceColour turns styling on for the duration of a test. Without a terminal,
// lipgloss strips every colour, and a test that cannot see colour cannot tell a
// filled bar from an empty one.
func forceColour(t *testing.T) {
	t.Helper()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
}

// A column plot stands straight up from the baseline, so half a scale of fill
// means the bottom half of the cells are lit and the top half are not. A curve
// drawn through the same data would put a line across the middle instead.
func TestColumnGraphFillsFromTheBaseline(t *testing.T) {
	th := DefaultTheme()
	values := make([]float64, 40)
	for i := range values {
		values[i] = 50
	}
	rows := RenderGraph(values, GraphOptions{
		Width: 10, Height: 10, Min: 0, Max: 100,
		Fill: true, Columns: true,
	}, th)
	if len(rows) != 10 {
		t.Fatalf("graph is %d rows, want 10", len(rows))
	}

	// A blank cell in a braille plot is U+2800, not a space, so the graph keeps
	// its glyph pitch across an empty region.
	lit := func(row string) bool {
		for _, r := range stripANSI(row) {
			if r != brailleBase {
				return true
			}
		}
		return false
	}
	for y, row := range rows {
		got := lit(row)
		want := y >= 5 // the bottom half of a 0..100 scale at 50
		if got != want {
			t.Errorf("row %d lit = %v, want %v: the fill must stand on the baseline", y, got, want)
		}
	}
}

// Density is the whole look of the plot. A filled cell lights all eight of its
// braille dots, which a terminal draws as a field of eight small dots rather
// than as a solid block: the gaps between the dots are what make the shape
// readable as a shape. Thinning the pattern inside a cell would turn the plot
// into a sparse scatter that no longer reads as a filled region.
func TestColumnGraphFillsEveryDot(t *testing.T) {
	th := DefaultTheme()
	values := make([]float64, 20)
	for i := range values {
		values[i] = 90
	}
	rows := RenderGraph(values, GraphOptions{
		Width: 8, Height: 6, Min: 0, Max: 100,
		Fill: true, Columns: true,
	}, th)

	full := 0
	for _, row := range rows {
		for _, r := range stripANSI(row) {
			switch r {
			case brailleBase:
			case brailleBase + 0xFF:
				full++
			default:
				// Only the top edge of a column may be partial.
				if full > 0 {
					t.Fatalf("a cell inside the fill is partial (%U); the fill must be dense", r)
				}
			}
		}
	}
	// A 90% series over six rows fills about five of them, and eight cells is
	// the width.
	if full < 8*4 {
		t.Errorf("%d fully lit cells, want the fill to be dense across the width", full)
	}
}
