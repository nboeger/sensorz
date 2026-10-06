package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// The glyphs a panel is drawn from: one horizontal rule, one vertical, and the
// four corners that join them.
//
// The corners are the whole trick behind a box that looks sharp. A horizontal
// rule and a vertical rule meet only in a glyph that is drawn to do exactly
// that - the corner sits in one cell with its horizontal arm running right and
// its vertical arm running down, so there is no join to see and nothing to
// break. Faking a corner with a vertical rule in the corner cell cannot look
// sharp: the rule sits in the middle of its cell and the horizontal one sits in
// the middle of its own, so the two arms meet at an angle with a gap on either
// side of the join.
const (
	hRule      = '\u2500' // ─
	vRule      = '\u2502' // │
	cornerTopL = '\u250c' // ┌
	cornerTopR = '\u2510' // ┐
	cornerBotL = '\u2514' // └
	cornerBotR = '\u2518' // ┘
)

// panelBorder draws a panel the way btop does: a horizontal rule across the top
// and bottom, a vertical rule down each side, and square corners joining them.
var panelBorder = lipgloss.Border{
	Top:         string(hRule),
	Bottom:      string(hRule),
	Left:        string(vRule),
	Right:       string(vRule),
	TopLeft:     string(cornerTopL),
	TopRight:    string(cornerTopR),
	BottomLeft:  string(cornerBotL),
	BottomRight: string(cornerBotR),
}

// Panel is a bordered box with a title, the btop layout style.
//
// Each box carries its own colour, the way btop's do: green for the compute
// boxes and purple for the rest. That is what makes a dense dashboard legible,
// because the eye finds a box by its hue before it reads the title.
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
		return p.theme.Brighten(c, 0.55)
	}
	return c
}

// Inner returns the drawable area inside the border.
func (p *Panel) Inner() (w, h int) {
	return max(0, p.Width-2), max(0, p.Height-2)
}

// Render draws the panel around the given content.
//
// The borders are plain ASCII lines with no corner glyphs, so a panel reads as
// a line with a name in it and two vertical rules, the way a wireframe looks and
// the way btop's panels read once the terminal draws the corners.
//
// The content is padded rather than truncated when it is too short, so a panel
// keeps its declared size and the grid of panels stays aligned; content that
// is too tall is cut, because a graph that runs off the bottom of its panel
// would corrupt the layout of everything below it.
func (p *Panel) Render(content []string) string {
	innerW, innerH := p.Inner()

	border := p.borderColor()
	box := lipgloss.NewStyle().
		Border(panelBorder).
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

// injectTitle writes the panel title into the top rule and redraws the bottom
// rule to match the body width.
//
// lipgloss pads the top edge to the width of the widest line, which is the body,
// but it measures its own border strings, and a rule drawn by one library and
// a title written by another is a join waiting to be a cell short. Both edges
// are rebuilt here from the body's own width so the box is exactly as wide as
// the content it contains.
func (p *Panel) injectTitle(rendered string) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		return rendered
	}
	total := lipgloss.Width(lines[1])
	if total < 4 {
		return rendered
	}

	style := p.theme.Style(p.borderColor())
	lines[0] = style.Render(p.ruleLine(p.Title, total))
	lines[len(lines)-1] = style.Render(p.ruleLine("", total))
	return strings.Join(lines, "\n")
}

// ruleLine draws one horizontal edge of the panel between its two corners,
// with the title written into the top one.
func (p *Panel) ruleLine(title string, total int) string {
	topLeft, topRight := string(cornerTopL), string(cornerTopR)
	if title == "" {
		topLeft, topRight = string(cornerBotL), string(cornerBotR)
	}
	if total < 4 {
		return topLeft + topRight
	}
	inner := total - 2

	var b strings.Builder
	b.WriteString(topLeft)

	switch title {
	case "":
		b.WriteString(strings.Repeat(string(hRule), inner))
	default:
		t := truncate(title, inner-2)
		tw := runewidth.StringWidth(t)
		// Centre the title, leaving at least one rule character either side.
		left := 1 + (inner-tw-2)/2
		if left < 1 {
			left = 1
		}
		if rest := inner - (left - 1) - (tw + 2); rest >= 1 {
			b.WriteString(strings.Repeat(string(hRule), left-1))
			b.WriteString(" " + t + " ")
			b.WriteString(strings.Repeat(string(hRule), rest))
		} else {
			b.WriteString(strings.Repeat(string(hRule), inner))
		}
	}

	b.WriteString(topRight)
	return b.String()
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
		return truncate(s, w)
	}
	return s + strings.Repeat(" ", w-width)
}

// truncate cuts a string to a display width, appending an ellipsis when it had
// to cut.
//
// It preserves colour. The escape sequences around the cut are copied through,
// so a truncated line never leaves a colour unterminated to bleed into whatever
// is drawn next to it - which is how a whole row of a panel turns the colour of
// the first value on it.
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

	var (
		b      strings.Builder
		col    int
		inEsc  bool
		escBuf strings.Builder
	)
	for _, r := range s {
		if r == 0x1b {
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

// Column lays panels out side by side, which is how btop arranges the CPU and
// GPU boxes at the top of the screen.
//
// Panels are joined line by line, so a spacer passed as a single blank string
// is expanded to the height of the tallest panel here. Without that, the gap
// between two boxes only appears on the first line and the rest of the boxes
// sit flush against each other.
func Column(panels ...string) string {
	if len(panels) == 0 {
		return ""
	}

	split := make([][]string, 0, len(panels))
	rows := 0
	for _, p := range panels {
		lines := strings.Split(p, "\n")
		rows = max(rows, len(lines))
		split = append(split, lines)
	}

	out := make([]string, rows)
	for i := 0; i < rows; i++ {
		row := make([]string, 0, len(split))
		for _, lines := range split {
			if i < len(lines) {
				row = append(row, lines[i])
				continue
			}
			// A panel that is shorter than the tallest one contributes blanks
			// of its own width, so a spacer stays one cell wide.
			w := 0
			if len(lines) > 0 {
				w = lipgloss.Width(lines[0])
			}
			row = append(row, strings.Repeat(" ", w))
		}
		out[i] = lipgloss.JoinHorizontal(lipgloss.Top, row...)
	}
	return strings.Join(out, "\n")
}

// Rows stacks panels vertically.
func Rows(panels ...string) string {
	return strings.Join(panels, "\n")
}
