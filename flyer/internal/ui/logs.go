package ui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// Log source modes
type logSource int

const (
	logSourceDaemon logSource = iota
	logSourceItem
)

// Log refresh constants
const (
	logRefreshInterval = 2 * time.Second
	logFetchTimeout    = 5 * time.Second
	logFetchLimit      = 100
	logBufferLimit     = 2000
)

// logState holds all log-related state.
type logState struct {
	mode logSource
	// rawLines holds the structured log events backing the current view, one
	// entry per displayed block (a block may span multiple visual rows via
	// Fields). formatLogEvent derives the plain text form on demand for
	// search matching and copy.
	rawLines    []spindle.LogEvent
	follow      bool
	lastRefresh time.Time
	fetchError  error
	loaded      bool
	generation  uint64

	// Cursors for incremental fetching
	streamCursor uint64
	itemCursor   uint64 // Changed to uint64 for /api/logs cursor
	lastItemID   int64  // Track which item the cursor belongs to

	// Filters (apply to both daemon and item logs via /api/logs)
	filterLevel   string
	filterStage   string
	filterAsset   string
	filterTask    string
	filterAttempt string

	// Search
	searchActive   bool
	searchQuery    string
	searchRegex    *regexp.Regexp
	searchInput    textinput.Model
	searchMatches  []int // Event indices that match
	searchMatchIdx int   // Current match index
	searchErr      string

	// showFields expands each event's structured fields below it; the
	// default is one row per event. eventLines maps an event index to its
	// first rendered line, so search can scroll to it in either mode.
	showFields bool
	eventLines []int

	// Content caching - skip re-render when unchanged
	contentVersion uint64
	lastRendered   uint64
}

// initLogState initializes the log state.
func (m *Model) initLogState() {
	ti := textinput.New()
	ti.Placeholder = "Search logs..."
	ti.CharLimit = 100

	m.logState = logState{
		mode:           logSourceDaemon,
		follow:         true,
		contentVersion: 1,      // Start at 1 so first increment (to 2) differs from initial render (lastRendered=1)
		filterLevel:    "info", // Default to INFO to hide DEBUG noise
	}
	m.logState.searchInput = ti
}

// logViewportHeight returns the panel interior height for log lines. The
// daemon view surrounds the panel with header band, status line, and footer
// band; the inspector logs tab adds its item and tab bands.
func (m *Model) logViewportHeight() int {
	if m.inspecting {
		return max(m.height-7, 1)
	}
	return max(m.height-5, 1)
}

// initLogViewport initializes the log viewport.
func (m *Model) initLogViewport() {
	m.logViewport = viewport.New(
		viewport.WithWidth(panelInnerWidth(m.width)),
		viewport.WithHeight(m.logViewportHeight()),
	)
	m.logViewport.Style = lipgloss.NewStyle()
}

// updateLogViewport updates the log viewport with current content.
func (m *Model) updateLogViewport() {
	if m.logViewport.Width() == 0 {
		m.initLogViewport()
	}

	m.logViewport.SetWidth(panelInnerWidth(m.width))
	m.logViewport.SetHeight(m.logViewportHeight())
	m.logViewport.Style = lipgloss.NewStyle()

	// Only re-render content if it changed (version mismatch or first render)
	if m.logState.lastRendered == 0 || m.logState.contentVersion != m.logState.lastRendered {
		content := m.renderLogContent()
		m.logViewport.SetContent(content)
		m.logState.lastRendered = m.logState.contentVersion
		if m.logState.lastRendered == 0 {
			m.logState.lastRendered = 1 // Mark as rendered at least once
		}
	}

	// Auto-scroll if following
	if m.logState.follow {
		m.logViewport.GotoBottom()
	}
}

// renderLogs renders the log view as a Level 1 panel with a status line.
func (m Model) renderLogs() string {
	styles := m.theme.Styles()
	panel := renderPanel(m.getLogTitle(), m.logViewport.View(), "", m.width, styles)
	return panel + "\n" + m.renderLogStatus(styles)
}

// getLogTitle returns the plain text title for the log view. This view now
// only ever shows the daemon log; item logs are rendered by the per-item
// inspector instead.
func (m Model) getLogTitle() string {
	if m.logFiltersActive() {
		return "Daemon Log (filtered)"
	}
	return "Daemon Log"
}

// renderLogStatus renders the log status bar.
func (m *Model) renderLogStatus(styles Styles) string {
	// The status is one fixed row of chrome; never let it wrap.
	fit := func(line string) string { return ansi.Truncate(line, m.width, "…") }
	if m.logState.fetchError != nil {
		return fit(styles.WarningText.Render("Log fetch failed; retained buffer stale: " + m.logState.fetchError.Error()))
	}
	if !m.logState.loaded && len(m.logState.rawLines) == 0 {
		return styles.MutedText.Render("Loading log window")
	}
	sep := " " + styles.FaintText.Render("·") + " "
	if m.logState.searchRegex != nil {
		if len(m.logState.searchMatches) == 0 {
			return fit(styles.DangerText.Render("Pattern not found: " + m.logState.searchQuery))
		}
		return fit(styles.AccentText.Render("/"+m.logState.searchQuery) + sep +
			styles.WarningText.Render(fmt.Sprintf("%d/%d", m.logState.searchMatchIdx+1, len(m.logState.searchMatches))) + sep +
			styles.AccentText.Render("n/N") + styles.FaintText.Render(" next/prev") + sep +
			styles.AccentText.Render("Esc") + styles.FaintText.Render(" clear"))
	}

	src := "Daemon log"
	if m.logState.mode == logSourceItem {
		src = "Item log"
		if m.logState.lastItemID > 0 {
			src = fmt.Sprintf("Item #%d log", m.logState.lastItemID)
		}
	}
	tail := "following"
	if !m.logState.follow {
		tail = "paused"
		// Scroll position while paused, so "where am I" stays visible.
		if m.logViewport.TotalLineCount() > m.logViewport.VisibleLineCount() {
			tail += fmt.Sprintf(" at %d%%", int(m.logViewport.ScrollPercent()*100))
		}
	}
	lines := fmt.Sprintf("%d events", len(m.logState.rawLines))
	if len(m.logState.rawLines) >= logBufferLimit {
		lines = fmt.Sprintf("last %d events", logBufferLimit)
	}
	parts := []string{
		styles.MutedText.Render(src),
		styles.FaintText.Render(lines),
		styles.FaintText.Render(tail),
	}

	// Search input mode
	if m.logState.searchActive {
		parts = append(parts, styles.AccentText.Render("search: "+m.logState.searchInput.Value()))
		if m.logState.searchErr != "" {
			parts = append(parts, styles.DangerText.Render("invalid pattern: "+m.logState.searchErr))
		}
	}

	// Filters, including the default level, so the window is never unexplained.
	var filterParts []string
	for _, f := range []struct{ name, value string }{
		{"level", m.logState.filterLevel}, {"stage", m.logState.filterStage}, {"asset", m.logState.filterAsset},
		{"task", m.logState.filterTask}, {"attempt", m.logState.filterAttempt},
	} {
		if f.value != "" {
			filterParts = append(filterParts, f.name+"="+f.value)
		}
	}
	if len(filterParts) > 0 {
		parts = append(parts, styles.MutedText.Render(strings.Join(filterParts, " ")))
	}

	return fit(strings.Join(parts, sep))
}

// renderLogContent renders the colorized log lines.
func (m *Model) renderLogContent() string {
	styles := m.theme.Styles()

	if len(m.logState.rawLines) == 0 {
		return styles.MutedText.Render("No log entries")
	}

	// Build a set of matching line indices for quick lookup
	matchSet := make(map[int]bool)
	for _, idx := range m.logState.searchMatches {
		matchSet[idx] = true
	}
	activeMatchLine := -1
	if len(m.logState.searchMatches) > 0 && m.logState.searchMatchIdx < len(m.logState.searchMatches) {
		activeMatchLine = m.logState.searchMatches[m.logState.searchMatchIdx]
	}

	var b strings.Builder
	m.logState.eventLines = m.logState.eventLines[:0]
	line := 0
	width := panelInnerWidth(m.width)

	for i, evt := range m.logState.rawLines {
		m.logState.eventLines = append(m.logState.eventLines, line)

		var lineContent string
		switch {
		case i == activeMatchLine:
			// Active match: the whole row on the warning background.
			lineContent = m.colorizeLineForSearch(ansi.Truncate(formatLogEvent(evt), width, "…"), m.theme.Warning)
		case matchSet[i]:
			lineContent = m.colorizeLineWithHighlight(ansi.Truncate(formatLogEvent(evt), width, "…"), styles)
		default:
			lineContent = m.styleLogEvent(evt, styles, false, m.logState.showFields, width)
		}
		line += strings.Count(lineContent, "\n") + 1

		b.WriteString(lineContent)
		if i < len(m.logState.rawLines)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// colorizeLineForSearch renders a line with search highlight background.
func (m *Model) colorizeLineForSearch(line string, bgColor string) string {
	style := lipgloss.NewStyle().
		Background(lipgloss.Color(bgColor)).
		Foreground(lipgloss.Color(m.theme.Background))
	return style.Render(line)
}

// colorizeLineWithHighlight renders a line with accent foreground for passive matches.
func (m *Model) colorizeLineWithHighlight(line string, styles Styles) string {
	return styles.AccentText.Render(line)
}

// styleLogEvent builds the styled log line directly from the structured
// LogEvent fields: timestamp muted, level colored by severity, the item/stage
// subject highlighted, and the message in normal text.
//
// Without fields the event is one row cut to width, with the decision
// result and error hint (the fields that answer "what happened") inline.
// With fields, every structured field follows on its own row, wrapped
// under its value.
//
// highlightErrorHint makes the error_hint field (when present) stand out
// with the warning/danger style, matching the level's severity. The daemon
// log view passes false; the problems view -- where error_hint is the most
// direct answer to "what broke" -- passes true.
func (m *Model) styleLogEvent(evt spindle.LogEvent, styles Styles, highlightErrorHint, fields bool, width int) string {
	level := strings.ToUpper(strings.TrimSpace(evt.Level))

	var result strings.Builder
	result.WriteString(styles.FaintText.Render(logEventTimestamp(evt)))
	result.WriteString(" ")
	result.WriteString(m.getLevelStyle(level, styles).Bold(true).Render(level))

	// Inside the inspector every event belongs to the inspected item.
	itemID := evt.ItemID
	if m.inspecting {
		itemID = 0
	}
	if subject := composeLogSubject(itemID, evt.Stage); subject != "" {
		result.WriteString(" ")
		result.WriteString(styles.AccentText.Render(subject))
	}

	if message := strings.TrimSpace(evt.Message); message != "" {
		result.WriteString(" ")
		result.WriteString(styles.FaintText.Render("–"))
		result.WriteString(" ")
		result.WriteString(styles.Text.Render(message))
	}

	if !fields {
		// Skip a result the message already states ("stage started").
		if r := strings.TrimSpace(evt.Fields["decision_result"]); r != "" && !strings.Contains(strings.ToLower(evt.Message), strings.ToLower(r)) {
			result.WriteString(styles.FaintText.Render(" → ") + styles.AccentText.Bold(true).Render(r))
		}
		if hint := strings.TrimSpace(evt.Fields["error_hint"]); hint != "" {
			result.WriteString(styles.FaintText.Render(" · ") + m.getLevelStyle(level, styles).Render(hint))
		}
		return ansi.Truncate(result.String(), width, "…")
	}

	for _, key := range orderedFieldKeys(evt.Fields) {
		value := strings.TrimSpace(evt.Fields[key])
		if value == "" {
			continue
		}
		for _, row := range wrapText(styleLogFieldRow(key, value, styles, level, highlightErrorHint), width) {
			result.WriteString("\n")
			if !strings.HasPrefix(ansi.Strip(row), "    - ") {
				row = "      " + row // continuation under the value
			}
			result.WriteString(row)
		}
	}

	return result.String()
}

// knownLogFieldOrder is the priority order for well-known structured log
// fields; any remaining keys in a LogEvent's Fields map are appended after
// these, sorted alphabetically.
var knownLogFieldOrder = []string{
	"decision_type", "decision_result", "decision_reason",
	"event_type", "error_hint", "impact", "stage_duration",
}

// orderedFieldKeys returns fields' keys in display order: the known keys
// above (only when present), then any remaining keys sorted alphabetically.
func orderedFieldKeys(fields map[string]string) []string {
	if len(fields) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(fields))
	keys := make([]string, 0, len(fields))
	for _, key := range knownLogFieldOrder {
		if _, ok := fields[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0, len(fields)-len(keys))
	for key := range fields {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// styleLogFieldRow renders one "    - key: value" row for a structured log
// field. decision_* fields get a distinct accent treatment (label accented,
// decision_result value bold-accented) so the decision they represent stands
// out from plain diagnostic fields. When highlightErrorHint is set, the
// error_hint field is rendered in the warning or danger style matching the
// event's level, so it stands out as the direct answer to "what broke".
func styleLogFieldRow(key, value string, styles Styles, level string, highlightErrorHint bool) string {
	labelStyle := styles.Text
	valueStyle := styles.Text
	if strings.HasPrefix(key, "decision_") {
		labelStyle = styles.AccentText
	}
	if key == "decision_result" {
		valueStyle = styles.AccentText.Bold(true)
	}
	if highlightErrorHint && key == "error_hint" {
		hint := styles.WarningText
		if level == "ERROR" {
			hint = styles.DangerText
		}
		labelStyle = hint
		valueStyle = hint.Bold(true)
	}
	return fmt.Sprintf("    - %s: %s", labelStyle.Render(key), valueStyle.Render(value))
}

// getLevelStyle returns the style for a log level.
func (m *Model) getLevelStyle(level string, styles Styles) lipgloss.Style {
	switch level {
	case "INFO":
		return styles.SuccessText
	case "WARN":
		return styles.WarningText
	case "ERROR":
		return styles.DangerText
	case "DEBUG":
		return styles.InfoText
	default:
		return styles.Text
	}
}

// logFiltersActive reports whether any filter narrows the log beyond the
// default INFO level.
func (m *Model) logFiltersActive() bool {
	return (m.logState.filterLevel != "" && !strings.EqualFold(m.logState.filterLevel, "info")) || m.logState.filterStage != "" || m.logState.filterAsset != "" || m.logState.filterTask != "" || m.logState.filterAttempt != ""
}

// handleLogsKey processes keyboard input for logs view.
func (m Model) handleLogsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Handle search input mode
	if m.logState.searchActive {
		return m.handleLogSearchInput(msg)
	}

	switch {
	case key.Matches(msg, m.keys.ToggleFollow):
		m.logState.follow = !m.logState.follow
		if m.logState.follow {
			m.logViewport.GotoBottom()
		}
		m.updateLogViewport()
		return m, nil

	case key.Matches(msg, m.keys.Search):
		m.logState.searchActive = true
		m.logState.searchInput.Focus()
		m.logState.searchInput.SetValue("")
		return m, nil

	case key.Matches(msg, m.keys.LogFilters):
		m.openLogFilters()
		return m, nil

	case key.Matches(msg, m.keys.ToggleDetails):
		m.logState.showFields = !m.logState.showFields
		m.logState.contentVersion++
		m.updateLogViewport()
		return m, nil

	case key.Matches(msg, m.keys.NextMatch):
		m.nextSearchMatch()
		return m, nil

	case key.Matches(msg, m.keys.PrevMatch):
		m.previousSearchMatch()
		return m, nil

	case key.Matches(msg, m.keys.Escape):
		// Clear search if active, otherwise return to the queue
		if m.logState.searchRegex != nil {
			m.clearLogSearch()
			m.updateLogViewport()
			return m, nil
		}
		if !m.inspecting {
			m.currentView = ViewQueue
		}
		return m, nil

	case key.Matches(msg, m.keys.Top):
		m.logViewport.GotoTop()
		m.logState.follow = false
		return m, nil

	case key.Matches(msg, m.keys.Bottom):
		m.logViewport.GotoBottom()
		m.logState.follow = true
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.logViewport.ScrollDown(1)
		m.logState.follow = false
		return m, nil

	case key.Matches(msg, m.keys.Up):
		m.logViewport.ScrollUp(1)
		m.logState.follow = false
		return m, nil

	case key.Matches(msg, m.keys.HalfPageDown):
		m.logViewport.HalfPageDown()
		m.logState.follow = false
		return m, nil

	case key.Matches(msg, m.keys.HalfPageUp):
		m.logViewport.HalfPageUp()
		m.logState.follow = false
		return m, nil

	case key.Matches(msg, m.keys.PageDown):
		m.logViewport.PageDown()
		m.logState.follow = false
		return m, nil

	case key.Matches(msg, m.keys.PageUp):
		m.logViewport.PageUp()
		m.logState.follow = false
		return m, nil
	}

	return m, nil
}

// handleLogSearchInput handles keyboard input during log search.
func (m *Model) handleLogSearchInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		// Apply search
		query := m.logState.searchInput.Value()
		if query == "" {
			m.logState.searchActive = false
			m.logState.searchInput.Blur()
			return m, nil
		}

		re, err := regexp.Compile("(?i)" + query)
		if err != nil {
			// Stay in search mode and say why Enter did nothing.
			m.logState.searchErr = err.Error()
			return m, nil
		}
		m.logState.searchErr = ""

		m.logState.searchRegex = re
		m.logState.searchQuery = query
		m.logState.searchActive = false
		m.logState.searchInput.Blur()

		// Find all matches
		m.findSearchMatches()

		// If matches found, scroll to first one
		if len(m.logState.searchMatches) > 0 {
			m.logState.searchMatchIdx = 0
			m.scrollToSearchMatch()
		}

		m.updateLogViewport()
		return m, nil

	case key.Matches(msg, m.keys.Escape):
		// Cancel search input
		m.logState.searchErr = ""
		m.logState.searchActive = false
		m.logState.searchInput.Blur()
		m.logState.searchInput.SetValue("")
		return m, nil
	}

	// Let the text input handle the key
	var cmd tea.Cmd
	m.logState.searchErr = ""
	m.logState.searchInput, cmd = m.logState.searchInput.Update(msg)
	return m, cmd
}

// clearLogSearch clears the search state.
func (m *Model) clearLogSearch() {
	m.logState.searchRegex = nil
	m.logState.searchQuery = ""
	m.logState.searchMatches = nil
	m.logState.searchMatchIdx = 0
	m.logState.contentVersion++ // Search highlighting changed
}

// findSearchMatches finds all lines matching the current search regex.
func (m *Model) findSearchMatches() {
	m.logState.searchMatches = nil
	if m.logState.searchRegex == nil {
		return
	}

	for i, evt := range m.logState.rawLines {
		if m.logState.searchRegex.MatchString(formatLogEvent(evt)) {
			m.logState.searchMatches = append(m.logState.searchMatches, i)
		}
	}
	m.logState.contentVersion++ // Search highlighting changed
}

// nextSearchMatch moves to the next search match.
func (m *Model) nextSearchMatch() {
	if len(m.logState.searchMatches) == 0 {
		return
	}

	m.logState.searchMatchIdx = (m.logState.searchMatchIdx + 1) % len(m.logState.searchMatches)
	m.logState.contentVersion++ // Active match changed
	m.scrollToSearchMatch()
	m.updateLogViewport()
}

// previousSearchMatch moves to the previous search match.
func (m *Model) previousSearchMatch() {
	if len(m.logState.searchMatches) == 0 {
		return
	}

	m.logState.searchMatchIdx = (m.logState.searchMatchIdx - 1 + len(m.logState.searchMatches)) % len(m.logState.searchMatches)
	m.logState.contentVersion++ // Active match changed
	m.scrollToSearchMatch()
	m.updateLogViewport()
}

// scrollToSearchMatch scrolls the viewport to show the current match.
func (m *Model) scrollToSearchMatch() {
	if len(m.logState.searchMatches) == 0 || m.logState.searchMatchIdx >= len(m.logState.searchMatches) {
		return
	}

	targetLine := m.logState.searchMatches[m.logState.searchMatchIdx]
	if targetLine < len(m.logState.eventLines) {
		targetLine = m.logState.eventLines[targetLine]
	}
	m.logState.follow = false

	// Calculate scroll position to center the match if possible
	viewportHeight := m.logViewport.Height()
	scrollTo := max(targetLine-viewportHeight/2, 0)
	m.logViewport.SetYOffset(scrollTo)
}

// refreshLogs fetches new log entries from the API. In daemon mode item is
// ignored; in item mode it identifies the item whose logs to fetch (the
// per-item inspector is the caller in that case).
func (m *Model) refreshLogs(item *spindle.QueueItem) tea.Cmd {
	if m.client == nil {
		return nil
	}

	// Skip when API is offline to reduce error noise
	if m.snapshot.IsOffline() {
		return nil
	}

	// Don't refresh too frequently
	if time.Since(m.logState.lastRefresh) < logRefreshInterval {
		return nil
	}
	m.logState.lastRefresh = time.Now()

	switch m.logState.mode {
	case logSourceItem:
		return m.fetchItemLogs(item)
	default:
		return m.fetchDaemonLogs()
	}
}

// fetchDaemonLogs fetches daemon logs from the API.
func (m *Model) fetchDaemonLogs() tea.Cmd {
	return m.fetchLogs(logSourceDaemon, 0, m.logState.streamCursor)
}

// fetchItemLogs fetches item-specific logs from the streaming API.
// Uses /api/logs with item filter for structured log events.
func (m *Model) fetchItemLogs(item *spindle.QueueItem) tea.Cmd {
	if item == nil {
		return nil
	}
	itemID := item.ID

	// Reset cursor and buffer when switching to a different item
	if itemID != m.logState.lastItemID {
		m.logState.itemCursor = 0
		m.logState.rawLines = nil
		m.logState.lastItemID = itemID
		m.logState.loaded, m.logState.fetchError = false, nil
		m.clearLogSearch()
		m.logState.contentVersion++
	}

	return m.fetchLogs(logSourceItem, itemID, m.logState.itemCursor)
}

func (m *Model) fetchLogs(source logSource, itemID int64, cursor uint64) tea.Cmd {
	s := m.logState
	query := spindle.LogQuery{Since: cursor, Limit: logFetchLimit, Tail: cursor == 0, ItemID: itemID, DaemonOnly: source == logSourceDaemon,
		Level: s.filterLevel, Stage: s.filterStage, Asset: s.filterAsset, TaskID: s.filterTask, Attempt: s.filterAttempt}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), logFetchTimeout)
		defer cancel()
		batch, err := client.FetchLogs(ctx, query)
		if err != nil {
			return logErrorMsg{err: err, source: source, itemID: itemID, generation: s.generation}
		}
		return logBatchMsg{events: batch.Events, next: batch.Next, source: source, itemID: itemID, generation: s.generation}
	}
}

// Log messages

type logBatchMsg struct {
	generation uint64
	events     []spindle.LogEvent
	next       uint64
	source     logSource
	itemID     int64 // For item logs, tracks which item this is for
}

type logErrorMsg struct {
	generation uint64
	err        error
	itemID     int64
	source     logSource
}

// handleLogBatch processes a batch of log events from the streaming API.
func (m *Model) handleLogBatch(msg logBatchMsg) {
	if msg.source != m.logState.mode || msg.generation != m.logState.generation {
		return
	}

	// For item logs, verify we're still looking at the same item
	if msg.source == logSourceItem {
		if msg.itemID != m.logState.lastItemID {
			return
		}
		m.logState.itemCursor = msg.next
	} else {
		m.logState.streamCursor = msg.next
	}

	m.logState.loaded, m.logState.fetchError = true, nil
	// Guard against duplicate/overlapping batches: only append events whose
	// Seq is strictly greater than the last one already appended. rawLines
	// already tracks the active mode's events (cleared on item switch), so
	// its last entry's Sequence doubles as the dedup cursor without needing
	// a separate field.
	var lastSeq uint64
	if n := len(m.logState.rawLines); n > 0 {
		lastSeq = m.logState.rawLines[n-1].Sequence
	}
	var newEvents []spindle.LogEvent
	for _, evt := range msg.events {
		if evt.Sequence <= lastSeq {
			continue
		}
		newEvents = append(newEvents, evt)
		lastSeq = evt.Sequence
	}

	if len(newEvents) > 0 {
		m.logState.rawLines = append(m.logState.rawLines, newEvents...)
		m.logState.rawLines = trimLogBuffer(m.logState.rawLines, logBufferLimit)
		m.logState.contentVersion++ // Mark content changed
		m.updateLogViewport()
	}
}

// logEventTimestamp formats an event's local time, dropping the date for
// today's events (as the Events tab does), and falls back to the raw string.
func logEventTimestamp(evt spindle.LogEvent) string {
	parsed := evt.ParsedTime()
	if parsed.IsZero() {
		return evt.Timestamp
	}
	local, now := parsed.In(time.Local), time.Now()
	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
		return local.Format("15:04:05")
	}
	return local.Format("Jan 02 15:04:05")
}

// formatLogEvent formats a single log event.
func formatLogEvent(evt spindle.LogEvent) string {
	ts := logEventTimestamp(evt)
	level := strings.ToUpper(strings.TrimSpace(evt.Level))
	parts := []string{ts, level}
	subject := composeLogSubject(evt.ItemID, evt.Stage)
	header := strings.Join(parts, " ")
	if subject != "" {
		header += " " + subject
	}
	message := strings.TrimSpace(evt.Message)
	if message != "" {
		header += " – " + message
	}

	var fieldParts []string
	for _, key := range orderedFieldKeys(evt.Fields) {
		value := strings.TrimSpace(evt.Fields[key])
		if value == "" {
			continue
		}
		fieldParts = append(fieldParts, fmt.Sprintf("%s=%s", key, value))
	}
	if len(fieldParts) == 0 {
		return header
	}
	return header + " " + strings.Join(fieldParts, " ")
}

// composeLogSubject creates the subject line for a log event.
func composeLogSubject(itemID int64, stage string) string {
	stage = strings.TrimSpace(stage)
	switch {
	case itemID > 0 && stage != "":
		return fmt.Sprintf("ID #%d (%s)", itemID, stage)
	case itemID > 0:
		return fmt.Sprintf("ID #%d", itemID)
	default:
		return stage
	}
}

// trimLogBuffer trims the log buffer to the limit by removing oldest entries.
func trimLogBuffer[T any](lines []T, limit int) []T {
	if overflow := len(lines) - limit; overflow > 0 {
		return append([]T(nil), lines[overflow:]...)
	}
	return lines
}

// --- Log Filters Modal ---

// initLogFilterInputs initializes the text inputs for log filters.
func (m *Model) initLogFilterInputs() {
	// Level input
	levelInput := textinput.New()
	levelInput.Placeholder = "e.g. error, warn, info"
	levelInput.CharLimit = 20
	levelInput.SetWidth(30)

	m.logFilterInputs[0] = levelInput
	for i, placeholder := range []string{"stage (e.g. encoding, subtitling)", "asset key (e.g. main)", "task ID from Events", "attempt number"} {
		input := textinput.New()
		input.Placeholder = placeholder
		input.CharLimit = 80
		input.SetWidth(30)
		m.logFilterInputs[i+1] = input
	}
}

// openLogFilters opens the log filters modal.
func (m *Model) openLogFilters() {
	// Pre-fill with current filter values
	m.logFilterInputs[0].SetValue(m.logState.filterLevel)
	for i, value := range []string{m.logState.filterStage, m.logState.filterAsset, m.logState.filterTask, m.logState.filterAttempt} {
		m.logFilterInputs[i+1].SetValue(value)
		m.logFilterInputs[i+1].Blur()
	}
	m.logFilterFocusIdx = 0
	m.logFilterInputs[0].Focus()
	m.showLogFilters = true
}

// handleLogFiltersKey handles keyboard input for the log filters modal.
func (m Model) handleLogFiltersKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Escape):
		// Cancel and close modal
		m.showLogFilters = false
		return m, nil

	case key.Matches(msg, m.keys.Confirm):
		// Apply filters and close
		m.applyLogFilters()
		m.showLogFilters = false
		return m, nil

	case key.Matches(msg, m.keys.Tab), key.Matches(msg, m.keys.Down):
		// Move to next field
		m.logFilterInputs[m.logFilterFocusIdx].Blur()
		m.logFilterFocusIdx = (m.logFilterFocusIdx + 1) % len(m.logFilterInputs)
		m.logFilterInputs[m.logFilterFocusIdx].Focus()
		return m, nil

	case key.Matches(msg, m.keys.ShiftTab), key.Matches(msg, m.keys.Up):
		// Move to previous field
		m.logFilterInputs[m.logFilterFocusIdx].Blur()
		m.logFilterFocusIdx = (m.logFilterFocusIdx - 1 + len(m.logFilterInputs)) % len(m.logFilterInputs)
		m.logFilterInputs[m.logFilterFocusIdx].Focus()
		return m, nil

	case msg.String() == "ctrl+c":
		return m, tea.Quit

	case msg.String() == "ctrl+x":
		// Clear all filters
		for i := range m.logFilterInputs {
			m.logFilterInputs[i].SetValue("")
		}
		return m, nil
	}

	// Let the focused input handle the key
	var cmd tea.Cmd
	m.logFilterInputs[m.logFilterFocusIdx], cmd = m.logFilterInputs[m.logFilterFocusIdx].Update(msg)
	return m, cmd
}

// applyLogFilters applies the filter values from the modal.
func (m *Model) applyLogFilters() {
	m.logState.filterLevel = strings.TrimSpace(m.logFilterInputs[0].Value())
	m.logState.filterStage = strings.TrimSpace(m.logFilterInputs[1].Value())
	m.logState.filterAsset = strings.TrimSpace(m.logFilterInputs[2].Value())
	m.logState.filterTask = strings.TrimSpace(m.logFilterInputs[3].Value())
	m.logState.filterAttempt = strings.TrimSpace(m.logFilterInputs[4].Value())
	m.logState.loaded, m.logState.fetchError = false, nil
	m.logState.generation++
	m.logState.contentVersion++

	// Reset log buffer to fetch with new filters
	m.logState.rawLines = nil
	m.logState.streamCursor = 0
	m.logState.itemCursor = 0
	m.clearLogSearch()
}

// renderLogFilters renders the log filters modal.
func (m Model) renderLogFilters() string {
	styles := m.theme.Styles()

	var b strings.Builder

	// Title
	title := styles.Text.Bold(true).Render("Log Filters")
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(styles.FaintText.Render(strings.Repeat("─", 40)))
	b.WriteString("\n\n")

	// Filter fields
	fields := []struct {
		label string
		index int
	}{
		{"Level:   ", 0}, {"Stage:   ", 1}, {"Asset:   ", 2}, {"Task:    ", 3}, {"Attempt: ", 4},
	}
	for _, f := range fields {
		label := f.label
		if m.logFilterFocusIdx == f.index {
			label = styles.AccentText.Render(label)
		} else {
			label = styles.MutedText.Render(label)
		}
		b.WriteString(label)
		b.WriteString(m.logFilterInputs[f.index].View())
		b.WriteString("\n")
	}

	// Buttons hint
	b.WriteString("\n")
	b.WriteString(styles.FaintText.Render("Enter apply · Esc cancel · Ctrl+X clear"))

	// Build the modal box; placement over the dimmed backdrop happens in
	// View().
	content := b.String()

	// Level 4 modal: double-line border per the guide's elevation model.
	modalWidth := 50
	modal := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color(m.theme.Accent)).
		Padding(1, 2).
		Width(modalWidth)

	return modal.Render(content)
}
