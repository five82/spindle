package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/spindle"
)

// Problems refresh constants
const (
	problemsRefreshInterval = 2 * time.Second
	problemsFetchTimeout    = 5 * time.Second
	problemsFetchLimit      = 100
	problemsBufferLimit     = 500
)

// problemsState holds warn/error log state for the inspector Problems tab.
type problemsState struct {
	logLines    []spindle.LogEvent
	logCursor   uint64
	lastItemID  int64
	lastRefresh time.Time
}

// --- Global triage view ---

// getTriageItems returns all items needing operator attention (review or
// failed), in queue priority order.
func (m *Model) getTriageItems() []spindle.QueueItem {
	var items []spindle.QueueItem
	for _, item := range m.snapshot.Queue {
		if item.NeedsReview || strings.EqualFold(item.Stage, "failed") {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		pi, pj := itemSortRank(items[i]), itemSortRank(items[j])
		if pi != pj {
			return pi < pj
		}
		return items[i].ID < items[j].ID
	})
	return items
}

// getTriageItem returns the selected triage item.
func (m *Model) getTriageItem() *spindle.QueueItem {
	items := m.getTriageItems()
	if m.problemsRow < 0 || m.problemsRow >= len(items) {
		return nil
	}
	return &items[m.problemsRow]
}

// clampProblemsRow keeps the triage selection within bounds.
func (m *Model) clampProblemsRow() {
	if count := len(m.getTriageItems()); m.problemsRow >= count {
		m.problemsRow = max(count-1, 0)
	}
}

// problemsVisibleRows returns the item rows available to the triage panel.
// Fixed chrome: header band, panel borders, footer band.
func (m *Model) problemsVisibleRows() int {
	return max(m.height-4, 1)
}

// renderProblems renders the global triage list as a Level 1 panel: every
// failed/review item with its lead reason. Enter drills into the item's
// Problems tab.
func (m Model) renderProblems() string {
	styles := m.theme.Styles()
	visibleRows := m.problemsVisibleRows()

	items := m.getTriageItems()

	var lines []string
	footer := ""
	if len(items) == 0 {
		lines = append(lines, styles.SuccessText.Render("No failed or review items"))
	} else {
		scroll := clampQueueScroll(m.problemsScroll, m.problemsRow, visibleRows, len(items))
		end := min(scroll+visibleRows, len(items))
		for i := scroll; i < end; i++ {
			lines = append(lines, m.renderTriageRow(items[i], i == m.problemsRow, styles))
		}
		footer = scrollRangeFooter(scroll, end, len(items), visibleRows)
	}
	for len(lines) < visibleRows {
		lines = append(lines, "")
	}

	title := fmt.Sprintf("Problems (%d)", len(items))
	return renderPanel(title, strings.Join(lines, "\n"), footer, m.width, styles)
}

// renderTriageRow renders one triage list row: marker, id, title, reason.
func (m Model) renderTriageRow(item spindle.QueueItem, selected bool, styles Styles) string {
	marker, markerStyle := "?", styles.WarningText
	if strings.EqualFold(item.Stage, "failed") {
		marker, markerStyle = "✗", styles.DangerText
	}

	inner := panelInnerWidth(m.width)
	idStr := fmt.Sprintf("#%d", item.ID)
	title := truncate(composeTitle(item), 40)
	reasonWidth := max(inner-(2+len(idStr)+1+lipgloss.Width(title)+2), 10)
	reason := truncate(triageLeadReason(item), reasonWidth)

	if selected {
		line := fmt.Sprintf("%s %s %s  %s", marker, idStr, title, reason)
		if n := inner - lipgloss.Width(line); n > 0 {
			line += strings.Repeat(" ", n)
		}
		return m.theme.Styles().Selected.Render(line)
	}

	return markerStyle.Render(marker) + " " +
		styles.MutedText.Render(idStr) + " " +
		styles.Text.Render(title) + "  " +
		styles.MutedText.Render(reason)
}

// triageLeadReason picks the most direct one-line answer to "what's wrong".
func triageLeadReason(item spindle.QueueItem) string {
	if task := item.FailedTask(); task != nil {
		reason := stageDisplay(task.Type).label + " failed"
		if msg := strings.TrimSpace(task.Error); msg != "" {
			reason += ": " + msg
		}
		return reason
	}
	if item.NeedsReview && len(item.ReviewReasons) > 0 {
		return strings.Join(item.ReviewReasons, "; ")
	}
	if msg := strings.TrimSpace(item.ErrorMessage); msg != "" {
		return msg
	}
	if stage := strings.TrimSpace(item.FailedAtStage); stage != "" {
		return stageDisplay(stage).label + " failed"
	}
	if item.NeedsReview {
		return "Needs operator review"
	}
	return ""
}

// handleProblemsKey processes keyboard input for the triage view.
func (m Model) handleProblemsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Inspect):
		return m.openInspector(tabProblems)

	case key.Matches(msg, m.keys.InspectLogs):
		return m.openInspector(tabLogs)

	case key.Matches(msg, m.keys.Escape):
		m.currentView = ViewQueue
		return m, nil
	}

	items := m.getTriageItems()
	if len(items) == 0 {
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Down):
		if m.problemsRow < len(items)-1 {
			m.problemsRow++
		}
	case key.Matches(msg, m.keys.Up):
		if m.problemsRow > 0 {
			m.problemsRow--
		}
	case key.Matches(msg, m.keys.Top):
		m.problemsRow = 0
	case key.Matches(msg, m.keys.Bottom):
		m.problemsRow = len(items) - 1
	}
	m.problemsScroll = clampQueueScroll(m.problemsScroll, m.problemsRow, m.problemsVisibleRows(), len(items))

	return m, nil
}

// --- Inspector Problems tab ---

// renderItemProblems renders the full problems content for an item:
// structured problem data followed by its warn/error log lines.
func (m *Model) renderItemProblems(item *spindle.QueueItem) string {
	styles := m.theme.Styles()

	var b strings.Builder
	m.renderStructuredProblems(&b, item, styles)

	// Add log messages section if we have any (for this item)
	if m.problemsState.lastItemID == item.ID && len(m.problemsState.logLines) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(styles.MutedText.Bold(true).Render("Log Messages"))
		b.WriteString("\n")

		for i, evt := range m.problemsState.logLines {
			b.WriteString(styles.FaintText.Render(fmt.Sprintf("%4d │ ", i+1)))
			b.WriteString(m.styleLogEvent(evt, styles, true))
			b.WriteString("\n")
		}
	}

	if b.Len() == 0 {
		return styles.MutedText.Render("No warnings or errors for this item")
	}
	return b.String()
}

// renderStructuredProblems extracts problem info from the item's structured data.
func (m *Model) renderStructuredProblems(b *strings.Builder, item *spindle.QueueItem, styles Styles) {
	// Failed task leads: it's the most direct answer to "what broke".
	m.renderFailedTaskSection(b, item, styles)

	// Review reasons
	if item.NeedsReview && len(item.ReviewReasons) > 0 {
		m.renderProblemSection(b, "Review Reasons", styles.WarningText)
		for _, reason := range item.ReviewReasons {
			if reason = strings.TrimSpace(reason); reason == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(styles.WarningText.Render("•"))
			b.WriteString(" ")
			b.WriteString(styles.Text.Render(reason))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Error message
	if msg := strings.TrimSpace(item.ErrorMessage); msg != "" {
		m.renderProblemSection(b, "Error", styles.DangerText)
		b.WriteString("  ")
		b.WriteString(styles.Text.Render(msg))
		b.WriteString("\n\n")
	}

	// Per-episode errors
	failedEpisodes := spindle.FilterFailed(item.Episodes)
	if len(failedEpisodes) > 0 {
		m.renderProblemSection(b, "Failed Episodes", styles.DangerText)
		for _, ep := range failedEpisodes {
			epLabel := ep.Key
			if ep.Title != "" {
				epLabel = fmt.Sprintf("%s - %s", ep.Key, ep.Title)
			}
			b.WriteString("  ")
			b.WriteString(styles.DangerText.Render("✗"))
			b.WriteString(" ")
			b.WriteString(styles.Text.Render(epLabel))
			b.WriteString("\n")
			if msg := strings.TrimSpace(ep.ErrorMessage); msg != "" {
				b.WriteString("      ")
				b.WriteString(styles.MutedText.Render(msg))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	// Encoding error details
	if item.Encoding != nil && item.Encoding.Error != nil {
		err := item.Encoding.Error
		if strings.TrimSpace(err.Title) != "" || strings.TrimSpace(err.Message) != "" {
			m.renderProblemSection(b, "Encoding Error", styles.DangerText)
			if err.Title != "" {
				b.WriteString("  ")
				b.WriteString(styles.Text.Render(err.Title))
				b.WriteString("\n")
			}
			if err.Message != "" {
				b.WriteString("  ")
				b.WriteString(styles.MutedText.Render(err.Message))
				b.WriteString("\n")
			}
			if err.Context != "" {
				b.WriteString("  ")
				b.WriteString(styles.FaintText.Render("Context:") + " ")
				b.WriteString(styles.Text.Render(err.Context))
				b.WriteString("\n")
			}
			if err.Suggestion != "" {
				b.WriteString("  ")
				b.WriteString(styles.FaintText.Render("Suggestion:") + " ")
				b.WriteString(styles.SuccessText.Render(err.Suggestion))
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// Encoding warning
	if item.Encoding != nil && strings.TrimSpace(item.Encoding.Warning) != "" {
		m.renderProblemSection(b, "Warning", styles.WarningText)
		b.WriteString("  ")
		b.WriteString(styles.Text.Render(item.Encoding.Warning))
		b.WriteString("\n\n")
	}

	// Validation steps
	if item.Encoding != nil && item.Encoding.Validation != nil {
		val := item.Encoding.Validation
		// Only show validation section if it failed or has steps to show
		if !val.Passed || len(val.Steps) > 0 {
			var titleStyle lipgloss.Style
			var passedIcon string
			if val.Passed {
				passedIcon = "✓"
				titleStyle = styles.SuccessText
			} else {
				passedIcon = "✗"
				titleStyle = styles.DangerText
			}

			b.WriteString(styles.MutedText.Bold(true).Render("Validation"))
			b.WriteString(" ")
			b.WriteString(titleStyle.Render(passedIcon))
			b.WriteString("\n")

			for _, step := range val.Steps {
				var icon string
				var iconStyle lipgloss.Style
				if step.Passed {
					icon = "✓"
					iconStyle = styles.SuccessText
				} else {
					icon = "✗"
					iconStyle = styles.DangerText
				}
				b.WriteString("  ")
				b.WriteString(iconStyle.Render(icon))
				b.WriteString(" ")
				b.WriteString(styles.Text.Render(step.Name))
				b.WriteString("\n")
				if strings.TrimSpace(step.Details) != "" {
					b.WriteString("      ")
					b.WriteString(styles.MutedText.Render(step.Details))
					b.WriteString("\n")
				}
			}
			b.WriteString("\n")
		}
	}
}

// renderFailedTaskSection leads the problems view with the item's failed
// task, when one exists: its name, attempts, and error. When no task is
// failed but the daemon still remembers a failed stage (a retry recompile
// can transiently drop the task rows), that stage name is shown as a
// fallback label. Otherwise this renders nothing.
func (m *Model) renderFailedTaskSection(b *strings.Builder, item *spindle.QueueItem, styles Styles) {
	danger := roleStyle("danger", styles)

	if task := item.FailedTask(); task != nil {
		m.renderProblemSection(b, "Failed Task", danger)
		b.WriteString("  ")
		b.WriteString(styles.Text.Bold(true).Render(stageDisplay(task.Type).label))
		if task.Attempts > 0 {
			b.WriteString(styles.MutedText.Render(fmt.Sprintf(" (attempt %d)", task.Attempts)))
		}
		b.WriteString("\n")
		if msg := strings.TrimSpace(task.Error); msg != "" {
			b.WriteString("  ")
			b.WriteString(styles.MutedText.Render(msg))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		return
	}

	if len(item.Tasks) == 0 {
		if stage := strings.TrimSpace(item.FailedAtStage); stage != "" {
			m.renderProblemSection(b, "Failed Task", danger)
			b.WriteString("  ")
			b.WriteString(styles.Text.Bold(true).Render(stageDisplay(stage).label))
			b.WriteString("\n\n")
		}
	}
}

// renderProblemSection renders a section header for problems.
func (m *Model) renderProblemSection(b *strings.Builder, title string, titleStyle lipgloss.Style) {
	b.WriteString(titleStyle.Bold(true).Render(title))
	b.WriteString("\n")
}

// --- Problems Log Fetching ---

// refreshProblemsLogs fetches warn/error logs for the given item.
func (m *Model) refreshProblemsLogs(item *spindle.QueueItem) tea.Cmd {
	if m.client == nil || item == nil {
		return nil
	}

	// Skip when API is offline to reduce error noise
	if m.snapshot.IsOffline() {
		return nil
	}

	// Clear logs if item changed
	if item.ID != m.problemsState.lastItemID {
		m.problemsState.logLines = nil
		m.problemsState.logCursor = 0
		m.problemsState.lastItemID = item.ID
	}

	// Don't refresh too frequently
	if time.Since(m.problemsState.lastRefresh) < problemsRefreshInterval {
		return nil
	}
	m.problemsState.lastRefresh = time.Now()

	itemID := item.ID
	cursor := m.problemsState.logCursor

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), problemsFetchTimeout)
		defer cancel()

		query := spindle.LogQuery{
			Since:  cursor,
			Limit:  problemsFetchLimit,
			ItemID: itemID,
			Level:  "warn", // Fetch warn and error level logs
		}
		if cursor == 0 {
			query.Tail = true
		}

		batch, err := m.client.FetchLogs(ctx, query)
		if err != nil {
			return problemsLogErrorMsg{err: err}
		}

		return problemsLogBatchMsg{
			events: batch.Events,
			next:   batch.Next,
			itemID: itemID,
		}
	}
}

// Problems log messages

type problemsLogBatchMsg struct {
	events []spindle.LogEvent
	next   uint64
	itemID int64
}

type problemsLogErrorMsg struct {
	err error
}

// handleProblemsLogBatch processes a batch of problems log events.
func (m *Model) handleProblemsLogBatch(msg problemsLogBatchMsg) {
	// Ignore if for a different item
	if msg.itemID != m.problemsState.lastItemID {
		return
	}

	// Update cursor for this item
	m.problemsState.logCursor = msg.next

	// Guard against duplicate/overlapping batches: only append events whose
	// Seq is strictly greater than the last one already appended (mirrors
	// handleLogBatch's guard in logs.go).
	var lastSeq uint64
	if n := len(m.problemsState.logLines); n > 0 {
		lastSeq = m.problemsState.logLines[n-1].Sequence
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
		m.problemsState.logLines = append(m.problemsState.logLines, newEvents...)
		m.problemsState.logLines = trimLogBuffer(m.problemsState.logLines, problemsBufferLimit)
		m.updateInspectorViewport()
	}
}
