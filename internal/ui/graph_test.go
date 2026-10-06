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
		Fill: true, Columns: true, Dots: true,
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

// A cell whose eight dots are all lit is a solid block, and a column chart of
// solid blocks is a rectangle. Every filled cell keeps the dot texture instead.
func TestColumnGraphCellsAreDots(t *testing.T) {
	th := DefaultTheme()
	values := make([]float64, 20)
	for i := range values {
		values[i] = 90
	}
	rows := RenderGraph(values, GraphOptions{
		Width: 8, Height: 6, Min: 0, Max: 100,
		Fill: true, Columns: true, Dots: true,
	}, th)

	dots := 0
	for _, row := range rows {
		for _, r := range stripANSI(row) {
			if r == brailleBase {
				continue // the blank background cell
			}
			if r == brailleBase+0xFF {
				t.Fatalf("a filled cell is solid (%q); the plot must stay a texture", r)
			}
			dots++
		}
	}
	if dots == 0 {
		t.Fatal("the graph drew nothing")
	}
	// A dot pattern lights half of each cell's dots, so a filled graph is
	// about half lit cells; anything much above that is a solid block again.
	if dots > 8*6*8 {
		t.Errorf("%d dots lit, too dense to be a dot texture", dots)
	}
}
