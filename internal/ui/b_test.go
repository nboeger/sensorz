package ui

import (
	"math"
	"math/rand"
	"testing"
)

func TestGridShape(t *testing.T) {
	g := NewGrid(20, 5)
	dw, dh := g.Dots()
	if dw != 40 || dh != 20 {
		t.Fatalf("dots=%dx%d want 40x20", dw, dh)
	}
	if g.Width() != 20 || g.Height() != 5 {
		t.Fatalf("cells=%dx%d", g.Width(), g.Height())
	}
	if len(g.Rows()) != 5 {
		t.Fatalf("rows=%d", len(g.Rows()))
	}
}

func TestPlotRenders(t *testing.T) {
	vals := make([]float64, 100)
	r := rand.New(rand.NewSource(1))
	for i := range vals {
		vals[i] = 40 + r.Float64()*40
	}
	g := NewGrid(30, 6)
	Plot(g, vals, 20, 100, PlotStyle{Fill: true, DrawLine: true})
	lit := 0
	for _, c := range g.cells {
		if c != 0 {
			lit++
		}
	}
	t.Logf("\n%s", g.String())
	if lit < 10 {
		t.Fatalf("only %d cells lit", lit)
	}
}

func TestPlotGapsAreNotConnected(t *testing.T) {
	vals := []float64{10, 10, math.NaN(), math.NaN(), 90, 90}
	g := NewGrid(10, 3)
	Plot(g, vals, 0, 100, PlotStyle{DrawLine: true})
	// The two NaN columns must stay blank and must not be bridged.
	x := g.Width()
	blank := 0
	for i := 4; i < 8; i++ {
		if g.cells[2*x+i] == 0 && g.cells[1*x+i] == 0 {
			blank++
		}
	}
	t.Logf("\n%s", g.String())
	if blank < 4 {
		t.Fatalf("gap not preserved: blank=%d/4", blank)
	}
}

func TestPlotEmptyAndZeroSize(t *testing.T) {
	Plot(NewGrid(10, 3), nil, 0, 100, PlotStyle{}) // must not panic
	Plot(NewGrid(0, 0), []float64{1}, 0, 100, PlotStyle{})
	Plot(NewGrid(5, 3), []float64{5}, 5, 5, PlotStyle{}) // degenerate range
}

func TestResamplePreservesPeak(t *testing.T) {
	vals := make([]float64, 100)
	for i := range vals {
		vals[i] = 10
	}
	vals[50] = 99
	out := resample(vals, 20)
	peak := 0.0
	for _, v := range out {
		if v > peak {
			peak = v
		}
	}
	if peak != 99 {
		t.Fatalf("peak lost during downsample: %v", peak)
	}
}
