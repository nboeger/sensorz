package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nathan/sensorz/internal/collect"
	"github.com/nathan/sensorz/internal/history"
	"github.com/nathan/sensorz/internal/model"
)

// renderLive collects a few samples and returns the rendered view. It is the
// layout regression test: if a panel overflows or a graph renders as noise, it
// shows up here.
func renderLive(t *testing.T, w, h int, samples int) string {
	t.Helper()
	return buildLive(t, w, h, samples).View()
}

func TestViewFitsTerminal(t *testing.T) {
	view := renderLive(t, 120, 40, 6)
	lines := strings.Split(view, "\n")
	t.Logf("\n%s", view)

	if len(lines) > 40 {
		t.Errorf("view is %d lines, terminal is 40: layout overflows", len(lines))
	}
	for i, line := range lines {
		if w := lineWidth(line); w > 120 {
			t.Errorf("line %d is %d cells wide, terminal is 120:\n%q", i, w, line)
		}
	}
}

func TestViewSmallTerminal(t *testing.T) {
	view := renderLive(t, 60, 20, 3)
	t.Logf("\n%s", view)
	if !strings.Contains(view, "sensorz") {
		t.Errorf("small terminal view missing header:\n%s", view)
	}
}

func TestKeyHandling(t *testing.T) {
	m := buildLive(t, 120, 40, 1)

	if focus := m.focus; focus != FocusCPU {
		t.Fatalf("initial focus = %v", focus)
	}

	// The cursor only visits panels the machine actually has, so the cycle is
	// checked against the panels this machine reported.
	visible := m.visibleFocus()
	if len(visible) < 2 {
		t.Skipf("only one panel visible (%v), nothing to cycle", visible)
	}
	for i, k := range []string{"tab", "tab", "tab"} {
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		m = mm.(*Model)
		want := visible[(1+i)%len(visible)]
		if m.focus != want {
			t.Fatalf("focus after %d tabs = %v, want %v (visible %v)", i+1, m.focus, want, visible)
		}
	}
	// Stepping backwards and forwards again returns to where we were, one
	// panel at a time.
	at := 3 % len(visible)
	if m.focus != visible[at] {
		t.Fatalf("focus = %v, want %v", m.focus, visible[at])
	}
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = mm.(*Model)
	if m.focus != visible[(at-1+len(visible))%len(visible)] {
		t.Errorf("focus after up = %v, want %v", m.focus, visible[(at-1+len(visible))%len(visible)])
	}
	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mm.(*Model)
	if m.focus != visible[at] {
		t.Errorf("focus after down = %v, want %v", m.focus, visible[at])
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if m2 := mm.(*Model); !m2.showAllSensors {
		t.Error("'a' did not toggle all sensors")
	}

	mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if m2 := mm.(*Model); !m2.paused {
		t.Error("'p' did not pause")
	}
}

func lineWidth(s string) int { return lipglossWidth(s) }

func lipglossWidth(s string) int { return lipgloss.Width(s) }

// buildLive is the shared constructor behind the render tests.
func buildLive(t *testing.T, w, h, samples int) *Model {
	t.Helper()
	agg, err := collect.New(collect.Options{SensorFilter: DefaultFilter()})
	if err != nil {
		t.Skipf("collectors unavailable: %v", err)
	}
	t.Cleanup(agg.Close)

	cfg := DefaultConfig()
	cfg.GPUBackend = agg.GPUBackend
	m := New(agg, history.NewStore(cfg.HistoryCapacity), cfg, DefaultTheme())
	m.width, m.height = w, h

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go agg.Run(ctx, func(s model.Snapshot) {})

	for i := 0; i < samples; i++ {
		m.applySnapshot(agg.Once(ctx))
		time.Sleep(120 * time.Millisecond)
	}
	return m
}

// TestHeightBudget checks the vertical arithmetic: the rendered view must fit
// the terminal exactly, with no rows spilling off the bottom.
func TestHeightBudget(t *testing.T) {
	m := buildLive(t, 120, 40, 6)
	lines := strings.Split(m.View(), "\n")
	t.Logf("terminal 40 rows, view has %d lines", len(lines))
	if len(lines) != 40 {
		for i, l := range lines {
			head := l
			if r := []rune(l); len(r) > 24 {
				head = string(r[:24])
			}
			t.Logf("%2d| %s", i+1, head)
		}
		t.Errorf("view has %d lines for a 40 row terminal", len(lines))
	}
}

// A reading with its unit is a reading a reader can identify. These rows are
// labelled by the kernel's own channel names - SYSTIN, Sensor 2, nvme0n1 -
// which say nothing about the quantity, so the number has to say it is a
// temperature itself.
func TestTemperaturesCarryTheirUnit(t *testing.T) {
	mt := model.Metric{Kind: model.KindTemperature, Value: 42}
	if got := readout(mt, true); got != "42°C" {
		t.Errorf("temperature readout = %q, want 42°C", got)
	}
	if got := readout(mt, false); got != "42" {
		t.Errorf("compact readout = %q, want the bare number", got)
	}

	fan := model.Metric{Kind: model.KindFan, Value: 1950}
	if got := readout(fan, true); !strings.Contains(got, "RPM") {
		t.Errorf("fan readout = %q, want it to carry RPM", got)
	}
}

// The thresholds under a figure are temperatures too, and a lone degree sign
// is not a unit.
func TestFooterThresholdsCarryTheirUnit(t *testing.T) {
	m := buildLive(t, 120, 40, 1)
	mt, ok := m.Metric(collect.MetricCPUTemp)
	if !ok {
		t.Skip("no CPU temperature on this machine")
	}
	footer := stripANSI(m.footer(80, mt))
	if !strings.Contains(footer, "°C") {
		t.Errorf("footer %q does not carry a unit", footer)
	}
}
