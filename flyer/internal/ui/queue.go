package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// updateQueueTable updates selection bounds when queue changes.
// Preserves selection by item ID when possible.
func (m *Model) updateQueueTable() {
	// Get the currently selected item's ID before updating
	var selectedID int64
	if item := m.getSelectedItem(); item != nil {
		selectedID = item.ID
	}

	items := m.getSortedItems()
	itemCount := len(items)

	if itemCount == 0 {
		m.selectedRow = 0
		return
	}

	// Try to find the previously selected item by ID
	if selectedID > 0 {
		for i, item := range items {
			if item.ID == selectedID {
				m.selectedRow = i
				return
			}
		}
	}

	// Item not found - clamp to valid range
	if m.selectedRow >= itemCount {
		m.selectedRow = itemCount - 1
	}
}

// getSelectedItem returns the currently selected queue item.
func (m *Model) getSelectedItem() *spindle.QueueItem {
	items := m.getSortedItems()
	if m.selectedRow < 0 || m.selectedRow >= len(items) {
		return nil
	}
	return &items[m.selectedRow]
}

// getSortedItems returns queue items filtered and sorted by priority.
func (m *Model) getSortedItems() []spindle.QueueItem {
	items := make([]spindle.QueueItem, 0, len(m.snapshot.Queue))
	query := strings.ToLower(m.queueFilterQuery)

	// Apply filter
	for _, item := range m.snapshot.Queue {
		switch m.filterMode {
		case FilterFailed:
			if !strings.EqualFold(item.Stage, "failed") {
				continue
			}
		case FilterReview:
			if !item.NeedsReview {
				continue
			}
		case FilterProcessing:
			if !isProcessingItem(item) {
				continue
			}
		}
		if query != "" && !queueItemMatches(item, query) {
			continue
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		// Review first, then failed, then live work, then by ID ascending
		// (spindle's processing order).
		pi, pj := itemSortRank(items[i]), itemSortRank(items[j])
		if pi != pj {
			return pi < pj
		}
		return items[i].ID < items[j].ID
	})

	return items
}

// queueItemMatches reports whether an item matches the lowercase text query
// (substring of the display title or the "#id" form).
func queueItemMatches(item spindle.QueueItem, query string) bool {
	if strings.Contains(strings.ToLower(composeTitle(item)), query) {
		return true
	}
	return strings.Contains(fmt.Sprintf("#%d", item.ID), query)
}

// queueColumns holds the computed fixed column widths for the queue table.
// ago == 0 hides the age column (compact terminals).
type queueColumns struct {
	strip  int
	id     int
	disc   int
	stage  int
	work   int
	ago    int
	title  int
	labels bool
}

// computeQueueColumns derives column widths from the item set and terminal
// width; the title column absorbs the slack of the panel interior. Below 80
// terminal columns the age column is dropped; wide terminals name outcomes.
func computeQueueColumns(items []spindle.QueueItem, width int) queueColumns {
	cols := queueColumns{strip: 1, id: 2, stage: 12, work: 4, ago: 8}
	if width < 80 {
		cols.ago = 0
	}
	if width >= compactWidthThreshold {
		cols.labels = true
		cols.work = len("FILES DONE")
	}
	for _, item := range items {
		count := queueFileCount(item)
		n := len(count)
		if cols.labels && count != "" {
			label := "published"
			if task := item.PrimaryTask(); task != nil {
				label = stageDisplay(task.Type).doneLabel
			}
			n += 1 + len(label)
		}
		cols.work = max(cols.work, n)
		if n := taskStripWidth(item); n > cols.strip {
			cols.strip = n
		}
		idLen := len(fmt.Sprintf("#%d", item.ID)) + 1 // room for review "?"
		if idLen > cols.id {
			cols.id = idLen
		}
		if item.DiscNumber > 0 {
			cols.disc = len("DISC")
		}
	}

	// Fixed columns plus 2-space separators between all columns.
	fixed := cols.strip + cols.id + cols.stage + cols.work + 8
	if cols.disc > 0 {
		fixed += cols.disc + 2
	}
	if cols.ago > 0 {
		fixed += cols.ago + 2
	}
	cols.title = max(panelInnerWidth(width)-fixed, 10)
	return cols
}

// queueFilterLineVisible reports whether the queue filter prompt row is shown.
func (m *Model) queueFilterLineVisible() bool {
	return m.queueFilterActive || m.queueFilterQuery != ""
}

// queueVisibleRows returns the item rows available to the queue table.
// Fixed chrome: header band, NOW band, panel borders, column header, footer
// band (+ the filter prompt row when shown).
func (m *Model) queueVisibleRows() int {
	rows := m.height - 6
	if m.queueFilterLineVisible() {
		rows--
	}
	return max(rows, 1)
}

// renderQueue renders the dashboard queue table as a Level 1 panel.
func (m Model) renderQueue() string {
	styles := m.theme.Styles()
	visibleRows := m.queueVisibleRows()

	var lines []string
	if m.queueFilterLineVisible() {
		lines = append(lines, m.renderQueueFilterLine(styles))
	}

	items := m.getSortedItems()
	cols := computeQueueColumns(items, m.width)
	lines = append(lines, renderQueueHeaderRow(cols, styles))

	footer := ""
	if len(items) == 0 {
		msg := "No items in queue"
		switch {
		case m.queueFilterQuery != "":
			msg = "No items match: " + m.queueFilterQuery
		case m.filterMode != FilterAll:
			msg = "No items match filter: " + m.filterLabel()
		}
		lines = append(lines, styles.MutedText.Render(msg))
	} else {
		// Keep the selection visible within the scroll window. The stored
		// offset is maintained on key handling; re-derive here defensively
		// so a resize between keypresses cannot hide the selection.
		scroll := clampQueueScroll(m.queueScroll, m.selectedRow, visibleRows, len(items))
		end := min(scroll+visibleRows, len(items))
		for i := scroll; i < end; i++ {
			lines = append(lines, m.renderQueueRow(items[i], cols, i == m.selectedRow, styles))
		}
		footer = scrollRangeFooter(scroll, end, len(items), visibleRows)
	}

	// Fill the panel to a stable height so the frame does not jump as the
	// queue grows and shrinks.
	for len(lines) < visibleRows+1+boolToInt(m.queueFilterLineVisible()) {
		lines = append(lines, "")
	}

	return renderPanel(m.getQueueTitle(), strings.Join(lines, "\n"), footer, m.width, styles)
}

// scrollRangeFooter formats a "start-end of total" panel footer, empty when
// everything fits.
func scrollRangeFooter(scroll, end, total, visible int) string {
	if total <= visible {
		return ""
	}
	return fmt.Sprintf("%d-%d of %d", scroll+1, end, total)
}

// boolToInt converts a bool to 0 or 1.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// renderQueueFilterLine renders the "/" filter prompt or the applied query.
func (m Model) renderQueueFilterLine(styles Styles) string {
	if m.queueFilterActive {
		return styles.AccentText.Render("/") + m.queueFilterInput.View()
	}
	return styles.AccentText.Render("/"+m.queueFilterQuery) +
		"  " + styles.FaintText.Render("Esc to clear")
}

// renderQueueHeaderRow renders the dim column header line.
func renderQueueHeaderRow(cols queueColumns, styles Styles) string {
	pad := func(s string, w int) string {
		if n := w - len(s); n > 0 {
			return s + strings.Repeat(" ", n)
		}
		return s
	}
	workLabel := "DONE"
	if cols.labels {
		workLabel = "FILES DONE"
	}
	parts := []string{
		pad("", cols.strip),
		pad("ID", cols.id),
		pad("TITLE", cols.title),
	}
	if cols.disc > 0 {
		parts = append(parts, pad("DISC", cols.disc))
	}
	parts = append(parts,
		pad("STAGE", cols.stage),
		pad(workLabel, cols.work),
	)
	if cols.ago > 0 {
		parts = append(parts, "AGE")
	}
	return styles.FaintText.Render(strings.Join(parts, "  "))
}

// clampQueueScroll adjusts a scroll offset so the selection stays visible
// and the window stays within bounds.
func clampQueueScroll(scroll, selected, visible, total int) int {
	if selected < scroll {
		scroll = selected
	}
	if selected >= scroll+visible {
		scroll = selected - visible + 1
	}
	return max(min(scroll, total-visible), 0)
}

// ensureQueueVisible updates the stored scroll offset after selection moves.
func (m *Model) ensureQueueVisible() {
	m.queueScroll = clampQueueScroll(m.queueScroll, m.selectedRow, m.queueVisibleRows(), len(m.getSortedItems()))
}

// renderQueueRow renders one queue table row:
// strip  id  title  [disc]  stage  pct  ago
// The selected row renders as one selection-colored bar (no per-cell colors,
// guaranteeing contrast); other rows use per-cell styling.
func (m Model) renderQueueRow(item spindle.QueueItem, cols queueColumns, selected bool, styles Styles) string {
	idStr := fmt.Sprintf("#%d", item.ID)
	if item.NeedsReview {
		idStr += "?"
	}
	title := truncate(composeTitle(item), cols.title)
	disc := ""
	if item.DiscNumber > 0 {
		disc = fmt.Sprintf("%d", item.DiscNumber)
	}
	stage, stageStyle := queueStageCell(item, styles)
	ago := ""
	if cols.ago > 0 {
		if updated := parseTimestamp(item.UpdatedAt); !updated.IsZero() {
			ago = humanizeDuration(time.Since(updated))
		}
	}

	pad := func(s string, w int) string {
		if n := w - lipgloss.Width(s); n > 0 {
			return s + strings.Repeat(" ", n)
		}
		return s
	}

	if selected {
		fields := []string{
			pad(plainTaskStrip(item), cols.strip),
			pad(idStr, cols.id),
			pad(title, cols.title),
		}
		if cols.disc > 0 {
			fields = append(fields, pad(disc, cols.disc))
		}
		fields = append(fields,
			pad(stage, cols.stage),
			pad(m.queueProgressCell(item, cols, stageStyle, styles, true), cols.work),
		)
		if cols.ago > 0 {
			fields = append(fields, ago)
		}
		line := strings.Join(fields, "  ")
		if n := panelInnerWidth(m.width) - lipgloss.Width(line); n > 0 {
			line += strings.Repeat(" ", n)
		}
		return styles.Selected.Render(line)
	}

	idStyle := styles.MutedText
	if item.NeedsReview {
		idStyle = styles.WarningText
	}

	parts := []string{
		pad(m.renderTaskStrip(item, styles), cols.strip),
		idStyle.Render(pad(idStr, cols.id)),
		styles.Text.Render(pad(title, cols.title)),
	}
	if cols.disc > 0 {
		parts = append(parts, styles.AccentText.Render(pad(disc, cols.disc)))
	}
	parts = append(parts,
		stageStyle.Render(pad(stage, cols.stage)),
		pad(m.queueProgressCell(item, cols, stageStyle, styles, false), cols.work),
	)
	if cols.ago > 0 {
		parts = append(parts, styles.FaintText.Render(ago))
	}
	return strings.Join(parts, "  ")
}

// queueProgressCell shows completed files, scoped by the adjacent task column.
// A wide terminal adds the outcome label; percentages live in the inspector.
func (m Model) queueProgressCell(item spindle.QueueItem, cols queueColumns, _ lipgloss.Style, styles Styles, plain bool) string {
	text := queueFileCount(item)
	if cols.labels && text != "" {
		label := "published"
		if task := item.PrimaryTask(); task != nil {
			label = stageDisplay(task.Type).doneLabel
		}
		text += " " + label
	}
	if plain || text == "" {
		return text
	}
	return styles.MutedText.Render(text)
}

// queueStageCell returns the stage column text and style for an item.
func queueStageCell(item spindle.QueueItem, styles Styles) (string, lipgloss.Style) {
	if item.NeedsReview {
		return "REVIEW", styles.WarningText
	}
	if strings.EqualFold(item.Stage, "failed") {
		return "FAILED", styles.DangerText
	}
	info := stageDisplay(itemDisplayStage(item))
	label := info.label
	style := roleStyle(info.role, styles)
	if item.IsTerminal() {
		label = info.doneLabel
		style = styles.MutedText
	} else if len(item.WorkingTasks()) == 0 {
		label = "waiting"
		style = styles.FaintText
	}
	return strings.ToLower(label), style
}

// queueFileCount returns completed files, never the current file's position.
func queueFileCount(item spindle.QueueItem) string {
	_, totals := item.EpisodeSnapshot()
	if totals.Planned == 0 {
		return ""
	}
	if item.Stage == "completed" {
		return fmt.Sprintf("%d/%d", totals.Final, totals.Planned)
	}
	if task := item.PrimaryTask(); task != nil {
		if count, ok := stageThroughput(stageDisplay(task.Type).totals, item, totals); ok {
			return fmt.Sprintf("%d/%d", count, totals.Planned)
		}
	}
	return ""
}

// stripCollapsed reports whether an item's task strip collapses to a single
// summary glyph. Completed items do: a run of identical green checks per
// row buries the one strip that matters. Failed items keep the full strip
// -- the ✗ position answers "where did it fail" at a glance.
func stripCollapsed(item spindle.QueueItem) bool {
	return len(item.Tasks) == 0 || strings.EqualFold(item.Stage, "completed")
}

// taskStripWidth returns the glyph-cell width an item's task strip needs.
func taskStripWidth(item spindle.QueueItem) int {
	if stripCollapsed(item) {
		return 1
	}
	return len(item.Tasks)
}

// collapsedStripGlyph returns the single summary glyph for a collapsed strip.
func collapsedStripGlyph(item spindle.QueueItem) string {
	switch {
	case strings.EqualFold(item.Stage, "completed"):
		return "✓"
	case strings.EqualFold(item.Stage, "failed"):
		return "✗"
	default:
		return "○"
	}
}

// renderTaskStrip renders one glyph per task, colored by task state (with
// the running glyph in its stage's role color). Completed or task-less
// items collapse to a single summary glyph.
func (m Model) renderTaskStrip(item spindle.QueueItem, styles Styles) string {
	if stripCollapsed(item) {
		glyph := collapsedStripGlyph(item)
		style := styles.FaintText
		switch glyph {
		case "✓":
			style = styles.SuccessText
		case "✗":
			style = styles.DangerText
		}
		return style.Render(glyph)
	}

	var b strings.Builder
	for _, t := range item.Tasks {
		style := styles.FaintText
		switch t.State {
		case "done":
			style = styles.SuccessText
		case "running":
			if t.IsWorking() {
				style = roleStyle(stageDisplay(t.Type).role, styles)
			}
		case "failed":
			style = styles.DangerText
		}
		state := t.State
		if state == "running" && !t.IsWorking() {
			state = "pending"
		}
		b.WriteString(style.Render(taskStateGlyph(state)))
	}
	return b.String()
}

// plainTaskStrip renders the task strip without styling (for selected rows).
func plainTaskStrip(item spindle.QueueItem) string {
	if stripCollapsed(item) {
		return collapsedStripGlyph(item)
	}
	var b strings.Builder
	for _, t := range item.Tasks {
		state := t.State
		if state == "running" && !t.IsWorking() {
			state = "pending"
		}
		b.WriteString(taskStateGlyph(state))
	}
	return b.String()
}

// composeTitle builds the display title for an item, preferring the
// server-computed one.
func composeTitle(item spindle.QueueItem) string {
	if item.DisplayTitle != "" {
		return item.DisplayTitle
	}
	if item.DiscTitle != "" {
		return item.DiscTitle
	}
	return fmt.Sprintf("ID #%d", item.ID)
}

// getQueueTitle returns the queue rule title with optional filter indicator.
func (m Model) getQueueTitle() string {
	items := m.getSortedItems()
	total := len(m.snapshot.Queue)
	visible := len(items)

	if m.filterMode == FilterAll {
		if m.queueFilterQuery != "" {
			return fmt.Sprintf("Queue (%d/%d)", visible, total)
		}
		return fmt.Sprintf("Queue (%d)", total)
	}

	// Show "Queue (visible/total) - FilterName"
	return fmt.Sprintf("Queue (%d/%d) %s", visible, total, m.filterLabel())
}
