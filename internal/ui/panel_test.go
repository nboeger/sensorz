package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The box has to close. A panel drawn as a top rule, a body of verticals and a
// bottom rule has a visible break at each corner, because the vertical run
// never reaches the horizontal one - so the corners carry the vertical rule and
// every line of the box starts and ends with it.
func TestPanelCornersClose(t *testing.T) {
	th := DefaultTheme()
	p := NewPanel("CPU", 30, 6, th, th.PanelCPU)
	rendered := p.Render([]string{"one", "two", "three"})

	lines := strings.Split(rendered, "\n")
	if len(lines) != 6 {
		t.Fatalf("panel is %d lines, want 6", len(lines))
	}
	for i, line := range lines {
		if lipgloss.Width(line) != 30 {
			t.Errorf("line %d is %d cells, want 30", i, lipgloss.Width(line))
		}
		plain := stripANSI(line)
		if i == 0 || i == len(lines)-1 {
			// The horizontal edges: a vertical at each end, rules between.
			if !strings.HasPrefix(plain, string(vRule)) || !strings.HasSuffix(plain, string(vRule)) {
				t.Errorf("edge %d does not close at both ends: %q", i, plain)
			}
			// The title is the only thing on an edge that is not a rule.
			if rest := strings.Trim(plain, string(hRule)+string(vRule)); strings.TrimSpace(rest) != "" &&
				strings.TrimSpace(rest) != "CPU" {
				t.Errorf("edge %d is not made of rules and its title: %q", i, plain)
			}
			continue
		}
		if !strings.HasPrefix(plain, string(vRule)) || !strings.HasSuffix(plain, string(vRule)) {
			t.Errorf("body line %d does not start and end with a vertical: %q", i, plain)
		}
	}
}

// The title has to fit inside the top rule with rule either side of it, or it
// eats the corner the box needs.
func TestPanelTitleFitsTheTopRule(t *testing.T) {
	th := DefaultTheme()
	for _, title := range []string{"CPU", "GPU", "Fans", "Drives", "Board", "A very long panel title"} {
		p := NewPanel(title, 24, 4, th, th.PanelCPU)
		plain := stripANSI(strings.Split(p.Render([]string{"x"}), "\n")[0])
		if !strings.Contains(plain, title) && title != "A very long panel title" {
			t.Errorf("title %q is missing from the top rule: %q", title, plain)
		}
		if !strings.HasPrefix(plain, string(vRule)) || !strings.HasSuffix(plain, string(vRule)) {
			t.Errorf("title %q ate a corner: %q", title, plain)
		}
	}
}

// A panel too narrow for its title still has to close.
func TestPanelTooNarrowForTitle(t *testing.T) {
	th := DefaultTheme()
	p := NewPanel("CPU", 6, 4, th, th.PanelCPU)
	for i, line := range strings.Split(p.Render([]string{"x"}), "\n") {
		plain := stripANSI(line)
		if !strings.HasPrefix(plain, string(vRule)) || !strings.HasSuffix(plain, string(vRule)) {
			t.Errorf("line %d does not close: %q", i, plain)
		}
	}
}
