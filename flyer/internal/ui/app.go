// Package ui provides a Bubble Tea-based TUI for Flyer.
package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/config"
	"github.com/five82/flyer/internal/prefs"
	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/state"
)

// View represents the current active view.
type View int

const (
	ViewQueue View = iota
	ViewLogs
	ViewProblems
)

// inspectorTab identifies a tab inside the item inspector.
type inspectorTab int

const (
	tabOverview inspectorTab = iota
	tabEpisodes
	tabProblems
	tabLogs
	tabCount
)

// QueueFilter represents the queue filter mode.
type QueueFilter int

const (
	FilterAll QueueFilter = iota
	FilterFailed
	FilterReview
	FilterProcessing
)

// detailState holds per-item detail view state.
type detailState struct {
	episodeCollapsed map[int64]bool
}

// Options configures the UI.
type Options struct {
	Context   context.Context
	Client    *spindle.Client
	Store     *state.Store
	Config    *config.Config
	PollTick  time.Duration
	ThemeName string
	PrefsPath string

	// Refresh forces an immediate poll of the Spindle API, updating the
	// store. Used by the manual refresh key.
	Refresh func() error
}

// Model is the root application state for Bubble Tea.
type Model struct {
	// Configuration
	ctx       context.Context
	client    *spindle.Client
	store     *state.Store
	config    *config.Config
	prefsPath string
	pollTick  time.Duration
	refreshFn func() error

	// Key bindings
	keys keyMap

	// UI state
	theme       Theme
	currentView View
	width       int
	height      int
	ready       bool

	// Data state
	snapshot    state.Snapshot
	lastUpdated time.Time

	// Queue state
	selectedRow int
	queueScroll int
	filterMode  QueueFilter

	// Queue text filter ("/" in the queue view)
	queueFilterActive bool // input is capturing keys
	queueFilterQuery  string
	queueFilterInput  textinput.Model

	// Spinner shown while connecting/offline
	spinnerFrame int
	spinnerOn    bool

	// Inspector state (full-screen single-item view)
	inspecting        bool
	inspectorTab      inspectorTab
	inspectedID       int64
	returnView        View // view Esc returns to
	inspectorViewport viewport.Model
	detailState       detailState

	// Log state
	logViewport viewport.Model
	logState    logState

	// Problems (triage) state
	problemsRow    int
	problemsScroll int
	problemsState  problemsState

	// Modal overlay (help, log filters, etc.)
	activeModal Modal

	// Log filters modal state (separate from Modal interface for simplicity)
	showLogFilters    bool
	logFilterInputs   [4]textinput.Model // level, component, lane, request
	logFilterFocusIdx int

	// Transient error display
	errorMsg    string
	errorExpiry time.Time
}

// New creates a new Bubble Tea model.
func New(opts Options) Model {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}

	pollTick := opts.PollTick
	if pollTick == 0 {
		pollTick = time.Second
	}

	themeName := opts.ThemeName
	if themeName == "" {
		themeName = "Slate"
	}

	prefsPath := opts.PrefsPath
	if prefsPath == "" {
		prefsPath = prefs.DefaultPath()
	}

	filterInput := textinput.New()
	filterInput.Prompt = "" // the filter line renders its own "/" prefix
	filterInput.Placeholder = "title or #id"
	filterInput.CharLimit = 80

	return Model{
		ctx:              ctx,
		client:           opts.Client,
		store:            opts.Store,
		config:           opts.Config,
		prefsPath:        prefsPath,
		pollTick:         pollTick,
		refreshFn:        opts.Refresh,
		keys:             DefaultKeyMap(),
		theme:            GetTheme(themeName),
		currentView:      ViewQueue,
		queueFilterInput: filterInput,
		spinnerOn:        true,
		detailState: detailState{
			episodeCollapsed: make(map[int64]bool),
		},
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tickCmd(m.pollTick),
		spinnerTickCmd(),
	}
	// Fetch snapshot immediately on start
	if m.store != nil {
		cmds = append(cmds, fetchSnapshotCmd(m.store))
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.initInspectorViewport()
			m.initLogState()
			m.initLogViewport()
			m.initLogFilterInputs()
		}
		m.ready = true
		m.updateQueueTable()
		m.updateInspectorViewport()
		m.updateLogViewport()
		return m, nil

	case tickMsg:
		return m.handleTick()

	case spinnerTickMsg:
		if m.spinnerActive() {
			m.spinnerFrame++
			return m, spinnerTickCmd()
		}
		m.spinnerOn = false
		return m, nil

	case snapshotMsg:
		m.snapshot = state.Snapshot(msg)
		m.lastUpdated = time.Now()
		m.updateQueueTable()
		m.clampProblemsRow()
		m.updateInspectorViewport()
		// Restart the spinner if the daemon went offline while it was idle.
		if m.spinnerActive() && !m.spinnerOn {
			m.spinnerOn = true
			return m, spinnerTickCmd()
		}
		return m, nil

	case logBatchMsg:
		m.handleLogBatch(msg)
		return m, nil

	case logErrorMsg:
		m.errorMsg = "Log fetch failed"
		m.errorExpiry = time.Now().Add(5 * time.Second)
		return m, nil

	case problemsLogBatchMsg:
		m.handleProblemsLogBatch(msg)
		return m, nil

	case problemsLogErrorMsg:
		m.errorMsg = "Problems fetch failed"
		m.errorExpiry = time.Now().Add(5 * time.Second)
		return m, nil
	}

	return m, nil
}

// View implements tea.Model.
func (m Model) View() tea.View {
	var v tea.View
	styles := m.theme.Styles()

	if !m.ready {
		v = tea.NewView(styles.AccentText.Render(m.spinnerGlyph()) + " " +
			styles.Text.Render("Starting flyer..."))
		v.AltScreen = true
		return v
	}

	// Modal overlays render centered over the dimmed main view (scrim).
	if m.activeModal != nil {
		v = tea.NewView(overlayCenter(m.renderMain(), m.activeModal.View(m.theme, m.width, m.height), m.width, m.height, styles))
		v.AltScreen = true
		return v
	}

	if m.showLogFilters {
		v = tea.NewView(overlayCenter(m.renderMain(), m.renderLogFilters(), m.width, m.height, styles))
		v.AltScreen = true
		return v
	}

	v = tea.NewView(m.renderMain())
	v.AltScreen = true
	return v
}

// handleKey processes keyboard input.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Handle active modal
	if m.activeModal != nil {
		modal, cmd, closed := m.activeModal.Update(msg, m.keys)
		if closed {
			m.activeModal = nil
		} else {
			m.activeModal = modal
		}
		return m, cmd
	}

	// Handle log filters modal
	if m.showLogFilters {
		return m.handleLogFiltersKey(msg)
	}

	// Log search input captures keys before global bindings ('q', 'd', ...).
	if m.logSearchCapturing() {
		return m.handleLogsKey(msg)
	}

	// Queue filter input captures keys the same way.
	if m.queueFilterCapturing() {
		return m.handleQueueFilterKey(msg)
	}

	// Global keys
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, m.keys.Help):
		m.activeModal = NewHelpModal(m.keys, m.helpContext())
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		return m, m.manualRefreshCmds()

	case key.Matches(msg, m.keys.CycleTheme):
		m.theme = GetTheme(NextTheme(m.theme.Name))
		if m.prefsPath != "" {
			_ = prefs.Save(m.prefsPath, prefs.Prefs{Theme: m.theme.Name})
		}
		m.updateInspectorViewport()
		m.updateLogViewport()
		return m, nil

	case key.Matches(msg, m.keys.ViewQueue):
		m.inspecting = false
		m.currentView = ViewQueue
		return m, nil

	case key.Matches(msg, m.keys.ViewDaemonLogs):
		m.inspecting = false
		return m.openDaemonLogs()

	case key.Matches(msg, m.keys.ViewProblems):
		m.inspecting = false
		m.currentView = ViewProblems
		m.clampProblemsRow()
		return m, nil
	}

	// Inspector captures the rest of the keys while open
	if m.inspecting {
		return m.handleInspectorKey(msg)
	}

	// View-specific keys
	switch m.currentView {
	case ViewQueue:
		return m.handleQueueKey(msg)
	case ViewLogs:
		return m.handleLogsKey(msg)
	case ViewProblems:
		return m.handleProblemsKey(msg)
	}

	return m, nil
}

// logSearchCapturing reports whether the log search input is consuming keys.
func (m Model) logSearchCapturing() bool {
	if !m.logState.searchActive {
		return false
	}
	if m.currentView == ViewLogs && !m.inspecting {
		return true
	}
	return m.inspecting && m.inspectorTab == tabLogs
}

// openDaemonLogs switches to the daemon log view.
func (m Model) openDaemonLogs() (tea.Model, tea.Cmd) {
	if m.logState.mode != logSourceDaemon {
		m.logState.mode = logSourceDaemon
		m.logState.rawLines = nil
		m.logState.streamCursor = 0
		m.clearLogSearch()
		m.logState.contentVersion++
	}
	m.currentView = ViewLogs
	m.updateLogViewport()
	return m, m.refreshLogs(nil)
}

// cycleFilter cycles through queue filter modes.
func (m *Model) cycleFilter() {
	switch m.filterMode {
	case FilterAll:
		m.filterMode = FilterFailed
	case FilterFailed:
		m.filterMode = FilterReview
	case FilterReview:
		m.filterMode = FilterProcessing
	default:
		m.filterMode = FilterAll
	}
}

// filterLabel returns the display label for the current filter mode.
func (m *Model) filterLabel() string {
	switch m.filterMode {
	case FilterFailed:
		return "Failed"
	case FilterReview:
		return "Review"
	case FilterProcessing:
		return "Active"
	default:
		return "All"
	}
}

// handleQueueKey processes keyboard input for the queue view.
func (m Model) handleQueueKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.CycleFilter):
		m.cycleFilter()
		m.updateQueueTable()
		return m, nil

	case key.Matches(msg, m.keys.Filter):
		m.queueFilterActive = true
		m.queueFilterInput.SetValue(m.queueFilterQuery)
		m.queueFilterInput.Focus()
		return m, nil

	case key.Matches(msg, m.keys.Escape):
		if m.queueFilterQuery != "" {
			m.clearQueueFilter()
		}
		return m, nil

	case key.Matches(msg, m.keys.Inspect):
		return m.openInspector(tabOverview)

	case key.Matches(msg, m.keys.InspectLogs):
		return m.openInspector(tabLogs)
	}

	items := m.getSortedItems()
	itemCount := len(items)
	if itemCount == 0 {
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Down):
		if m.selectedRow < itemCount-1 {
			m.selectedRow++
		}
	case key.Matches(msg, m.keys.Up):
		if m.selectedRow > 0 {
			m.selectedRow--
		}
	case key.Matches(msg, m.keys.Top):
		m.selectedRow = 0
	case key.Matches(msg, m.keys.Bottom):
		m.selectedRow = itemCount - 1
	}
	m.ensureQueueVisible()

	return m, nil
}

// handleTick processes the polling tick.
func (m Model) handleTick() (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Clear expired errors
	if m.errorMsg != "" && !m.errorExpiry.IsZero() && time.Now().After(m.errorExpiry) {
		m.errorMsg = ""
		m.errorExpiry = time.Time{}
	}

	// Fetch latest snapshot
	if m.store != nil {
		cmds = append(cmds, fetchSnapshotCmd(m.store))
	}

	// Skip log fetching when API is offline to reduce error noise
	if !m.snapshot.IsOffline() {
		// Daemon log view refresh while following
		if !m.inspecting && m.currentView == ViewLogs && m.logState.follow {
			if cmd := m.refreshLogs(nil); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

		// Inspector tabs with live fetches
		if m.inspecting {
			if item := m.getInspectedItem(); item != nil {
				switch m.inspectorTab {
				case tabLogs:
					if m.logState.follow {
						if cmd := m.refreshLogs(item); cmd != nil {
							cmds = append(cmds, cmd)
						}
					}
				case tabProblems:
					if cmd := m.refreshProblemsLogs(item); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}
	}

	// Schedule next tick
	cmds = append(cmds, tickCmd(m.pollTick))

	return m, tea.Batch(cmds...)
}

// renderMain renders the full UI: header, content, and the command bar as
// a footer key strip pinned to the bottom row of the terminal.
func (m Model) renderMain() string {
	var b strings.Builder

	// Header line 1: logo + status
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// NOW band: live resource occupancy (dashboard only)
	if m.currentView == ViewQueue && !m.inspecting {
		b.WriteString(m.renderNowBand())
		b.WriteString("\n")
	}

	// Main content
	b.WriteString(m.renderContent())

	body := b.String()
	if pad := m.height - 1 - lipgloss.Height(body); pad > 0 {
		body += strings.Repeat("\n", pad)
	}
	return body + "\n" + m.renderCommandBar()
}

// renderContent renders the main content area based on current view.
func (m Model) renderContent() string {
	if m.inspecting {
		return m.renderInspector()
	}
	switch m.currentView {
	case ViewQueue:
		return m.renderQueue()
	case ViewLogs:
		return m.renderLogs()
	case ViewProblems:
		return m.renderProblems()
	default:
		return ""
	}
}

// queueFilterCapturing reports whether the queue filter input is consuming keys.
func (m Model) queueFilterCapturing() bool {
	return m.queueFilterActive && m.currentView == ViewQueue && !m.inspecting
}

// handleQueueFilterKey handles keys while the queue filter input is active.
// The filter applies live as the query is typed.
func (m Model) handleQueueFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		m.queueFilterQuery = strings.TrimSpace(m.queueFilterInput.Value())
		m.queueFilterActive = false
		m.queueFilterInput.Blur()
		m.updateQueueTable()
		return m, nil

	case key.Matches(msg, m.keys.Escape):
		m.clearQueueFilter()
		return m, nil
	}

	var cmd tea.Cmd
	m.queueFilterInput, cmd = m.queueFilterInput.Update(msg)
	m.queueFilterQuery = strings.TrimSpace(m.queueFilterInput.Value())
	m.updateQueueTable()
	return m, cmd
}

// clearQueueFilter drops the queue text filter entirely.
func (m *Model) clearQueueFilter() {
	m.queueFilterActive = false
	m.queueFilterQuery = ""
	m.queueFilterInput.SetValue("")
	m.queueFilterInput.Blur()
	m.updateQueueTable()
}

// helpContext names the help section for the surface the user is on, so the
// help modal can list it first.
func (m Model) helpContext() string {
	if m.inspecting {
		if m.inspectorTab == tabLogs {
			return "Logs"
		}
		return "Inspector"
	}
	switch m.currentView {
	case ViewLogs:
		return "Logs"
	case ViewProblems:
		return "Views"
	default:
		return "Queue"
	}
}

// spinnerFrames animate the connecting/offline indicator.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerGlyph returns the current spinner frame.
func (m Model) spinnerGlyph() string {
	return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
}

// spinnerActive reports whether the spinner should keep animating: while
// starting up, or whenever the daemon is unreachable.
func (m Model) spinnerActive() bool {
	return !m.ready || !m.snapshot.HasStatus || m.snapshot.IsOffline()
}

// manualRefreshCmds forces an immediate API poll plus a log refresh when a
// log surface is visible.
func (m Model) manualRefreshCmds() tea.Cmd {
	refreshFn, store := m.refreshFn, m.store
	cmds := []tea.Cmd{func() tea.Msg {
		if refreshFn != nil {
			_ = refreshFn()
		}
		if store != nil {
			return snapshotMsg(store.Snapshot())
		}
		return nil
	}}

	if m.inspecting && m.inspectorTab == tabLogs {
		if cmd := m.refreshLogs(m.getInspectedItem()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if !m.inspecting && m.currentView == ViewLogs {
		if cmd := m.refreshLogs(nil); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// Messages

type tickMsg time.Time

type spinnerTickMsg struct{}

type snapshotMsg state.Snapshot

// Commands

func tickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func fetchSnapshotCmd(store *state.Store) tea.Cmd {
	return func() tea.Msg {
		return snapshotMsg(store.Snapshot())
	}
}

// Run starts the Bubble Tea program.
func Run(opts Options) error {
	m := New(opts)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
