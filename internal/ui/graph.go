package ui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// GraphOptions configures how a series is rendered into panel rows.
type GraphOptions struct {
	// Width and Height are the drawable area in terminal cells.
	Width, Height int
	// Min and Max are the vertical scale of the plot. Values outside are
	// clamped so a spike does not silently rescale the whole graph.
	Min, Max float64
	// Fill draws the area under the curve. Sensorz leaves it off and plots the
	// line as braille dots on their own, which is how btop draws its graphs.
	Fill bool
	// Warn and Crit colour the graph. Every dot is drawn in the colour its own
	// reading ramps to, so a line that is partly yellow has already been warm
	// somewhere in the retained window.
	Warn, Crit float64
	// Label and Value are drawn in the top-left corner of the plot, on top of
	// the graph, the way btop overlays its labels.
	Label string
	Value string
	// Thick draws the trace as a band of dot rows rather than a single row, so
	// a wide graph still reads as a line at a glance.
	Thick int
	// Columns drops the line joining the samples and leaves the fill, which
	// draws the series as vertical columns standing on the baseline instead of
	// as a curve across the panel.
	//
	// A column is straight up and down where a curve leans, and for a level
	// that is what the data is: a temperature holds, and the shape of a series
	// that holds is a staircase, not a slope.
	Columns bool
}

// RenderGraph draws one series into exactly Height rows of Width cells.
//
// The vertical scale is deliberately fixed rather than fitted to the data when
// a maximum is supplied: a temperature graph that rescales every frame is
// unreadable, because a steady 60C looks identical to a rising one.
func RenderGraph(values []float64, o GraphOptions, th Theme) []string {
	if o.Width <= 0 || o.Height <= 0 {
		return nil
	}

	lo, hi := o.Min, o.Max
	if hi <= lo {
		hi = lo + 1
	}

	grid := NewGrid(o.Width, o.Height)
	Plot(grid, values, lo, hi, PlotStyle{
		Fill:     o.Fill,
		DrawLine: !o.Columns,
		Thick:    o.Thick,
	})

	labelColor := th.Ramp(latestOf(values), o.Warn, o.Crit)
	labelStyle := lipgloss.NewStyle().Foreground(labelColor).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(labelColor).Bold(true)

	rows := grid.Rows()
	out := make([]string, len(rows))
	for y, row := range rows {
		out[y] = paintRow(grid, y, row, o, th, labelColor)
	}

	// Overlay the label and value on the first row, overwriting graph dots the
	// way btop does rather than reserving a line for them.
	if o.Label != "" || o.Value != "" {
		out[0] = overlayText(out[0], o.Label, o.Value, labelStyle, valueStyle)
	}
	return out
}

// paintRow colours one rendered row of braille.
//
// The colour of a dot is the colour of the reading behind it, ramped from pale
// green through yellow to red by how close that reading is to the danger zone.
// Colouring cell by cell rather than row by row is what lets a graph show
// *where* it got hot: on a rising line the hot end is red while the cool end is
// still green, which is what btop's graphs do.
func paintRow(grid *Grid, y int, row string, o GraphOptions, th Theme, lineColor lipgloss.AdaptiveColor) string {
	w := grid.Width()
	runes := []rune(row)

	var b strings.Builder
	for x := 0; x < w && x < len(runes); x++ {
		bits := grid.cells[y*w+x]
		if bits == 0 {
			b.WriteRune('\u2800') // a blank braille cell, not a space, so the
			continue              // graph keeps a consistent glyph pitch
		}
		color := lineColor
		if v := columnValue(grid, x, y, o, len(runes)); !math.IsNaN(v) {
			color = th.Ramp(v, o.Warn, o.Crit)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(runes[x])))
	}
	return b.String()
}

// columnValue approximates the reading behind one column of the plot, so each
// dot can be coloured by the value at its own x rather than by the value at the
// end of the series.
//
// It walks down from the top of the column and returns at the first dot it
// finds, which for a line plot is the curve. The search covers the whole column
// height rather than one row: the curve at this x may be in any of the four dot
// rows of this cell, or in a cell above, and a dot that cannot find its own value
// falls back to the series' latest colour, which is how a rising line ends up
// uniformly the colour of its right hand end.
func columnValue(grid *Grid, x, y int, o GraphOptions, rowLen int) float64 {
	// Both of the cell's dot columns, left to right.
	for cx := x * brailleCols; cx < (x+1)*brailleCols && cx < grid.dotW; cx++ {
		for dotY := 0; dotY < grid.dotH; dotY++ {
			if grid.dotsSet(cx, dotY) {
				t := 1 - float64(dotY)/float64(max(1, grid.dotH-1))
				return o.Min + t*(o.Max-o.Min)
			}
		}
	}
	return math.NaN()
}

// dotsSet reports whether the dot at (x, dotY) is lit.
func (g *Grid) dotsSet(dotX, dotY int) bool {
	if dotX < 0 || dotY < 0 || dotX >= g.dotW || dotY >= g.dotH {
		return false
	}
	cx, cy := dotX/brailleCols, dotY/brailleRows
	return g.cells[cy*g.Width()+cx]&dotBit(dotX%brailleCols, dotY%brailleRows) != 0
}

// latestOf returns the last valid sample, or NaN.
func latestOf(values []float64) float64 {
	for i := len(values) - 1; i >= 0; i-- {
		if !math.IsNaN(values[i]) {
			return values[i]
		}
	}
	return math.NaN()
}

// overlayText writes the label and value over the first row of a graph.
func overlayText(row, label, value string, labelStyle, valueStyle lipgloss.Style) string {
	w := lipgloss.Width(row)
	// Label on the left, value on the right, as btop does.
	left := " " + label
	right := value + " "
	room := w - 2
	if room <= 0 {
		return row
	}
	if lw := lipgloss.Width(left); lw > room {
		left = truncate(left, room)
	}
	if vw := lipgloss.Width(right); vw > room-lipgloss.Width(left) {
		right = truncate(right, max(0, room-lipgloss.Width(left)))
	}

	gap := room - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	out := left + strings.Repeat(" ", gap) + right

	// Pad to the full width so the row stays aligned with the graph below it.
	if pad := w - lipgloss.Width(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return labelStyle.Render(left) + strings.Repeat(" ", gap) + valueStyle.Render(right)
}
