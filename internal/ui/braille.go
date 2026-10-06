// Package ui renders the dashboard. The graph renderer here is the piece that
// gives sensorz its btop-like look: each terminal cell holds a 2x4 grid of
// braille dots, so a plot is twice as wide and four times as tall as it looks,
// which is where btop's extra resolution comes from.
package ui

import (
	"math"
	"strings"
)

// Braille base patterns. U+2800 is a blank cell; each bit turns on one dot.
// The dot layout within a cell is:
//
//	(0,0)=0x01  (0,1)=0x02
//	(1,0)=0x04  (1,1)=0x08
//	(2,0)=0x10  (2,1)=0x20
//	(3,0)=0x40  (3,1)=0x80
//
// with (row, col) where row 0 is the top of the cell.
const (
	brailleBase = 0x2800
	brailleRows = 4 // dots per cell vertically
	brailleCols = 2 // dots per cell horizontally
)

// brailleRunes maps a byte of dot bits to its codepoint: U+2800 plus the
// bit pattern. Building it once avoids a 256 entry literal in the source.
var brailleRunes = func() [256]rune {
	var t [256]rune
	for i := range t {
		t[i] = rune(brailleBase + i)
	}
	return t
}()

// dotBit returns the braille bit for the dot at (col, row) inside a cell.
// col is 0..1 left to right, row is 0..3 top to bottom.
func dotBit(col, row int) byte {
	return 1 << (row*brailleCols + col)
}

// Grid is a braille dot canvas: dotW by dotH dots, addressed with row 0 at the
// top. It is the backing store both the line plot and the meter fill draw into.
type Grid struct {
	w, h   int    // size in terminal cells
	dotW   int    // w * brailleCols
	dotH   int    // h * brailleRows
	cells  []byte // one byte of dot bits per terminal cell
	shades []byte // per-cell fill level, 0..brailleRows
}

// NewGrid allocates a grid of w columns by h terminal cells.
func NewGrid(w, h int) *Grid {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Grid{
		w:      w,
		h:      h,
		dotW:   w * brailleCols,
		dotH:   h * brailleRows,
		cells:  make([]byte, w*h),
		shades: make([]byte, w*h),
	}
}

// Width returns the width in terminal cells.
func (g *Grid) Width() int { return g.w }

// Height returns the height in terminal cells.
func (g *Grid) Height() int { return g.h }

// Dots returns the plot resolution in braille dots.
func (g *Grid) Dots() (w, h int) { return g.dotW, g.dotH }

// Cell returns the raw braille codepoint for a cell.
func (g *Grid) Cell(x, y int) rune {
	if x < 0 || y < 0 || y*g.Width()+x >= len(g.cells) {
		return 0
	}
	return brailleRunes[g.cells[y*g.Width()+x]]
}

// Set lights a dot.
func (g *Grid) Set(dotX, dotY int) {
	if dotX < 0 || dotY < 0 || dotX >= g.dotW || dotY >= g.dotH {
		return
	}
	cx, cy := dotX/brailleCols, dotY/brailleRows
	g.cells[cy*g.Width()+cx] |= dotBit(dotX%brailleCols, dotY%brailleRows)
}

// Shade records a fill level for a cell, used to pick the gradient colour.
func (g *Grid) Shade(x, y, level int) {
	if x < 0 || y < 0 || y*g.Width()+x >= len(g.cells) {
		return
	}
	if level < 0 {
		level = 0
	}
	if level > brailleRows {
		level = brailleRows
	}
	idx := y*g.Width() + x
	if uint8(level) > g.shades[idx] {
		g.shades[idx] = uint8(level)
	}
}

// ShadeAt returns the recorded fill level for a cell.
func (g *Grid) ShadeAt(x, y int) int {
	if x < 0 || y < 0 || y*g.Width()+x >= len(g.cells) {
		return 0
	}
	return int(g.shades[y*g.Width()+x])
}

// PlotStyle controls how a series is drawn into a grid.
type PlotStyle struct {
	// Fill draws a shaded area under the curve.
	Fill bool
	// DrawLine connects consecutive points with dots.
	DrawLine bool
}

// Plot draws a series into the grid.
//
// values are read oldest-first and squeezed to fill the grid width, so a
// partially filled history lines up on the right and grows leftwards, which is
// what btop does. min and max define the vertical scale; values outside it are
// clamped, and NaN samples (a sensor that went away) leave the column blank so
// the graph shows a genuine gap rather than a false straight line.
//
// NaN gaps are also not joined: the line breaks rather than interpolating
// across missing data, because a straight segment through a gap reads as data
// that was never measured.
func Plot(g *Grid, values []float64, min, max float64, style PlotStyle) {
	if g == nil || g.dotW == 0 || g.dotH == 0 || len(values) == 0 {
		return
	}
	if max <= min {
		max = min + 1
	}

	n := g.dotW
	cols := resample(values, n)

	prevX, prevY := -1, -1
	for x, v := range cols {
		if math.IsNaN(v) {
			prevX, prevY = -1, -1
			continue
		}
		y := g.scaleY(v, min, max)

		if style.DrawLine && prevX >= 0 {
			drawSegment(g, prevX, prevY, x, y)
		}
		g.Set(x, y)

		if style.Fill {
			fillColumn(g, x, y)
		}
		prevX, prevY = x, y
	}
}

// scaleY maps a value onto a dot row, with row 0 at the top.
func (g *Grid) scaleY(v, min, max float64) int {
	t := (v - min) / (max - min)
	t = math.Max(0, math.Min(1, t))
	return int(math.Round((1 - t) * float64(g.dotH-1)))
}

// fillColumn shades the area between the curve and the bottom of the grid.
//
// For each cell it records how many of the cell's four braille rows are filled
// (0..4) and lights the dots in those rows. The caller colours the dots using
// the recorded level to dim them toward the bottom of the panel, which is what
// gives the graph its depth, the same way btop renders its filled plots.
func fillColumn(g *Grid, x, y int) {
	if x < 0 || x >= g.dotW {
		return
	}
	cx := x / brailleCols
	for dotY := y; dotY < g.dotH; dotY++ {
		cy := dotY / brailleRows
		// A cell is filled from row (dotY % brailleRows) down, so it has
		// brailleRows - (dotY % brailleRows) filled rows.
		g.Shade(cx, cy, brailleRows-(dotY%brailleRows))
		g.Set(x, dotY)
	}
}

// drawSegment lights every dot along the line between two points using
// Bresenham, so steep slopes stay connected instead of leaving gaps.
func drawSegment(g *Grid, x0, y0, x1, y1 int) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		g.Set(x0, y0)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// resample squeezes or stretches values to exactly n columns, oldest-first, by
// nearest neighbour. Nearest neighbour rather than averaging because a
// temperature spike must stay visible instead of being averaged away.
func resample(values []float64, n int) []float64 {
	out := make([]float64, n)
	if len(values) == 0 {
		return out
	}
	if len(values) == n {
		copy(out, values)
		return out
	}
	if len(values) > n {
		// Downsample: keep the extreme value in each bucket rather than one
		// arbitrary sample, so a short spike survives downsampling.
		for i := 0; i < n; i++ {
			lo := i * len(values) / n
			hi := (i + 1) * len(values) / n
			if hi <= lo {
				hi = lo + 1
			}
			out[i] = extreme(values[lo:min(hi, len(values))])
		}
		return out
	}
	// Upsample: repeat each sample across its share of the columns.
	for i := 0; i < n; i++ {
		idx := i * len(values) / n
		out[i] = values[min(idx, len(values)-1)]
	}
	return out
}

// extreme returns the value furthest from the series mean, which for a
// temperature or utilization plot is the interesting one: the peak.
func extreme(vals []float64) float64 {
	var sum float64
	var n int
	for _, v := range vals {
		if !math.IsNaN(v) {
			sum += v
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	mean := sum / float64(n)
	best, bestDist := math.NaN(), -1.0
	for _, v := range vals {
		if math.IsNaN(v) {
			continue
		}
		if d := math.Abs(v - mean); d > bestDist {
			best, bestDist = v, d
		}
	}
	return best
}

// String renders the grid as rows of braille characters.
func (g *Grid) String() string {
	w, h := g.Width(), g.Height()
	var b strings.Builder
	b.Grow(h * (w*3 + 1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			b.WriteRune(g.Cell(x, y))
		}
		if y < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Rows returns the grid as a slice of strings, one per cell row.
func (g *Grid) Rows() []string {
	w, h := g.Width(), g.Height()
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		b.Grow(w * 3)
		for x := 0; x < w; x++ {
			b.WriteRune(g.Cell(x, y))
		}
		rows[y] = b.String()
	}
	return rows
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
