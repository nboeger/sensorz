package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/nathan/sensorz/internal/collect"
	"github.com/nathan/sensorz/internal/collect/sensors"
	"github.com/nathan/sensorz/internal/model"
)

// View renders the whole dashboard.
//
// The dashboard is a thermal and airflow monitor: temperatures and fan speeds,
// nothing else. There is deliberately no CPU utilisation, memory or throughput
// panel to look at, because every row on screen is a reading the user can act
// on - run something cooler, open a case, clean a filter.
func (m *Model) View() string {
	if m.err != nil {
		return m.th.Style(m.th.Bad).Render("sensorz: " + m.err.Error())
	}
	if !m.live {
		return m.th.Style(m.th.Dim).Render("sensorz: waiting for the first sensor sample…")
	}

	w, h := m.width, m.height
	if w < 40 || h < 12 {
		return m.th.Style(m.th.Dim).Render(
			fmt.Sprintf("sensorz: terminal too small (%dx%d), need at least 40x12", w, h))
	}

	// The rows are decided by what the machine actually reports: a box with no
	// readable GPU has no GPU panel, and a box with no fan sensor has no fan
	// row, rather than either of them sitting there empty.
	rest := h - headerH
	topH, midH, boardH := layout(rest, m.hasFans() || m.hasDrives(), m.hasBoard())

	var b strings.Builder
	b.WriteString(m.header(w))
	b.WriteByte('\n')
	b.WriteString(m.topRow(w, topH))
	if midH > 0 {
		b.WriteByte('\n')
		b.WriteString(m.midRow(w, midH))
	}
	if boardH > 0 {
		b.WriteByte('\n')
		b.WriteString(m.board(w, boardH))
	}

	if m.cfg.ShowHelp {
		b.WriteByte('\n')
		b.WriteString(m.help(w))
	}
	if warn := m.warningLine(w); warn != "" {
		b.WriteByte('\n')
		b.WriteString(warn)
	}
	return b.String()
}

// headerH is the height of the status bar, title line plus rule.
const headerH = 2

// layout splits the rows below the header into a top, middle and board row.
//
// Rows are dropped from the bottom up when the terminal is short, because the
// top row carries the CPU temperature - the reading that matters on every
// machine - and the middle row carries the fans, which are the second thing
// anyone opens a thermal monitor for. The board sensors are a footnote.
func layout(rest int, hasMid, hasBoard bool) (top, mid, board int) {
	midOK := hasMid && rest >= 18
	boardOK := hasBoard && rest >= 26
	if !midOK && hasBoard && rest >= 18 {
		// With room for only one of the two lower rows, the fans win.
		midOK, boardOK = false, true
	}

	// A CPU temperature graph stops being informative well before twenty rows,
	// so past that the space goes to the panels with more to say.
	const (
		capTop   = 18
		capMid   = 16
		capBoard = 12
	)

	switch {
	case midOK && boardOK:
		board = clampInt(rest/5, 5, 10)
		mid = clampInt((rest-board)*45/100, 8, capMid)
		top = rest - mid - board
	case midOK:
		mid = clampInt(rest*45/100, 8, capMid)
		top = rest - mid
	case boardOK:
		board = clampInt(rest/3, 6, capBoard)
		top = rest - board
	default:
		top = rest
	}

	for top > capTop {
		switch {
		case midOK && mid < capMid:
			mid++
		case boardOK && board < capBoard:
			board++
		default:
			return top, mid, board
		}
		top--
	}
	if top < 9 {
		// Three rows will not fit; keep the top one tall and drop the rest.
		top, mid, board = rest, 0, 0
	}
	return top, mid, board
}

func clampInt(v, lo, hi int) int { return min(max(v, lo), hi) }

// header is the top status bar.
func (m *Model) header(w int) string {
	s := m.Snapshot()
	label := m.th.Style(m.th.Label)
	value := m.th.Style(m.th.Value)

	left := strings.Join([]string{
		label.Render("sensorz"),
		value.Render(m.hostname()),
		label.Render("gpu:" + m.gpuSummary(s)),
		label.Render(fmt.Sprintf("up %s", humanDuration(time.Since(time.Unix(int64(bootTime()), 0))))),
	}, label.Render(" │ "))

	state := ""
	if m.paused {
		state = m.th.Style(m.th.Bad).Bold(true).Render(" PAUSED ")
	} else {
		state = m.th.Style(m.th.Dim).Render(" " + m.cfg.CollectInterval.String())
	}

	// The layout is btop's: the facts on the left, the clock held in the middle
	// on a rule of its own, and the update interval on the right. The clock sits
	// between two rules rather than at the end because that is where the eye
	// lands when it first reads a status bar.
	clock := value.Render(s.Time.Format("15:04:05"))
	rule := m.th.Style(m.th.Border)
	fill := func(n int) string {
		if n < 1 {
			return " "
		}
		return rule.Render(strings.Repeat("\u2500", n))
	}

	fixed := lipgloss.Width(left) + lipgloss.Width(state) + lipgloss.Width(clock) + 6
	spaces := w - fixed
	if spaces < 3 {
		// Too narrow for the full arrangement; the clock and the facts win.
		spaces = max(3, w-lipgloss.Width(left)-lipgloss.Width(clock)-3)
	}
	head := " " + left + " " + fill(spaces/2) + " " + clock + " " + fill(spaces-spaces/2) + state + " "
	head = padLine(truncateStyled(head, w), w)

	// The rule under the bar is the same thin line btop draws below its header,
	// and the panels start immediately below it.
	return head + "\n" + rule.Render(strings.Repeat("\u2500", w))
}

// topRow renders the CPU and GPU temperature panels side by side. A machine
// with no readable GPU gives the whole row to the CPU.
func (m *Model) topRow(w, h int) string {
	const gap = 1
	if !m.hasGPUs() {
		cpu := NewPanel("CPU", w, h, m.th, m.th.PanelCPU)
		cpu.Focus = m.focus == FocusCPU
		return cpu.Render(m.cpuContent(cpu.Inner()))
	}

	// Both rows are split exactly in half, so the four boxes stand in two
	// columns of the same width and the panel borders line up down the screen.
	// An uneven split looks deliberate until you notice the GPU box ending in
	// a different place from the drive box under it.
	cpuW := (w - gap) / 2
	gpuW := w - cpuW - gap

	cpu := NewPanel("CPU", cpuW, h, m.th, m.th.PanelCPU)
	cpu.Focus = m.focus == FocusCPU
	gpu := NewPanel("GPU", gpuW, h, m.th, m.th.PanelGPU)
	gpu.Focus = m.focus == FocusGPU

	return Column(
		cpu.Render(m.cpuContent(cpu.Inner())),
		strings.Repeat(" ", gap),
		gpu.Render(m.gpuContent(gpu.Inner())),
	)
}

// midRow renders the fan and drive panels side by side.
func (m *Model) midRow(w, h int) string {
	const gap = 1
	if !m.hasFans() && !m.hasDrives() {
		return ""
	}
	if !m.hasFans() {
		d := NewPanel("Drives", w, h, m.th, m.th.PanelDrives)
		d.Focus = m.focus == FocusDrives
		return d.Render(m.driveContent(d.Inner()))
	}
	if !m.hasDrives() {
		f := NewPanel("Fans", w, h, m.th, m.th.PanelFans)
		f.Focus = m.focus == FocusFans
		return f.Render(m.fanContent(f.Inner()))
	}

	half := (w - gap) / 2
	fans := NewPanel("Fans", half, h, m.th, m.th.PanelFans)
	fans.Focus = m.focus == FocusFans
	drives := NewPanel("Drives", w-half-gap, h, m.th, m.th.PanelDrives)
	drives.Focus = m.focus == FocusDrives

	return Column(
		fans.Render(m.fanContent(fans.Inner())),
		strings.Repeat(" ", gap),
		drives.Render(m.driveContent(drives.Inner())),
	)
}

// board renders the motherboard sensors: everything the CPU, GPU and drive
// panels did not already claim.
func (m *Model) board(w, h int) string {
	p := NewPanel("Board", w, h, m.th, m.th.PanelBoard)
	p.Focus = m.focus == FocusBoard
	return p.Render(m.boardContent(p.Inner()))
}

// cpuContent draws the CPU panel: the average across every CPU temperature
// sensor as a large figure, its history as a wide graph beneath it, and the
// thresholds it is judged against along the bottom.
//
// The big number is the headline because a 32-core machine has 33 temperature
// channels and the user wants one figure that says "how hot is my CPU". The
// detail graphs below it are still there, for the moment when the average is
// not enough - finding the one core running 15 degrees hotter than its
// neighbours.
func (m *Model) cpuContent(innerW, innerH int) []string {
	if innerW < 8 {
		return nil
	}
	mt, ok := m.Metric(collect.MetricCPUTemp)
	if !ok {
		return []string{m.th.Style(m.th.Dim).Render("no CPU temperature sensors exposed")}
	}

	// A graph is worth three rows on its own, so whatever the panel cannot give
	// the graph comes out of the figure's surroundings.
	// One number and its history. The individual cores are deliberately not
	// listed: on a 32 core machine that is 32 rows of near-identical numbers,
	// and the average is the figure that answers the question the panel was
	// opened to ask.
	head, _ := m.headline(innerW, mt, "temp", innerH-minGraphRows-1)
	rows := head
	rows = append(rows, m.historyGraph(innerW, innerH-len(rows)-1, mt)...)
	rows = append(rows, m.footer(innerW, mt))
	return rows
}

// minGraphRows is the fewest rows a history graph is worth drawing in. Below
// three the curve is unreadable, so the panel shows the number and the
// thresholds instead of a graph that says nothing.
const minGraphRows = 3

// headline renders the panel's one important number: what is being measured,
// the value as a large block figure, and its unit underneath.
//
// The figure is drawn as a grid of blocks rather than as text because a
// terminal has one font size: the only way to make a number bigger is to draw
// it. The unit goes below rather than beside, so the digits line up on the same
// row whatever the unit is.
func (m *Model) headline(w int, mt model.Metric, caption string, budget int) (rows []string, figureW int) {
	color := m.th.Ramp(mt.Value, mt.Warn, mt.Crit)
	style := m.th.Style(color).Bold(true)

	// Half size: one cell per pixel, with the pixel rows packed two to a cell
	// by the half-block glyphs. A digit is three cells wide and three rows
	// tall, which still reads as a display figure but leaves the panel for the
	// bar and the graph.
	number := bigText(mt)
	if BigNumberWidth(number, 1) > w {
		number = bigDigits(mt)
	}
	figure := BigNumberHalf(number, 1)
	figureW = BigNumberWidth(number, 1)

	const (
		barW   = 6
		barGap = 2
	)

	rows = []string{center(m.th.Style(m.th.Label).Render(truncate(mt.Label, w)), w)}
	// The air above the figure is the first thing to go: on a short panel the
	// graph is worth more than the spacing, and a blank row is a row of
	// history not shown.
	if budget-len(rows) >= 5 {
		rows = append(rows, "")
	}

	// The figure block is the number with its caption underneath. The bar is
	// drawn at least as tall as that, and takes any rows left over as well: a
	// four row bar cannot show where a reading sits on its scale, so the extra
	// room goes to the bar rather than to air.
	hasCaption := budget-len(rows) >= 2
	blockH := len(figure)
	if hasCaption {
		blockH++
	}
	extra := clampInt(budget-len(rows)-blockH, 0, 4)
	barH := blockH + extra
	bar := m.levelBar(barW, barH, mt)

	for i := 0; i < barH; i++ {
		right := ""
		switch {
		case i < len(figure):
			right = style.Render(figure[i])
		case hasCaption && i == len(figure):
			right = m.th.Style(m.th.Dim).Render(truncate(caption, w))
		}
		rows = append(rows, center(bar[i]+strings.Repeat(" ", barGap)+right, w))
	}
	if extra == 0 && budget-len(rows) >= 1 {
		rows = append(rows, "")
	}
	return rows, figureW
}

// center puts a rendered row in the middle of the width it is drawn into.
//
// The panels hold one number and nothing beside them now, so it is centred
// rather than left aligned: an empty box with a figure jammed against its left
// edge reads as a mistake, and a centred one reads as a readout.
func center(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return truncateStyled(s, w)
	}
	left := (w - sw) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-left-sw)
}

// bigText is the text drawn as the big figure: the value, and its unit when
// there is room for one.
func bigText(mt model.Metric) string {
	value := model.FormatValueCompact(mt.Kind, mt.Value)
	switch mt.Kind {
	case model.KindTemperature:
		return value + "\u00b0C"
	case model.KindFan:
		return value + "RPM"
	default:
		return value
	}
}

// bigDigits is the fallback when even the value alone is too wide for the
// panel: the unit has to go rather than the digits.
func bigDigits(mt model.Metric) string {
	return model.FormatValueCompact(mt.Kind, mt.Value)
}

// historyGraph draws the metric's history as a wide, thick dot graph filling
// the rows it is given.
//
// Thick matters more than tall here: across seventy columns a one dot trace is
// a hairline, and a hairline that steps four times a pixel is impossible to
// read at a glance. Two dot rows of trace makes it a line again without hiding
// the shape of the curve.
func (m *Model) historyGraph(w, h int, mt model.Metric) []string {
	h = min(h, 10)
	if h < minGraphRows {
		return nil
	}
	return RenderGraph(m.Values(mt.ID), GraphOptions{
		Width: w, Height: h,
		Min: mt.Min, Max: mt.Max,
		Fill:  false,
		Thick: 2,
		Warn:  mt.Warn,
		Crit:  mt.Crit,
	}, m.th)
}

// footer is the bottom line of a panel: what the number above it is judged
// against, so the colours on the graph have a meaning the reader can see.
func (m *Model) footer(w int, mt model.Metric) string {
	var parts []string
	if mt.Hint != "" {
		parts = append(parts, m.th.Style(m.th.Dim).Render("avg of "+mt.Hint))
	}
	if mt.Warn > 0 && mt.Crit > 0 {
		parts = append(parts,
			m.th.Style(m.th.Warn).Render(fmt.Sprintf("warn %.1f\u00b0C", mt.Warn)),
			m.th.Style(m.th.Bad).Render(fmt.Sprintf("crit %.1f\u00b0C", mt.Crit)))
	}
	return padLine(strings.Join(parts, m.th.Style(m.th.Dim).Render("   ")), w)
}

// moreRow says how many sensors did not fit, because a silently truncated list
// reads as "this machine only has three temperatures".
func (m *Model) moreRow(missing, w int) string {
	if missing <= 0 || m.showAllSensors {
		return ""
	}
	return m.th.Style(m.th.Dim).Render(
		truncate(fmt.Sprintf("+%d more (a shows all)", missing), w))
}

// cpuDetail returns the CPU's individual temperature sensors, ordered so the
// package sensor comes first and the hottest core next.
func (m *Model) cpuDetail() []model.Metric {
	var out []model.Metric
	for _, mt := range m.Snapshot().MetricsByCategory(model.CategorySensor) {
		if mt.Kind != model.KindTemperature || !sensors.IsCPUChip(mt.Group) {
			continue
		}
		out = append(out, mt)
	}
	if m.showAllSensors {
		sortCPUTemps(out)
		return out
	}
	return budgetTemps(out, 8)
}

// sortCPUTemps orders CPU sensors with the package first, then hottest first,
// so the graph order is stable but still puts the interesting one on top.
func sortCPUTemps(ms []model.Metric) {
	sort.SliceStable(ms, func(i, j int) bool {
		pi, pj := isPackageLabel(ms[i].Label), isPackageLabel(ms[j].Label)
		if pi != pj {
			return pi
		}
		return ms[i].Value > ms[j].Value
	})
}

// budgetTemps keeps the package sensor plus the hottest of the rest, capped at
// n entries.
//
// The cap matters on a 32-core machine, where every core has its own channel:
// showing all of them turns the CPU panel into a wall of identical graphs and
// pushes everything else off the screen.
func budgetTemps(in []model.Metric, n int) []model.Metric {
	if len(in) <= n {
		return in
	}
	out := make([]model.Metric, 0, n)
	var rest []model.Metric
	for _, mt := range in {
		if isPackageLabel(mt.Label) {
			out = append(out, mt)
		} else {
			rest = append(rest, mt)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Value > rest[j].Value })
	for _, mt := range rest {
		if len(out) >= n {
			break
		}
		out = append(out, mt)
	}
	return out
}

func isPackageLabel(l string) bool {
	l = strings.ToLower(l)
	return strings.Contains(l, "package") || strings.Contains(l, "tdie") ||
		strings.Contains(l, "tctl") || strings.Contains(l, "tctl/tdie")
}

// gpuContent draws the GPU panel: one graph covering every readable card, then
// a line per card.
//
// The panel exists only when at least one GPU produced a real temperature, so
// a card whose driver exposes nothing is absent from the dashboard rather than
// drawn as a permanently idle 0C line. Where there is more than one card the
// graph is the average of them all and the per-card lines below say who is
// actually running hot.
func (m *Model) gpuContent(innerW, innerH int) []string {
	if innerW < 8 {
		return nil
	}
	devs := m.readableGPUs()
	mt, ok := m.Metric(collect.MetricGPUTemp)
	if len(devs) == 0 || !ok {
		return []string{m.th.Style(m.th.Dim).Render("no GPU temperature sensors")}
	}

	// One row is reserved per card: every card has to stay visible, so the
	// graph gives way to them rather than the other way round.
	rows, _ := m.headline(innerW, mt, "temp", innerH-len(devs)-minGraphRows-1)
	rows = append(rows, m.historyGraph(innerW, innerH-len(rows)-len(devs)-1, mt)...)
	for _, d := range devs {
		rows = append(rows, m.gpuLine(d, innerW))
	}
	rows = append(rows, m.footer(innerW, mt))
	return rows
}

// gpuLine renders one GPU as a single row: name, temperature, and whichever
// auxiliary readings the card exposes.
func (m *Model) gpuLine(d model.Device, innerW int) string {
	color := m.th.Color(d.Temperature, 80, 90)
	temp := m.th.Style(color).Bold(true).Render(model.FormatValue(model.KindTemperature, d.Temperature))

	var extras []string
	for _, t := range d.ExtraTemps {
		if t.Label == "" || t.Value != t.Value {
			continue
		}
		extras = append(extras, m.th.Style(m.th.Dim).Render(t.Label)+" "+m.th.Style(m.th.Value).
			Render(model.FormatValueCompact(model.KindTemperature, t.Value)+"°"))
	}
	if d.FanPercent >= 0 {
		extras = append(extras, m.th.Style(m.th.Dim).Render("fan")+" "+m.th.Style(m.th.Value).
			Render(model.FormatValueCompact(model.KindUtilization, d.FanPercent)+"%"))
	}

	label := m.th.Style(m.th.Label).Render(truncate("GPU"+itoaStr(d.Index)+" "+shortDeviceName(d), max(0, innerW-16)))
	extrasText := strings.Join(extras, m.th.Style(m.th.Dim).Render(" · "))
	row := label + " " + temp
	if pad := innerW - lipgloss.Width(row) - lipgloss.Width(extrasText); pad > 1 {
		row += strings.Repeat(" ", pad)
	} else {
		row += " "
	}
	return truncateStyled(row+extrasText, innerW)
}

// fanContent draws the fan panel: the average speed across every fan first,
// then each fan's own graph.
func (m *Model) fanContent(innerW, innerH int) []string {
	if innerW < 8 {
		return nil
	}
	mt, ok := m.Metric(collect.MetricFanAvg)
	if !ok {
		return []string{m.th.Style(m.th.Dim).Render("no fan sensors found")}
	}

	// The average, the same shape as the CPU panel. The individual fans are not
	// listed here either: a case has eight of them and they all sit within a
	// few hundred RPM of each other, so the average is the figure that says
	// whether the cooling is keeping up.
	rows, _ := m.headline(innerW, mt, "fan", innerH-minGraphRows-1)
	rows = append(rows, m.historyGraph(innerW, innerH-len(rows)-1, mt)...)
	rows = append(rows, m.footer(innerW, mt))
	return rows
}

// driveContent draws one temperature graph per drive, with the drive's
// secondary sensors in the hint line beneath it.
func (m *Model) driveContent(innerW, innerH int) []string {
	if innerW < 8 {
		return nil
	}
	ms := m.MetricsFor(model.CategoryDisk)
	if len(ms) == 0 {
		return []string{m.th.Style(m.th.Dim).Render("no drive temperature sensors")}
	}

	// Split each drive's series into the headline composite and the extras,
	// which is what tells an NVMe about to throttle from one that is merely
	// warm.
	type drive struct {
		temp   *model.Metric
		extras []model.Metric
	}
	byDev := map[string]*drive{}
	var order []string
	for i := range ms {
		mt := ms[i]
		d, ok := byDev[mt.Label]
		if !ok {
			d = &drive{}
			byDev[mt.Label] = d
			order = append(order, mt.Label)
		}
		switch mt.Group {
		case "SSD Temp", "HDD Temp":
			cp := mt
			d.temp = &cp
		default:
			d.extras = append(d.extras, mt)
		}
	}
	sort.Strings(order)

	const graphH = 3
	// The extras are the first thing to go when the panel is short, because the
	// composite graph above them already carries the headline figure.
	showExtras := innerH >= len(order)*(graphH+1)

	var rows []string
	for _, name := range order {
		d := byDev[name]
		if d.temp == nil {
			continue
		}
		rows = append(rows, RenderGraph(m.Values(d.temp.ID), GraphOptions{
			Width: innerW, Height: graphH,
			Min: d.temp.Min, Max: d.temp.Max,
			Fill:  false,
			Warn:  d.temp.Warn,
			Crit:  d.temp.Crit,
			Label: name,
			// WithUnits: a drive's rows are labelled by the kernel's own
			// channel names, which say nothing about the quantity, so the
			// number has to say it is a temperature.
			Value: readout(*d.temp, true),
		}, m.th)...)
		if showExtras && len(d.extras) > 0 {
			parts := make([]string, 0, len(d.extras))
			for _, e := range d.extras {
				parts = append(parts, m.th.Style(m.th.Ramp(e.Value, e.Warn, e.Crit)).
					Render(e.Group+" "+readout(e, true)))
			}
			rows = append(rows, m.th.Style(m.th.Dim).Render(truncate(strings.Join(parts, "  "), innerW)))
		}
	}
	return rows
}

// boardContent draws the motherboard's own temperature sensors, grouped by the
// chip that reported them.
func (m *Model) boardContent(innerW, innerH int) []string {
	if innerW < 8 {
		return nil
	}
	ms := m.boardTemps()
	if len(ms) == 0 {
		return []string{m.th.Style(m.th.Dim).Render("no motherboard temperature sensors")}
	}

	byChip := map[string][]model.Metric{}
	var chips []string
	for _, mt := range ms {
		if _, ok := byChip[mt.Group]; !ok {
			chips = append(chips, mt.Group)
		}
		byChip[mt.Group] = append(byChip[mt.Group], mt)
	}
	sort.Strings(chips)

	var rows []string
	for _, chip := range chips {
		rows = append(rows, m.chipHeading(innerW, chip, len(byChip[chip])))
		// WithUnits: these readings are labelled by the board's own wiring
		// ("SYSTIN", "AUXTIN2"), which says nothing about what they measure, so
		// the number has to say what it is. A bare "36" on a row labelled
		// AUXTIN2 is not a temperature the reader can identify.
		grid, shown := m.tempGrid(innerW, innerH-len(rows), byChip[chip], true)
		rows = append(rows, grid...)
		if r := m.moreRow(len(byChip[chip])-shown, innerW); r != "" {
			rows = append(rows, r)
		}
	}
	return rows
}

// boardTemps returns the temperatures that belong to no other panel: the
// motherboard thermistors, memory modules and anything else the board reports.
func (m *Model) boardTemps() []model.Metric {
	var out []model.Metric
	for _, mt := range m.Snapshot().MetricsByCategory(model.CategorySensor) {
		if mt.Kind != model.KindTemperature {
			continue
		}
		if sensors.IsCPUChip(mt.Group) || sensors.IsGPUChip(mt.Group) || sensors.IsDriveChip(mt.Group) {
			continue
		}
		out = append(out, mt)
	}
	if !m.showAllSensors {
		out = budgetTemps(out, 12)
	}
	return out
}

// tempGrid renders temperature graphs in a multi-column grid, one graph and its
// number per cell.
//
// The columns are built as full stacks of rows and then joined row by row, so
// every cell keeps the same height however long its label is and the grid stays
// aligned from frame to frame. Each cell is a complete graph - label, number and
// braille plot - because a grid of numbers with no plot would hide exactly the
// thing a temperature monitor is for.
func (m *Model) tempGrid(w, availRows int, temps []model.Metric, withUnits bool) ([]string, int) {
	if len(temps) == 0 || w < 12 {
		return nil, 0
	}
	// One graph plus the blank line under it. Each cell is deliberately
	// generous: braille puts two dots across and four down per cell, so a wider
	// cell is twice the resolution of the same graph drawn smaller, which is
	// what turns "a plain line" into a readable trend.
	const cellW = 34
	cellH := 7

	// A graph needs all of its rows or it is not a graph: half a plot is a
	// rectangle of empty cells, which looks like a broken sensor rather than a
	// small one. So the cell shrinks whole or not at all, and when even the
	// small cell will not fit the panel falls back to plain label-and-number
	// rows, which is honest about what there is room for.
	if availRows > 0 {
		switch {
		case availRows >= 7:
			cellH = 7
		case availRows >= 5:
			cellH = 5
		default:
			return m.tempList(w, availRows, temps, withUnits), len(temps)
		}
	}

	cols := min(max(1, w/cellW), len(temps))
	perCol := (len(temps) + cols - 1) / cols
	if availRows > 0 {
		// Only lay out the rows that will actually be visible, so a long list
		// of sensors does not spend the panel on the ones cut off.
		if maxRows := max(1, availRows/cellH); perCol > maxRows {
			perCol = maxRows
			temps = temps[:min(len(temps), perCol*cols)]
			cols = min(cols, len(temps))
			perCol = (len(temps) + cols - 1) / cols
		}
	}
	if perCol == 0 {
		return nil, 0
	}

	// Build each column as one flat stack of rows, then pad the columns to a
	// common height when joining them.
	cols0 := make([][]string, cols)
	for c := 0; c < cols; c++ {
		var block []string
		for i := 0; i < perCol; i++ {
			idx := c*perCol + i
			if idx >= len(temps) {
				break
			}
			mt := temps[idx]
			block = append(block, RenderGraph(m.Values(mt.ID), GraphOptions{
				Width: min(cellW, w), Height: cellH - 1,
				Min: mt.Min, Max: mt.Max,
				Fill:  false,
				Warn:  mt.Warn,
				Crit:  mt.Crit,
				Label: mt.Label,
				Value: readout(mt, withUnits),
			}, m.th)...)
			block = append(block, "") // one blank row between graphs
		}
		cols0[c] = block
	}

	height := 0
	for _, block := range cols0 {
		height = max(height, len(block))
	}

	out := make([]string, 0, height)
	shown := 0
	for r := 0; r < height; r++ {
		parts := make([]string, 0, cols*2)
		for c := 0; c < cols; c++ {
			block := cols0[c]
			if r < len(block) {
				parts = append(parts, padLine(block[r], min(cellW, w)))
				if (r+1)%cellH == 0 {
					shown++
				}
			} else {
				parts = append(parts, strings.Repeat(" ", min(cellW, w)))
			}
			if c < cols-1 {
				parts = append(parts, " ")
			}
		}
		out = append(out, strings.Join(parts, ""))
	}
	return out, min(shown, len(temps))
}

// tempList renders temperatures as label-and-number rows, for panels too short
// for graphs. It is the fallback, not the default: a number with no history is
// still worth showing, but only when there is no room for the history.
func (m *Model) tempList(w, availRows int, temps []model.Metric, withUnits bool) []string {
	const cellW = 20
	cols := max(1, min(w/cellW, len(temps)))
	rows := (len(temps) + cols - 1) / cols
	truncated := 0
	if availRows > 0 && rows > availRows {
		truncated = len(temps) - availRows*cols
		rows = availRows
	}

	out := make([]string, 0, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			i := r*cols + c
			if i >= len(temps) {
				continue
			}
			mt := temps[i]
			value := readout(mt, withUnits)
			cell := m.th.Style(m.th.Label).Render(truncate(mt.Label, cellW-lipgloss.Width(value)-2)) +
				" " + m.th.Style(m.th.Ramp(mt.Value, mt.Warn, mt.Crit)).Render(value)
			if pad := cellW - lipgloss.Width(cell); pad > 0 {
				cell += strings.Repeat(" ", pad)
			}
			b.WriteString(cell)
		}
		line := b.String()
		if r == rows-1 && truncated > 0 {
			line += m.th.Style(m.th.Dim).Render(fmt.Sprintf("+%d", truncated))
		}
		out = append(out, line)
	}
	return out
}

// readout renders a value for a grid cell, with its unit when the panel needs
// one. The unit is what tells a reader that the number on a row labelled
// "AUXTIN2" is a temperature and not a voltage or a count.
func readout(mt model.Metric, withUnits bool) string {
	if !withUnits {
		return model.FormatValueCompact(mt.Kind, mt.Value)
	}
	return model.FormatValue(mt.Kind, mt.Value)
}

// chipHeading renders the sub-heading above one chip's temperature graphs.
func (m *Model) chipHeading(w int, chip string, n int) string {
	txt := chip
	if n > 1 {
		txt += " (" + itoaStr(n) + ")"
	}
	return m.th.Style(m.th.Title).Render(truncate(txt, w))
}

// warningLine surfaces collector problems without stealing screen space.
func (m *Model) warningLine(w int) string {
	warns := m.Snapshot().Warnings
	if len(warns) == 0 {
		return ""
	}
	if len(warns) > 2 {
		warns = append(warns[:2:2], fmt.Sprintf("(+%d more)", len(warns)-2))
	}
	return m.th.Style(m.th.Warn).Render(truncate("! "+strings.Join(warns, "; "), w))
}

// help renders the keybinding overlay.
func (m *Model) help(w int) string {
	keys := [][2]string{
		{"↑/↓ tab", "cycle panels"},
		{"a", "all sensors"},
		{"p", "pause history"},
		{"r", "reset history"},
		{"?", "toggle help"},
		{"q", "quit"},
	}
	var cols []string
	for _, k := range keys {
		cols = append(cols, m.th.Style(m.th.Accent).Render(k[0])+" "+m.th.Style(m.th.Dim).Render(k[1]))
	}
	line := strings.Join(cols, m.th.Style(m.th.Dim).Render("  ·  "))
	return truncate(line, w)
}

// shortDeviceName shortens a GPU name so it fits a panel row.
func shortDeviceName(d model.Device) string {
	name := d.Name
	for _, drop := range []string{"NVIDIA GeForce ", "AMD Radeon ", "Intel(R) ", "Radeon ", "GeForce ", "NVIDIA "} {
		if strings.HasPrefix(name, drop) {
			name = strings.TrimPrefix(name, drop)
			break
		}
	}
	return name
}

func (m *Model) hostname() string {
	if m.cfg.Hostname != "" {
		return m.cfg.Hostname
	}
	h, err := hostName()
	if err != nil {
		return "linux"
	}
	return h
}

// gpuSummary is the one-line GPU fact in the header: how many cards are
// readable and how hot the hottest of them is.
func (m *Model) gpuSummary(s model.Snapshot) string {
	devs := m.readableGPUs()
	if len(devs) == 0 {
		if len(s.Devices) > 0 {
			// Cards are there but nothing can read their temperature. Saying
			// which backend answered is the difference between "no GPU" and
			// "a GPU whose driver exposes nothing".
			backend := ""
			if m.cfg.GPUBackend != "" && m.cfg.GPUBackend != "none" {
				backend = " via " + m.cfg.GPUBackend
			}
			return fmt.Sprintf("%d unreadable%s", len(s.Devices), backend)
		}
		return "none"
	}
	var hottest float64
	for _, d := range devs {
		if d.Temperature > hottest {
			hottest = d.Temperature
		}
	}
	if len(devs) == 1 {
		return fmt.Sprintf("%.0f°C", hottest)
	}
	return fmt.Sprintf("%d @%.0f°C", len(devs), hottest)
}

// readableGPUs returns the devices that produced a temperature this tick.
func (m *Model) readableGPUs() []model.Device {
	var out []model.Device
	for _, d := range m.Snapshot().Devices {
		if d.HasTemperature() {
			out = append(out, d)
		}
	}
	return out
}

func (m *Model) hasGPUs() bool { return len(m.readableGPUs()) > 0 }

// hasFans reports whether any fan sensor exists, which decides whether the fan
// row is drawn at all.
func (m *Model) hasFans() bool {
	_, ok := m.Metric(collect.MetricFanAvg)
	return ok
}

// hasDrives reports whether any drive exposes a temperature.
func (m *Model) hasDrives() bool { return len(m.MetricsFor(model.CategoryDisk)) > 0 }

// hasBoard reports whether there are motherboard sensors worth a panel.
func (m *Model) hasBoard() bool { return len(m.boardTemps()) > 0 }

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func itoaStr(i int) string { return fmt.Sprintf("%d", i) }
