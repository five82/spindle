package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/five82/flyer/internal/spindle"
)

// inspectorTabLabels are the tab bar entries in tab order.
var inspectorTabLabels = [tabCount]string{"Overview", "Episodes", "Problems", "Logs"}

// getInspectedItem resolves the inspected item by ID from the full queue,
// independent of filters and sorting. Returns nil when the item is gone.
func (m *Model) getInspectedItem() *spindle.QueueItem {
	if m.inspectedID == 0 {
		return nil
	}
	for i := range m.snapshot.Queue {
		if m.snapshot.Queue[i].ID == m.inspectedID {
			return &m.snapshot.Queue[i]
		}
	}
	return nil
}

// openInspector opens the full-screen inspector for the current selection.
func (m Model) openInspector(tab inspectorTab) (tea.Model, tea.Cmd) {
	var item *spindle.QueueItem
	if m.currentView == ViewProblems {
		item = m.getTriageItem()
	} else {
		item = m.getSelectedItem()
	}
	if item == nil {
		return m, nil
	}

	m.returnView = m.currentView
	m.inspecting = true
	m.inspectedID = item.ID
	return m.switchInspectorTab(tab)
}

// closeInspector returns to the view the inspector was opened from.
func (m *Model) closeInspector() {
	m.inspecting = false
	m.currentView = m.returnView
}

// switchInspectorTab activates a tab and kicks off its data fetch if needed.
func (m Model) switchInspectorTab(tab inspectorTab) (tea.Model, tea.Cmd) {
	m.inspectorTab = tab
	item := m.getInspectedItem()

	switch tab {
	case tabLogs:
		if m.logState.mode != logSourceItem {
			m.logState.mode = logSourceItem
			m.logState.rawLines = nil
			m.logState.itemCursor = 0
			m.logState.lastItemID = 0 // Force reset in fetchItemLogs
			m.clearLogSearch()
			m.logState.contentVersion++
		}
		m.updateLogViewport()
		return m, m.refreshLogs(item)
	case tabProblems:
		m.inspectorViewport.GotoTop()
		m.updateInspectorViewport()
		return m, m.refreshProblemsLogs(item)
	default:
		m.inspectorViewport.GotoTop()
		m.updateInspectorViewport()
		return m, nil
	}
}

// handleInspectorKey processes keyboard input while the inspector is open.
func (m Model) handleInspectorKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Escape):
		// Let an active log search clear first
		if m.inspectorTab == tabLogs && m.logState.searchRegex != nil {
			return m.handleLogsKey(msg)
		}
		m.closeInspector()
		return m, nil

	case key.Matches(msg, m.keys.Tab):
		return m.switchInspectorTab((m.inspectorTab + 1) % tabCount)

	case key.Matches(msg, m.keys.ShiftTab):
		return m.switchInspectorTab((m.inspectorTab + tabCount - 1) % tabCount)

	case key.Matches(msg, m.keys.Tab1):
		return m.switchInspectorTab(tabOverview)

	case key.Matches(msg, m.keys.Tab2):
		return m.switchInspectorTab(tabEpisodes)

	case key.Matches(msg, m.keys.Tab3):
		return m.switchInspectorTab(tabProblems)

	case key.Matches(msg, m.keys.Tab4):
		return m.switchInspectorTab(tabLogs)

	case key.Matches(msg, m.keys.ToggleEpisodes):
		m.toggleInspectedEpisodes()
		return m, nil
	}

	// Logs tab: delegate to the log key handler (follow, search, scroll)
	if m.inspectorTab == tabLogs {
		return m.handleLogsKey(msg)
	}

	// Other tabs scroll the inspector viewport
	switch {
	case key.Matches(msg, m.keys.Down):
		m.inspectorViewport.ScrollDown(1)
	case key.Matches(msg, m.keys.Up):
		m.inspectorViewport.ScrollUp(1)
	case key.Matches(msg, m.keys.Top):
		m.inspectorViewport.GotoTop()
	case key.Matches(msg, m.keys.Bottom):
		m.inspectorViewport.GotoBottom()
	case key.Matches(msg, m.keys.HalfPageDown):
		m.inspectorViewport.HalfPageDown()
	case key.Matches(msg, m.keys.HalfPageUp):
		m.inspectorViewport.HalfPageUp()
	case key.Matches(msg, m.keys.PageDown):
		m.inspectorViewport.PageDown()
	case key.Matches(msg, m.keys.PageUp):
		m.inspectorViewport.PageUp()
	}
	return m, nil
}

// toggleInspectedEpisodes flips the episode list collapse state for the
// inspected item, starting from its effective (auto-expand) default.
func (m *Model) toggleInspectedEpisodes() {
	item := m.getInspectedItem()
	if item == nil {
		return
	}
	episodes, totals := item.EpisodeSnapshot()
	collapsed := m.isEpisodesCollapsed(*item, episodes, totals)
	m.detailState.episodeCollapsed[item.ID] = !collapsed
	m.updateInspectorViewport()
}

// inspectorViewportHeight returns the panel interior height for inspector
// content. Chrome: header band, item band, tab band, panel borders, footer
// (+ log status line on the Logs tab).
func (m *Model) inspectorViewportHeight() int {
	return max(m.height-6, 1)
}

// initInspectorViewport initializes the inspector viewport.
func (m *Model) initInspectorViewport() {
	m.inspectorViewport = viewport.New(
		viewport.WithWidth(panelInnerWidth(m.width)),
		viewport.WithHeight(m.inspectorViewportHeight()),
	)
}

// updateInspectorViewport refreshes the inspector viewport content for the
// active tab.
func (m *Model) updateInspectorViewport() {
	if !m.inspecting {
		return
	}
	if m.inspectorViewport.Width() == 0 {
		m.initInspectorViewport()
	}
	inner := panelInnerWidth(m.width)
	m.inspectorViewport.SetWidth(inner)
	m.inspectorViewport.SetHeight(m.inspectorViewportHeight())

	item := m.getInspectedItem()
	if item == nil {
		m.inspectorViewport.SetContent(m.theme.Styles().MutedText.Render("Item no longer in queue"))
		return
	}

	switch m.inspectorTab {
	case tabEpisodes:
		m.inspectorViewport.SetContent(m.renderEpisodesTab(*item))
	case tabProblems:
		m.inspectorViewport.SetContent(m.renderItemProblems(item))
	default:
		m.inspectorViewport.SetContent(m.renderDetailContent(*item, inner))
	}
}

// renderInspector renders the full inspector: item band, tab band, and the
// active tab's content in a Level 1 panel.
func (m Model) renderInspector() string {
	styles := m.theme.Styles()
	band := m.theme.BandStyles()

	var b strings.Builder
	b.WriteString(padBand(m.renderInspectorItemLine(band), m.width, band.Band))
	b.WriteString("\n")
	b.WriteString(padBand(m.renderInspectorTabBar(band), m.width, band.Band))
	b.WriteString("\n")

	title := inspectorTabLabels[m.inspectorTab]
	if m.inspectorTab == tabLogs {
		b.WriteString(renderPanel(title, m.logViewport.View(), "", m.width, styles))
		b.WriteString("\n")
		b.WriteString(m.renderLogStatus(styles))
	} else {
		footer := ""
		if m.inspectorViewport.TotalLineCount() > m.inspectorViewport.VisibleLineCount() {
			footer = fmt.Sprintf("%d%%", int(m.inspectorViewport.ScrollPercent()*100))
		}
		b.WriteString(renderPanel(title, m.inspectorViewport.View(), footer, m.width, styles))
	}
	return b.String()
}

// renderInspectorItemLine renders the persistent item identity line, led by
// a breadcrumb back to the view Esc returns to (guide drill-down pattern).
// Segments carry drop ranks so narrow terminals shed whole segments
// (runtime first, then year, age, chips, disc) instead of cropping.
func (m Model) renderInspectorItemLine(styles Styles) string {
	crumb := "Queue"
	if m.returnView == ViewProblems {
		crumb = "Problems"
	}
	prefix := styles.FaintText.Render(crumb + " › ")

	item := m.getInspectedItem()
	if item == nil {
		return prefix + styles.AccentText.Bold(true).Render(fmt.Sprintf("ID #%d", m.inspectedID)) +
			styles.MutedText.Render(" (gone)")
	}

	itemLabel := fmt.Sprintf("ID #%d", item.ID)
	identity := prefix + styles.AccentText.Bold(true).Render(itemLabel)
	title := composeTitle(*item)
	if title != itemLabel {
		identity += styles.FaintText.Render(" › ") + styles.Text.Bold(true).Render(title)
	}
	parts := []headerPart{{identity, 0}}
	// Year and runtime are identity, not metadata. The display title
	// usually embeds the year already; only fill the gap when it doesn't.
	if year := metadataYear(item.Metadata); year != "" && !strings.Contains(title, year) {
		parts = append(parts, headerPart{styles.MutedText.Render("(" + year + ")"), 4})
	}
	if item.DiscNumber > 0 {
		parts = append(parts, headerPart{styles.MutedText.Render(fmt.Sprintf("disc %d", item.DiscNumber)), 1})
	}
	if item.Source != nil && item.Source.DurationSeconds > 0 {
		runtime := humanizeDurationLong(time.Duration(item.Source.DurationSeconds) * time.Second)
		parts = append(parts, headerPart{styles.MutedText.Render(runtime), 5})
	}
	parts = append(parts, headerPart{m.renderStatusChips(*item, styles), 1})
	if updated := parseTimestamp(item.UpdatedAt); !updated.IsZero() {
		parts = append(parts, headerPart{styles.FaintText.Render(humanizeDuration(time.Since(updated))), 3})
	}
	return joinHeaderParts(parts, m.width, styles.Band)
}

// renderInspectorTabBar renders the numbered tab bar. The Problems tab
// carries a warning glyph when the item actually has problems, so the
// operator never tabs in blind; the glyph marks presence, the label
// carries the meaning (guide: color/symbol never stands alone). The
// Episodes tab marks absence the same way: it dims further when the item
// has no episode list worth visiting (movies), keeping tab positions
// stable without advertising a dead end.
func (m Model) renderInspectorTabBar(styles Styles) string {
	item := m.getInspectedItem()
	segments := make([]string, 0, tabCount)
	for i, label := range inspectorTabLabels {
		num := fmt.Sprintf("%d", i+1)
		text := num + " " + label
		deadEpisodesTab := inspectorTab(i) == tabEpisodes && item != nil && !isEpisodicItem(*item)
		switch {
		case inspectorTab(i) == m.inspectorTab:
			text = styles.AccentText.Bold(true).Render(text)
		case deadEpisodesTab:
			text = styles.FaintText.Faint(true).Render(text)
		default:
			text = styles.FaintText.Render(text)
		}
		if inspectorTab(i) == tabProblems && item != nil && needsAttention(*item) {
			text += styles.WarningText.Render(" ⚠")
		}
		segments = append(segments, text)
	}
	return strings.Join(segments, styles.RuleText.Render("  │  "))
}

// renderEpisodesTab renders the Episodes tab content.
func (m *Model) renderEpisodesTab(item spindle.QueueItem) string {
	styles := m.theme.Styles()
	episodes, totals := item.EpisodeSnapshot()
	if len(episodes) == 0 {
		return styles.MutedText.Render("No episodes for this item")
	}

	var b strings.Builder
	m.renderEpisodeList(&b, item, styles, totals)
	return b.String()
}
