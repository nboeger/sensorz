package ui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nathan/sensorz/internal/model"
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
	// Unit is appended to the value.
	Unit string
	// Autoscale grows the ceiling to fit the data when the metric has no
	// meaningful fixed maximum, such as fan RPM on a quiet machine.
	Autoscale bool
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
	// Dots renders a lit cell as a dot matrix rather than as the exact set of
	// dots the plot put there.
	//
	// A cell whose eight dots are all lit is a solid block, and a column chart
	// of solid blocks is a rectangle: the shape is there but the texture is
	// gone, and a series that holds looks like a wall rather than a plateau.
	// Intersecting with a dot pattern keeps the shape and gives every filled
	// cell the same texture, so a steady series reads as a textured plateau and
	// a moving one as a stepped edge.
	Dots bool
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
	if o.Autoscale {
		lo, hi = autoscale(values, o.Min, o.Max)
	}
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

// barDots is the pattern a level bar is drawn with: two rows of two dots per
// cell. A dot matrix, not a solid block, so a filled bar reads as a column of
// little dots rather than as a rectangle of colour.
const barDots = 0x33

// levelBar draws a reading as a solid column of dots standing on the baseline.
//
// The height is the reading's share of its limit and the colour is how close
// that share is to the limit: pale green while there is half the scale in hand,
// warming through yellow, red at the critical point. It is a thermometer, not a
// time series, which is the point of drawing it vertically - a bar next to a
// number answers "how hot is it now" without the reader having to find the top
// of a curve and trace it down to a scale that is not labelled.
//
// The unfilled part of the column is left as dim dots rather than blank, so the
// full height of the bar reads as the scale and the filled part as the reading.
func (m *Model) levelBar(w, h int, mt model.Metric) []string {
	if w <= 0 || h <= 0 {
		return nil
	}

	scale, color := mt.Crit, m.th.RampTo(mt.Value, mt.Crit)
	if scale <= 0 {
		// Nothing to climb towards - fan speed has no critical point - so the
		// bar is scaled against the sensor's own ceiling and stays green,
		// because more of a fan is not worse.
		scale, color = mt.Max, m.th.Good
	}
	if scale <= 0 {
		return nil
	}

	level := int(math.Round(clamp01(mt.Value/scale) * float64(h)))
	dots := string(brailleRunes[barDots])

	rows := make([]string, h)
	for y := 0; y < h; y++ {
		style := m.th.Style(m.th.Dim)
		if y >= h-level {
			style = m.th.Style(color)
		}
		rows[y] = style.Render(strings.Repeat(dots, w))
	}
	return rows
}

// clamp01 bounds a fraction to 0..1.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
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
		cell := runes[x]
		if o.Dots {
			cell = brailleRunes[grid.cells[y*w+x]&barDots]
		}
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(cell)))
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

// autoscale picks bounds from the data when the metric has no fixed maximum.
func autoscale(values []float64, lo, hi float64) (float64, float64) {
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if math.IsNaN(v) {
			continue
		}
		minV = math.Min(minV, v)
		maxV = math.Max(maxV, v)
	}
	if math.IsInf(minV, 1) || math.IsInf(maxV, -1) {
		return lo, hi
	}
	// Keep a floor of range so a perfectly flat series does not divide by zero
	// or amplify float noise into a full-height graph.
	if span := maxV - minV; span < 1 {
		minV -= 1
		maxV += 1
	}
	// Round outward to tidy numbers so the axis reads well.
	return math.Floor(minV), math.Ceil(maxV)
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
