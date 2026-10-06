package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// panelBorder draws a panel the way btop does: solid continuous rules in the
// horizontal and a single line down each side, with no corner glyphs at all.
//
// The rules are drawn with the box-drawing line characters rather than ASCII
// dashes. A row of '-' is not a line, it is a row of dashes with a gap at every
// cell boundary, which is what makes ASCII borders look broken next to the
// solid rules btop draws. The corners are left out deliberately: they are the
// part of the block a font is most likely to be missing, and a box with no
// corners is exactly as readable as one with them.
var panelBorder = lipgloss.Border{
	Top:         "\u2500", // ─
	Bottom:      "\u2500",
	Left:        "\u2502", // │
	Right:       "\u2502",
	TopLeft:     "",
	TopRight:    "",
	BottomLeft:  "",
	BottomRight: "",
}

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

	// The panels have no corner glyphs, so the top line is nothing but the
	// horizontal rule, and the title is written into the middle of it. The
	// width comes from the body, which is the line that has to line up: lipgloss
	// renders an empty corner as a space, so the rule it produced is a cell
	// short and cannot be trusted as the measure.
	total := lipgloss.Width(lines[0])
	if len(lines) > 1 {
		total = lipgloss.Width(lines[1])
	}
	if total < 5 {
		return rendered
	}
	dashes := total
	const fill = '\u2500' // the same solid rule lipgloss drew the sides with

	title := truncate(p.Title, dashes-2)
	if title == "" {
		return rendered
	}
	tw := runewidth.StringWidth(title)

	// Centre the title, leaving at least one dash on either side.
	start := 1 + (dashes-tw-2)/2
	if start < 1 {
		start = 1
	}
	if start+tw+2 > dashes {
		start = dashes - tw - 2
	}
	if start < 1 {
		start = 1
	}

	var b strings.Builder
	b.WriteString(strings.Repeat(string(fill), start-1))
	b.WriteString(" " + title + " ")
	b.WriteString(strings.Repeat(string(fill), dashes-(start-1)-(tw+2)))

	color := p.borderColor()
	lines[0] = p.theme.Style(color).Render(b.String())

	// The bottom rule is written out in full as well: lipgloss renders an empty
	// corner as a space, which would leave the box closed with a blank at each
	// end instead of a line.
	if len(lines) > 1 {
		lines[len(lines)-1] = p.theme.Style(color).Render(strings.Repeat(string(fill), dashes))
	}
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
