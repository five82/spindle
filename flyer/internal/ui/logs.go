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

	"github.com/five82/flyer/internal/spindle"
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

	// Cursors for incremental fetching
	streamCursor uint64
	itemCursor   uint64 // Changed to uint64 for /api/logs cursor
	lastItemID   int64  // Track which item the cursor belongs to

	// Filters (apply to both daemon and item logs via /api/logs)
	filterLevel     string
	filterComponent string
	filterLane      string
	filterRequest   string

	// Search
	searchActive   bool
	searchQuery    string
	searchRegex    *regexp.Regexp
	searchInput    textinput.Model
	searchMatches  []int // Line indices that match
	searchMatchIdx int   // Current match index

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
	// If we have an active search with matches, show search status instead
	if m.logState.searchRegex != nil && len(m.logState.searchMatches) > 0 {
		matchNum := m.logState.searchMatchIdx + 1
		totalMatches := len(m.logState.searchMatches)
		return styles.AccentText.Render(fmt.Sprintf("/%s", m.logState.searchQuery)) +
			styles.FaintText.Render(" - ") +
			styles.WarningText.Render(fmt.Sprintf("%d/%d", matchNum, totalMatches)) +
			styles.FaintText.Render(" - Press ") +
			styles.AccentText.Render("n") +
			styles.FaintText.Render(" for next, ") +
			styles.AccentText.Render("N") +
			styles.FaintText.Render(" for previous, ") +
			styles.AccentText.Render("Esc") +
			styles.FaintText.Render(" to clear")
	}

	// If search regex exists but no matches
	if m.logState.searchRegex != nil && len(m.logState.searchMatches) == 0 {
		return styles.DangerText.Render("Pattern not found: " + m.logState.searchQuery)
	}

	// Source label
	var src, apiPath string
	switch m.logState.mode {
	case logSourceItem:
		src = "Item"
		if m.logState.lastItemID > 0 {
			apiPath = fmt.Sprintf("api logs item=%d", m.logState.lastItemID)
		} else {
			apiPath = "api logs"
		}
	default:
		src = "Daemon"
		apiPath = "api logs"
	}

	// Build status: "Item log 341 lines auto-tail on"
	autoTail := "off"
	if m.logState.follow {
		autoTail = "on"
	}
	status := fmt.Sprintf("%s log %d lines auto-tail %s", src, len(m.logState.rawLines), autoTail)

	var parts []string
	parts = append(parts, styles.FaintText.Render(status))

	// Scroll position while not following, so "where am I" stays visible.
	if !m.logState.follow && m.logViewport.TotalLineCount() > m.logViewport.VisibleLineCount() {
		parts = append(parts, styles.MutedText.Render(fmt.Sprintf("%d%%", int(m.logViewport.ScrollPercent()*100))))
	}

	// Search input mode
	if m.logState.searchActive {
		parts = append(parts, styles.AccentText.Render("search: "+m.logState.searchInput.Value()))
	}

	// Filters
	if m.logFiltersActive() {
		var filterParts []string
		if m.logState.filterLevel != "" {
			filterParts = append(filterParts, "level="+m.logState.filterLevel)
		}
		if m.logState.filterComponent != "" {
			filterParts = append(filterParts, "comp="+m.logState.filterComponent)
		}
		if m.logState.filterLane != "" {
			filterParts = append(filterParts, "lane="+m.logState.filterLane)
		}
		if m.logState.filterRequest != "" {
			filterParts = append(filterParts, "req="+m.logState.filterRequest)
		}
		if len(filterParts) > 0 {
			parts = append(parts, styles.MutedText.Render("filter: "+strings.Join(filterParts, " ")))
		}
	}

	// API path at the end
	if apiPath != "" {
		parts = append(parts, styles.AccentText.Render(apiPath))
	}

	// Join with styled bullet separator
	sep := " " + styles.FaintText.Render("•") + " "
	return strings.Join(parts, sep)
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

	for i, evt := range m.logState.rawLines {
		lineNum := i + 1

		// Determine if this line is a search match
		isActiveMatch := i == activeMatchLine
		isPassiveMatch := matchSet[i] && !isActiveMatch

		// Build line content: line number + styled text
		var lineContent string
		switch {
		case isActiveMatch:
			// Active match: full line (including the line-number prefix)
			// highlighted with the warning background.
			prefixStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(m.theme.Warning)).
				Foreground(lipgloss.Color(m.theme.Background))
			lineContent = prefixStyle.Render(fmt.Sprintf("%4d │ ", lineNum)) +
				m.colorizeLineForSearch(formatLogEvent(evt), m.theme.Warning)
		case isPassiveMatch:
			// Passive match: accent foreground
			lineContent = styles.AccentText.Render(fmt.Sprintf("%4d │ ", lineNum)) +
				m.colorizeLineWithHighlight(formatLogEvent(evt), styles)
		default:
			// Normal line: styled directly from the structured event fields
			lineContent = styles.FaintText.Render(fmt.Sprintf("%4d │ ", lineNum)) +
				m.styleLogEvent(evt, styles, false)
		}

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
// subject highlighted, message in normal text, and structured fields
// appended below, matching the visual treatment previously produced by
// re-parsing formatLogEvent's output with regexes.
//
// highlightErrorHint makes the error_hint field (when present) stand out
// with the warning/danger style, matching the level's severity. The daemon
// log view passes false; the problems view -- where error_hint is the most
// direct answer to "what broke" -- passes true.
func (m *Model) styleLogEvent(evt spindle.LogEvent, styles Styles, highlightErrorHint bool) string {
	level := strings.ToUpper(strings.TrimSpace(evt.Level))

	var result strings.Builder
	result.WriteString(styles.FaintText.Render(logEventTimestamp(evt)))
	result.WriteString(" ")
	result.WriteString(m.getLevelStyle(level, styles).Bold(true).Render(level))

	// Note: the [component] tag is part of formatLogEvent's plain text (for
	// search) but is not shown here, since the stage is already surfaced via
	// the subject below.
	if subject := composeLogSubject(evt.ItemID, evt.Stage); subject != "" {
		result.WriteString(" ")
		result.WriteString(styles.AccentText.Render(subject))
	}

	if message := strings.TrimSpace(evt.Message); message != "" {
		result.WriteString(" ")
		result.WriteString(styles.FaintText.Render("–"))
		result.WriteString(" ")
		result.WriteString(styles.Text.Render(message))
	}

	for _, key := range orderedFieldKeys(evt.Fields) {
		value := strings.TrimSpace(evt.Fields[key])
		if value == "" {
			continue
		}
		result.WriteString("\n")
		result.WriteString(styleLogFieldRow(key, value, styles, level, highlightErrorHint))
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

// logFiltersActive returns true if any log filters are active.
func (m *Model) logFiltersActive() bool {
	return m.logState.filterLevel != "" || m.logState.filterComponent != "" || m.logState.filterLane != "" || m.logState.filterRequest != ""
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
			// Invalid regex - stay in search mode
			return m, nil
		}

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
		m.logState.searchActive = false
		m.logState.searchInput.Blur()
		m.logState.searchInput.SetValue("")
		return m, nil
	}

	// Let the text input handle the key
	var cmd tea.Cmd
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
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), logFetchTimeout)
		defer cancel()

		query := spindle.LogQuery{
			Since:      m.logState.streamCursor,
			Limit:      logFetchLimit,
			Level:      m.logState.filterLevel,
			Component:  m.logState.filterComponent,
			Lane:       m.logState.filterLane,
			DaemonOnly: true, // Only logs without item association
			Request:    m.logState.filterRequest,
		}
		if m.logState.streamCursor == 0 {
			query.Tail = true
		}

		batch, err := m.client.FetchLogs(ctx, query)
		if err != nil {
			return logErrorMsg{err: err}
		}

		return logBatchMsg{
			events: batch.Events,
			next:   batch.Next,
			source: logSourceDaemon,
		}
	}
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
		m.clearLogSearch()
		m.logState.contentVersion++
	}

	// Capture cursor for the closure
	cursor := m.logState.itemCursor

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), logFetchTimeout)
		defer cancel()

		query := spindle.LogQuery{
			Since:     cursor,
			Limit:     logFetchLimit,
			ItemID:    itemID,
			Level:     m.logState.filterLevel,
			Component: m.logState.filterComponent,
			Lane:      m.logState.filterLane,
			Request:   m.logState.filterRequest,
		}
		if cursor == 0 {
			query.Tail = true
		}

		batch, err := m.client.FetchLogs(ctx, query)
		if err != nil {
			return logErrorMsg{err: err}
		}

		return logBatchMsg{
			events: batch.Events,
			next:   batch.Next,
			source: logSourceItem,
			itemID: itemID,
		}
	}
}

// Log messages

type logBatchMsg struct {
	events []spindle.LogEvent
	next   uint64
	source logSource
	itemID int64 // For item logs, tracks which item this is for
}

type logErrorMsg struct {
	err error
}

// handleLogBatch processes a batch of log events from the streaming API.
func (m *Model) handleLogBatch(msg logBatchMsg) {
	if msg.source != m.logState.mode {
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

// logEventTimestamp formats an event's timestamp for display, preferring the
// parsed local time and falling back to the raw timestamp string.
func logEventTimestamp(evt spindle.LogEvent) string {
	if parsed := evt.ParsedTime(); !parsed.IsZero() {
		return parsed.In(time.Local).Format("2006-01-02 15:04:05")
	}
	return evt.Timestamp
}

// formatLogEvent formats a single log event.
func formatLogEvent(evt spindle.LogEvent) string {
	ts := logEventTimestamp(evt)
	level := strings.ToUpper(strings.TrimSpace(evt.Level))
	parts := []string{ts, level}
	if component := strings.TrimSpace(evt.Component); component != "" {
		parts = append(parts, fmt.Sprintf("[%s]", component))
	}
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
	levelInput.Placeholder = "e.g. error, warn, info, debug"
	levelInput.CharLimit = 20
	levelInput.SetWidth(30)

	// Component input
	compInput := textinput.New()
	compInput.Placeholder = "e.g. api, workflow, encoder"
	compInput.CharLimit = 50
	compInput.SetWidth(30)

	// Lane input
	laneInput := textinput.New()
	laneInput.Placeholder = "e.g. ripping, encoding"
	laneInput.CharLimit = 50
	laneInput.SetWidth(30)

	// Request input
	reqInput := textinput.New()
	reqInput.Placeholder = "e.g. abc123"
	reqInput.CharLimit = 50
	reqInput.SetWidth(30)

	m.logFilterInputs[0] = levelInput
	m.logFilterInputs[1] = compInput
	m.logFilterInputs[2] = laneInput
	m.logFilterInputs[3] = reqInput
}

// openLogFilters opens the log filters modal.
func (m *Model) openLogFilters() {
	// Pre-fill with current filter values
	m.logFilterInputs[0].SetValue(m.logState.filterLevel)
	m.logFilterInputs[1].SetValue(m.logState.filterComponent)
	m.logFilterInputs[2].SetValue(m.logState.filterLane)
	m.logFilterInputs[3].SetValue(m.logState.filterRequest)
	m.logFilterFocusIdx = 0
	m.logFilterInputs[0].Focus()
	m.logFilterInputs[1].Blur()
	m.logFilterInputs[2].Blur()
	m.logFilterInputs[3].Blur()
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
		// Clear all filters (modal-specific, doesn't quit)
		m.logFilterInputs[0].SetValue("")
		m.logFilterInputs[1].SetValue("")
		m.logFilterInputs[2].SetValue("")
		m.logFilterInputs[3].SetValue("")
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
	m.logState.filterComponent = strings.TrimSpace(m.logFilterInputs[1].Value())
	m.logState.filterLane = strings.TrimSpace(m.logFilterInputs[2].Value())
	m.logState.filterRequest = strings.TrimSpace(m.logFilterInputs[3].Value())

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

	// Hint
	b.WriteString(styles.MutedText.Render("Filters apply to both daemon and item logs."))
	b.WriteString("\n")
	b.WriteString(styles.MutedText.Render("Leave blank to disable filter."))
	b.WriteString("\n\n")

	// Filter fields
	fields := []struct {
		label string
		index int
	}{
		{"Level:     ", 0},
		{"Component: ", 1},
		{"Lane:      ", 2},
		{"Request:   ", 3},
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
		b.WriteString("\n\n")
	}

	// Buttons hint
	b.WriteString(styles.FaintText.Render("Enter: Apply  •  Esc: Cancel  •  Ctrl+C: Clear"))

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
