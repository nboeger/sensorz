package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nathan/sensorz/internal/collect"
	"github.com/nathan/sensorz/internal/collect/sensors"
	"github.com/nathan/sensorz/internal/history"
	"github.com/nathan/sensorz/internal/model"
)

// Messages delivered to the bubbletea update loop.
type (
	// snapshotMsg carries a completed collection pass.
	snapshotMsg model.Snapshot
	// tickMsg drives the redraw clock. Collection is on its own timer; the
	// redraw runs faster so a graph can animate between samples if the
	// collector is slower than the display.
	tickMsg time.Time
	// errorMsg reports a fatal startup problem.
	errorMsg struct{ err error }
)

// Focus identifies the panel the keyboard cursor is on.
type Focus int

// The panels the user can cycle between. They map one to one onto the rows of
// the dashboard: CPU and GPU share the top row, Fans and Drives the row below,
// and Board the last one.
const (
	FocusCPU Focus = iota
	FocusGPU
	FocusFans
	FocusDrives
	FocusBoard
)

func (f Focus) String() string {
	switch f {
	case FocusCPU:
		return "cpu"
	case FocusGPU:
		return "gpu"
	case FocusFans:
		return "fans"
	case FocusDrives:
		return "drives"
	case FocusBoard:
		return "board"
	default:
		return "?"
	}
}

// Model is the bubbletea model: it owns the latest snapshot, the history store
// and the UI state.
type Model struct {
	agg  *collect.Aggregator
	hist *history.Store
	th   Theme
	cfg  Config

	mu       sync.RWMutex
	snapshot model.Snapshot
	// metricsByID indexes the latest snapshot so the renderer can look up the
	// bounds and thresholds of a series without re-scanning the slice.
	metricsByID map[string]model.Metric

	width, height int
	focus         Focus
	// showAllSensors reveals the full hwmon list; by default the sensor panel
	// is limited to the metrics that matter.
	showAllSensors bool
	// paused stops pushing new samples into history while keeping the display
	// live, so a spike can be inspected.
	paused bool
	// showThemeMenu displays the theme selection overlay.
	showThemeMenu bool
	err           error

	// live tracks whether the collector has produced anything yet.
	live bool

	// cancel stops the collector goroutine, and snapshots carries its output
	// back into the update loop.
	cancel    context.CancelFunc
	snapshots chan model.Snapshot
}

// New builds the UI model.
func New(agg *collect.Aggregator, hist *history.Store, cfg Config, th Theme) *Model {
	return &Model{
		agg:         agg,
		hist:        hist,
		cfg:         cfg,
		th:          th,
		metricsByID: map[string]model.Metric{},
		width:       80,
		height:      24,
		focus:       FocusCPU,
		snapshot:    model.Snapshot{Time: time.Now()},
	}
}

// Init starts the collector goroutine and the redraw ticker.
//
// The collector runs outside the bubbletea loop and delivers snapshots through
// a channel: a slow or blocking sensor read must never stall the UI's message
// pump, or the whole terminal freezes while a single sysfs file is read.
func (m *Model) Init() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.snapshots = make(chan model.Snapshot, 4)

	go func() {
		m.agg.Run(ctx, func(s model.Snapshot) {
			select {
			case m.snapshots <- s:
			default:
				// The UI is behind; drop this sample rather than let the
				// collector block. The next tick is what the user sees.
			}
		})
	}()

	return tea.Batch(m.waitForSnapshot(), m.tick())
}

// waitForSnapshot blocks until the collector produces something.
func (m *Model) waitForSnapshot() tea.Cmd {
	return func() tea.Msg {
		s, ok := <-m.snapshots
		if !ok {
			return errorMsg{fmt.Errorf("collector stopped")}
		}
		return snapshotMsg(s)
	}
}

// tick schedules the next repaint.
func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.cfg.RefreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case snapshotMsg:
		m.applySnapshot(model.Snapshot(msg))
		// Re-arm immediately so the next snapshot is picked up as soon as it
		// arrives rather than one repaint late.
		return m, m.waitForSnapshot()

	case tickMsg:
		return m, m.tick()

	case errorMsg:
		m.err = msg.err
		return m, tea.Quit

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "up", "k":
		m.cycleFocus(-1)
	case "down", "j":
		m.cycleFocus(1)

	case "tab":
		m.cycleFocus(1)
	case "shift+tab":
		m.cycleFocus(-1)

	case "a":
		m.showAllSensors = !m.showAllSensors
	case "p":
		m.paused = !m.paused
	case "r":
		// Drop history and start over, which is the quickest way to compare a
		// short burst against a long trend.
		m.hist = history.NewStore(m.cfg.HistoryCapacity)
		m.paused = false
	case "?", "h":
		m.cfg.ShowHelp = !m.cfg.ShowHelp
	case "t":
		m.showThemeMenu = !m.showThemeMenu
	case "1", "2", "3", "4", "5":
		if m.showThemeMenu {
			m.selectTheme(msg.String())
		}
	case "esc":
		m.showThemeMenu = false
	}
	return m, nil
}

// cycleFocus moves the panel cursor by delta, skipping panels that are not on
// screen.
//
// The dashboard only draws a GPU panel when a GPU is readable, and only draws
// the fan, drive and board panels when those sensors exist, so a fixed cycle
// would spend keystrokes on panels the user cannot see.
func (m *Model) cycleFocus(delta int) {
	visible := m.visibleFocus()
	if len(visible) == 0 {
		return
	}
	idx := 0
	for i, f := range visible {
		if f == m.focus {
			idx = i
			break
		}
	}
	m.focus = visible[(idx+delta+len(visible))%len(visible)]
}

// selectTheme changes the theme based on the key pressed (1-5).
func (m *Model) selectTheme(key string) {
	themes := AvailableThemes()
	idx := int(key[0] - '1')
	if idx >= 0 && idx < len(themes) {
		name := themes[idx]
		m.cfg.ThemeName = name
		m.th = ThemeByName(name)
		m.showThemeMenu = false
	}
}

// visibleFocus lists the panels that will actually be drawn, in reading order.
func (m *Model) visibleFocus() []Focus {
	out := []Focus{FocusCPU}
	if m.hasGPUs() {
		out = append(out, FocusGPU)
	}
	if m.hasFans() || m.hasDrives() {
		if m.hasFans() {
			out = append(out, FocusFans)
		}
		if m.hasDrives() {
			out = append(out, FocusDrives)
		}
	}
	if m.hasBoard() {
		out = append(out, FocusBoard)
	}
	return out
}

// applySnapshot stores a new snapshot and records it in history.
func (m *Model) applySnapshot(s model.Snapshot) {
	m.mu.Lock()
	m.snapshot = s
	m.metricsByID = make(map[string]model.Metric, len(s.Metrics))
	ids := make([]string, 0, len(s.Metrics))
	for _, mt := range s.Metrics {
		m.metricsByID[mt.ID] = mt
		ids = append(ids, mt.ID)
	}
	m.live = true
	m.mu.Unlock()

	if m.paused {
		return
	}
	// Push only the metrics belonging to the currently displayed categories;
	// recording the whole hwmon set would grow the store without bound on a
	// board with hundreds of channels, most of which the user never sees.
	m.hist.PushSnapshot(ids, func(id string) (float64, bool) {
		mt, ok := m.metricsByID[id]
		if !ok || !mt.Valid() {
			return 0, false
		}
		return mt.Value, true
	}, s.Time)
}

// Values returns a metric's history for rendering, or nil when it has none.
func (m *Model) Values(id string) []float64 {
	s := m.hist.Get(id)
	if s == nil {
		return nil
	}
	return s.Values()
}

// Metric returns the latest reading of a metric.
func (m *Model) Metric(id string) (model.Metric, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mt, ok := m.metricsByID[id]
	return mt, ok
}

// Snapshot returns the latest snapshot.
func (m *Model) Snapshot() model.Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

// MetricsFor returns the metrics of a category, optionally restricted to the
// groups whose names appear in groups (empty means all groups).
func (m *Model) MetricsFor(cat model.Category, groups ...string) []model.Metric {
	ms := m.Snapshot().MetricsByCategory(cat)
	if len(groups) == 0 {
		return ms
	}
	want := make(map[string]bool, len(groups))
	for _, g := range groups {
		want[g] = true
	}
	out := make([]model.Metric, 0, len(ms))
	for _, mt := range ms {
		if want[mt.Group] {
			out = append(out, mt)
		}
	}
	return out
}

// Close stops the collector goroutine.
func (m *Model) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.agg.Close()
}

// DefaultFilter is the hwmon filter the UI uses when the caller does not
// supply one. It is re-exported so cmd/sensorz does not need its own import.
func DefaultFilter() sensors.Filter { return sensors.DefaultFilter() }

// SetShowAllSensors toggles the full hwmon listing, which the -all-sensors
// flag controls.
func (m *Model) SetShowAllSensors(v bool) { m.showAllSensors = v }
