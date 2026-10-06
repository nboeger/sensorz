package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The ramp is the whole point of the colour scheme: a reading has to move from
// pale green to red as it closes in on the danger zone, not jump between three
// flat bands.
func TestRampGoesGreenToRed(t *testing.T) {
	th := DefaultTheme()
	const warn, crit = 70.0, 85.0

	cases := []struct {
		value float64
		want  lipgloss.AdaptiveColor
	}{
		{20, th.Good},       // cold: untouched green
		{69, th.Good},       // still just under the warning point
		{crit, th.Bad},      // at the limit: fully red
		{crit + 10, th.Bad}, // past it: still red, no further to go
		{0, th.Good},        // nothing is red at zero
	}
	for _, c := range cases {
		if got := th.Ramp(c.value, warn, crit); got != c.want {
			t.Errorf("Ramp(%v) = %v, want %v", c.value, got, c.want)
		}
	}

	// Inside the warning band the colour has to move continuously: a quarter
	// of the way in is already a green leaning to yellow, three quarters is a
	// red leaning to yellow, and neither is a flat band.
	early := th.Ramp(warn+(crit-warn)*0.25, warn, crit)
	late := th.Ramp(warn+(crit-warn)*0.75, warn, crit)
	if early == th.Good || early == th.Warn {
		t.Errorf("Ramp a quarter of the way in = %v, want a blend", early)
	}
	if late == th.Warn || late == th.Bad {
		t.Errorf("Ramp three quarters of the way in = %v, want a blend", late)
	}
	if early == late {
		t.Errorf("Ramp gave the same colour at 25%% and 75%% of the band: %v", early)
	}

	// A metric with no thresholds never turns red: a fast fan is not a fault.
	if got := th.Ramp(5000, 0, 0); got != th.Good {
		t.Errorf("Ramp with no thresholds = %v, want Good", got)
	}
}

// The meter has to read as btop's does: solid where the value is, a dotted
// track where there is room left.
func TestMeterDrawsATrack(t *testing.T) {
	th := DefaultTheme()
	m := Meter(10, 0.5, th, th.Good)
	if !strings.Contains(m, string(meterFill)) {
		t.Errorf("meter %q has no filled portion", stripANSI(m))
	}
	if !strings.Contains(m, string(meterTrack)) {
		t.Errorf("meter %q has no dotted track for the remainder", stripANSI(m))
	}
	if got := lipgloss.Width(m); got != 10 {
		t.Errorf("meter is %d cells wide, want 10", got)
	}
	if stripANSI(Meter(10, 0, th, th.Good)) != strings.Repeat(string(meterTrack), 10) {
		t.Error("an empty meter should be all track")
	}
	if stripANSI(Meter(10, 1, th, th.Good)) != strings.Repeat(string(meterFill), 10) {
		t.Error("a full meter should be all fill")
	}
	// A zero width meter must render nothing rather than panic.
	if Meter(0, 0.5, th, th.Good) != "" {
		t.Error("a zero width meter should be empty")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// A temperature graph has to show the ramp in the plot itself: the cool end
// pale green, the hot end red. Without this the graph would be a shape with no
// temperature in it.
func TestGraphDotsCarryTheRamp(t *testing.T) {
	// Tests run without a terminal, where lipgloss strips colour entirely, so
	// the profile has to be forced for there to be anything to assert on.
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	th := DefaultTheme()
	values := []float64{55, 60, 65, 70, 75, 80, 85, 90}
	rows := RenderGraph(values, GraphOptions{
		Width: 60, Height: 8, Min: 20, Max: 95, Warn: 70, Crit: 85,
		Label: "CPU", Value: "90",
	}, th)

	colors := map[string]bool{}
	for _, row := range rows {
		for _, code := range colorCodes(row) {
			colors[code] = true
		}
	}
	if len(colors) < 3 {
		t.Errorf("graph used %d colours, want a ramp from green to red: %v", len(colors), colors)
	}
	t.Logf("graph drawn with %d colours: %v", len(colors), colors)
}

// colorCodes returns the 24-bit foreground colours used in a styled string.
func colorCodes(s string) []string {
	var (
		out   []string
		inEsc bool
		esc   strings.Builder
	)
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			esc.Reset()
			esc.WriteRune(r)
			continue
		}
		if inEsc {
			esc.WriteRune(r)
			if r == 'm' {
				inEsc = false
				if code := strings.TrimSuffix(strings.TrimPrefix(esc.String(), "\x1b[38;2;"), "m"); code != esc.String() {
					out = append(out, code)
				}
			}
		}
	}
	return out
}

// Every box wears the same hue. Two colours across the chrome reads as two
// kinds of thing on screen, and the only colour that should mean anything is
// the one a reading is drawn in.
func TestAllPanelsShareOneHue(t *testing.T) {
	th := DefaultTheme()
	panels := map[string]lipgloss.AdaptiveColor{
		"CPU":    th.PanelCPU,
		"GPU":    th.PanelGPU,
		"Fans":   th.PanelFans,
		"Drives": th.PanelDrives,
		"Board":  th.PanelBoard,
	}
	for name, c := range panels {
		if c != th.PanelFans {
			t.Errorf("the %s panel is drawn in %v, want the same hue as the fan panel (%v)", name, c, th.PanelFans)
		}
	}
}
