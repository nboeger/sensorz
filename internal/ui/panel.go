package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Panel is a bordered box with a title, the btop layout style.
//
// Each box carries its own colour, the way btop's do: green for the compute
// boxes, blue and purple for the rest. That is what makes a dense dashboard
// legible, because the eye finds a box by its hue before it reads the title.
type Panel struct {
	Title  string
	Width  int
	Height int
	Focus  bool
	// Color is the box's border and title colour. A zero value falls back to
	// the theme's default border, dimmed unless focused.
	Color lipgloss.AdaptiveColor
	theme Theme
}

// NewPanel builds a panel. Width and Height are the outer dimensions including
// the border.
func NewPanel(title string, w, h int, th Theme, color lipgloss.AdaptiveColor) *Panel {
	return &Panel{Title: title, Width: max(w, 2), Height: max(h, 2), Color: color, theme: th}
}

// borderColor returns the colour this box is drawn in, brightened when focused
// so the cursor is visible without a second visual cue.
func (p *Panel) borderColor() lipgloss.AdaptiveColor {
	c := p.Color
	if c == (lipgloss.AdaptiveColor{}) {
		c = p.theme.Border
	}
	if p.Focus {
		return p.theme.BorderFocus
	}
	return c
}

// Inner returns the drawable area inside the border.
func (p *Panel) Inner() (w, h int) {
	return max(0, p.Width-2), max(0, p.Height-2)
}

// Render draws the panel around the given content.
//
// The content is padded rather than truncated when it is too short, so a panel
// keeps its declared size and the grid of panels stays aligned; content that
// is too tall is cut, because a graph that runs off the bottom of its panel
// would corrupt the layout of everything below it.
func (p *Panel) Render(content []string) string {
	innerW, innerH := p.Inner()

	border := p.borderColor()
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Width(innerW).
		Height(innerH)

	body := make([]string, 0, innerH)
	for i := 0; i < innerH; i++ {
		if i < len(content) {
			body = append(body, padLine(content[i], innerW))
		} else {
			body = append(body, strings.Repeat(" ", innerW))
		}
	}

	rendered := box.Render(strings.Join(body, "\n"))
	if p.Title == "" {
		return rendered
	}
	return p.injectTitle(rendered)
}

// injectTitle overwrites the middle of the top border with the panel title,
// which is what btop and every other panel-based TUI does.
//
// The border is rebuilt from plain runes and coloured in one pass at the end.
// Splicing styled text into a styled line would require counting display cells
// around escape sequences, which is exactly the kind of off-by-one that shows
// up as a stray escape code eating a border character.
func (p *Panel) injectTitle(rendered string) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}

	top := []rune(lines[0])
	if len(top) < 3 {
		return rendered
	}
	// The top border is "╭" followed by innerW dashes followed by "╮".
	dashes := len(top) - 2

	title := truncate(p.Title, dashes-2)
	if title == "" {
		return rendered
	}

	start := 1 + (dashes-runewidth.StringWidth(title)-2)/2
	if start < 1 {
		start = 1
	}
	if start+runewidth.StringWidth(title)+2 > len(top)-1 {
		start = len(top) - 2 - runewidth.StringWidth(title)
		if start < 1 {
			start = 1
		}
	}

	var b strings.Builder
	b.WriteRune(top[0])
	b.WriteString(strings.Repeat(string(top[1]), start-1))
	b.WriteString(" " + title + " ")
	b.WriteString(strings.Repeat(string(top[1]), len(top)-1-(start+runewidth.StringWidth(title)+2)))
	b.WriteRune(top[len(top)-1])

	color := p.borderColor()
	lines[0] = p.theme.Style(color).Render(b.String())
	return strings.Join(lines, "\n")
}

// padLine pads a line to exactly w display cells, cutting it if it is longer.
// Padding uses spaces rather than lipgloss so that lines already carrying ANSI
// colour sequences are measured by their printable width, not their byte or
// rune count.
func padLine(s string, w int) string {
	width := lipgloss.Width(s)
	if width == w {
		return s
	}
	if width > w {
		return truncateStyled(s, w)
	}
	return s + strings.Repeat(" ", w-width)
}

// truncate cuts a string to a display width, appending an ellipsis when it had
// to cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return runewidth.Truncate(s, w-1, "") + "…"
}

// truncateStyled cuts an ANSI-styled string to w display cells, preserving the
// escape sequences so colours are not left unterminated.
func truncateStyled(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	var (
		b      strings.Builder
		col    int
		inEsc  bool
		escBuf strings.Builder
	)
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			escBuf.Reset()
			escBuf.WriteRune(r)
			continue
		}
		if inEsc {
			escBuf.WriteRune(r)
			if r == 'm' || r == 'K' {
				inEsc = false
				b.WriteString(escBuf.String())
			}
			continue
		}
		rw := runewidth.RuneWidth(r)
		if col+rw > w-1 {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		col += rw
	}
	return b.String()
}

// Column lays panels out side by side, which is how btop arranges CPU, GPU and
// memory at the top of the screen.
func Column(panels ...string) string {
	if len(panels) == 0 {
		return ""
	}
	split := strings.Split(panels[0], "\n")
	for _, p := range panels[1:] {
		for i, line := range strings.Split(p, "\n") {
			if i < len(split) {
				split[i] = lipgloss.JoinHorizontal(lipgloss.Top, split[i], line)
			}
		}
	}
	return strings.Join(split, "\n")
}

// Rows stacks panels vertically.
func Rows(panels ...string) string {
	return strings.Join(panels, "\n")
}
