package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// inspectorTabLabels are the tab bar entries in tab order.
var inspectorTabLabels = [tabCount]string{"Overview", "Episodes", "Problems", "Logs", "Events"}

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
	case tabEvents:
		if item != nil && item.ID != m.itemEvents.itemID {
			m.itemEvents = itemEventState{itemID: item.ID}
		}
		m.inspectorViewport.GotoBottom() // open on the newest events
		m.updateInspectorViewport()
		return m, m.fetchItemEvents(item)
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

	case key.Matches(msg, m.keys.Tab5):
		return m.switchInspectorTab(tabEvents)

	case key.Matches(msg, m.keys.ToggleDetails) && m.inspectorTab == tabEpisodes:
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

// toggleInspectedEpisodes toggles secondary file evidence for the
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

	// Events follow the newest line while the operator stays at the bottom.
	follow := m.inspectorTab == tabEvents && m.inspectorViewport.AtBottom()
	anchor := ""
	if m.inspectorViewport.YOffset() > 0 {
		anchor = strings.TrimSpace(ansi.Strip(strings.Split(m.inspectorViewport.View(), "\n")[0]))
	}
	var content string
	switch m.inspectorTab {
	case tabEpisodes:
		content = m.renderEpisodesTab(*item)
	case tabProblems:
		content = m.renderItemProblems(item)
	case tabEvents:
		content = m.renderItemEvents()
	default:
		content = m.renderDetailContent(*item, inner)
	}
	if m.snapshot.LastError != nil {
		content = "Stale queue snapshot; last successful fetch " + m.snapshot.LastUpdated.Format(time.RFC3339) + "\n" + content
	}
	m.inspectorViewport.SetContent(content)
	if follow {
		m.inspectorViewport.GotoBottom()
	} else if anchor != "" {
		for i, line := range strings.Split(content, "\n") {
			if strings.TrimSpace(ansi.Strip(line)) == anchor {
				m.inspectorViewport.SetYOffset(i)
				break
			}
		}
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

	// Panels stay untitled: the highlighted tab already names them.
	if m.inspectorTab == tabLogs {
		b.WriteString(renderPanel("", m.logViewport.View(), "", m.width, styles))
		b.WriteString("\n")
		b.WriteString(m.renderLogStatus(styles))
	} else {
		vp := m.inspectorViewport
		footer := scrollRangeFooter(vp.YOffset(), min(vp.YOffset()+vp.Height(), vp.TotalLineCount()), vp.TotalLineCount(), vp.Height())
		b.WriteString(renderPanel("", m.inspectorViewport.View(), footer, m.width, styles))
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
		return prefix + styles.AccentText.Bold(true).Render(fmt.Sprintf("#%d", m.inspectedID)) +
			styles.MutedText.Render(" (gone)")
	}

	identity := prefix + styles.AccentText.Bold(true).Render(fmt.Sprintf("#%d", item.ID))
	title := composeTitle(*item)
	if title != fmt.Sprintf("ID #%d", item.ID) { // composeTitle's untitled fallback
		identity += " " + styles.Text.Bold(true).Render(title)
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
	// The header clock already dates a fresh snapshot; only a stale one
	// needs its age called out here.
	if updated := m.snapshot.LastUpdated; m.snapshot.LastError != nil && !updated.IsZero() {
		parts = append(parts, headerPart{styles.WarningText.Render("stale: fetched " + humanizeDuration(time.Since(updated))), 3})
	}
	return joinHeaderParts(parts, m.width, styles.Band)
}

// renderInspectorTabBar renders the numbered tab bar. The active tab is
// reverse video, so it reads without color. Episodes and Problems carry
// counts so the operator never tabs in blind; Problems adds a warning
// glyph when the item has any (the label carries the meaning, the glyph
// marks presence). Movies relabel Episodes as File, keeping tab positions
// stable without advertising a dead end.
func (m Model) renderInspectorTabBar(styles Styles) string {
	item := m.getInspectedItem()
	segments := make([]string, 0, tabCount)
	for i, label := range inspectorTabLabels {
		tab := inspectorTab(i)
		suffix := ""
		if item != nil {
			switch tab {
			case tabEpisodes:
				if !isEpisodicItem(*item) {
					label = "File"
				} else if episodes, _ := item.EpisodeSnapshot(); len(episodes) > 0 {
					label += fmt.Sprintf(" (%d)", len(episodes))
				}
			case tabProblems:
				if n := len(itemProblems(*item)); n > 0 {
					label += fmt.Sprintf(" (%d)", n)
					suffix = styles.WarningText.Render("⚠ ")
				}
			}
		}
		text := fmt.Sprintf(" %d %s ", i+1, label)
		if tab == m.inspectorTab {
			text = styles.AccentText.Bold(true).Reverse(true).Render(text)
		} else {
			text = styles.MutedText.Render(text)
		}
		segments = append(segments, text+suffix)
	}
	return strings.Join(segments, styles.RuleText.Render("│"))
}

// renderEpisodesTab renders the Episodes tab content.
func (m *Model) renderEpisodesTab(item spindle.QueueItem) string {
	styles := m.theme.Styles()
	episodes, totals := item.EpisodeSnapshot()
	if len(episodes) == 0 {
		return styles.MutedText.Render("No selected files recorded yet")
	}

	var b strings.Builder
	m.renderEpisodeList(&b, item, styles, totals)
	return b.String()
}
