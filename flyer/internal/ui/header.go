package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/spindle"
)

// compactWidthThreshold is the terminal width below which the UI uses compact mode.
const compactWidthThreshold = 100

// isProcessingItem reports whether an item has live scheduler work.
func isProcessingItem(item spindle.QueueItem) bool {
	return len(item.RunningTasks()) > 0
}

// headerPart is one header segment with a drop rank: when the line
// overflows, parts with the highest rank are dropped first.
type headerPart struct {
	text string
	rank int
}

type driveState uint8

const (
	driveUnknown driveState = iota
	driveAvailable
	driveBusy
	drivePaused
)

// opticalDriveState reports whether the drive can accept another disc.
func (m Model) opticalDriveState() driveState {
	sched := m.snapshot.Status.Scheduler
	if sched == nil {
		return driveUnknown
	}
	res, ok := sched.Resources["drive"]
	if !ok {
		return driveUnknown
	}
	if res.Used > 0 {
		return driveBusy
	}
	if disc := m.snapshot.Status.Disc; disc != nil && disc.Paused {
		return drivePaused
	}
	return driveAvailable
}

// renderHeader renders the top status band (Surface-filled).
func (m Model) renderHeader() string {
	styles := m.theme.BandStyles()

	if !m.snapshot.HasStatus || m.snapshot.IsOffline() {
		return m.renderConnectingHeader(styles)
	}

	compact := m.width < compactWidthThreshold
	failed, review := m.countProblemCounts()

	var parts []headerPart

	// Logo, daemon status, and drive availability are never dropped.
	parts = append(parts, headerPart{styles.Logo.Render("flyer"), 0})
	if m.snapshot.Status.Running {
		parts = append(parts, headerPart{styles.SuccessText.Render("● ON"), 0})
	} else {
		parts = append(parts, headerPart{styles.DangerText.Render("● OFF"), 0})
	}
	if drive := m.renderHeaderDriveStatus(styles); drive != "" {
		parts = append(parts, headerPart{drive, 0})
	}

	// Queue count
	parts = append(parts, headerPart{
		styles.MutedText.Render("Queue: ") + styles.Text.Render(fmt.Sprintf("%d", len(m.snapshot.Queue))),
		3,
	})

	// Failed and review counts (only shown when non-zero)
	if p := m.buildProblemCountsPart(compact, failed, review, styles); p != "" {
		parts = append(parts, headerPart{p, 2})
	}

	// Timestamp
	if timeStr := m.formatTimestamp(); timeStr != "" {
		parts = append(parts, headerPart{styles.MutedText.Render(timeStr), 4})
	}

	// Health warnings
	if healthWarning := m.formatHealthWarning(compact, styles); healthWarning != "" {
		parts = append(parts, headerPart{healthWarning, 2})
	}

	// Error indicators: keep over counts/clock but below logo.
	for _, p := range m.buildErrorParts(compact, styles) {
		parts = append(parts, headerPart{p, 1})
	}

	return padBand(joinHeaderParts(parts, m.width, styles.Band), m.width, styles.Band)
}

// renderHeaderDriveStatus renders the optical drive's operator-facing state.
// An absent scheduler drive resource means the daemon cannot report it.
func (m Model) renderHeaderDriveStatus(styles Styles) string {
	state := m.opticalDriveState()
	if state == driveUnknown {
		return ""
	}

	stateStyle := styles.AccentText
	label := "BUSY"
	switch state {
	case driveAvailable:
		stateStyle = styles.SuccessText
		label = "AVAILABLE"
	case drivePaused:
		stateStyle = styles.WarningText
		label = "PAUSED"
	}

	return styles.MutedText.Render("Drive: ") + stateStyle.Bold(true).Render(label)
}

// joinHeaderParts joins parts with two-space separators rendered in the fill
// style. When the line overflows, parts are dropped one at a time — highest
// rank first, rightmost within a rank — until it fits. Rank 0 never drops.
func joinHeaderParts(parts []headerPart, width int, fill lipgloss.Style) string {
	for headerPartsWidth(parts) > width {
		drop := -1
		for i, p := range parts {
			if p.rank > 0 && (drop == -1 || p.rank >= parts[drop].rank) {
				drop = i
			}
		}
		if drop == -1 {
			break
		}
		parts = append(parts[:drop], parts[drop+1:]...)
	}

	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = p.text
	}
	return strings.Join(texts, fill.Render("  "))
}

func headerPartsWidth(parts []headerPart) int {
	w := 0
	for i, p := range parts {
		if i > 0 {
			w += 2
		}
		w += lipgloss.Width(p.text)
	}
	return w
}

// renderConnectingHeader shows the connecting/error state as a band.
func (m Model) renderConnectingHeader(styles Styles) string {
	sep := styles.Band.Render("  ")

	if m.snapshot.LastError != nil {
		last := "soon"
		if !m.lastUpdated.IsZero() {
			last = m.lastUpdated.Format("15:04:05")
		}
		errorMsg := classifyConnectionError(m.snapshot.LastError)

		parts := []string{
			styles.Logo.Render("flyer"),
			styles.DangerText.Bold(true).Render("SPINDLE " + errorMsg),
			styles.WarningText.Bold(true).Render(m.spinnerGlyph() + " Retrying..."),
			styles.MutedText.Render(last),
		}

		// Add log path hint if config is available
		if m.config != nil {
			if logPath := m.config.DaemonLogPath(); logPath != "" {
				displayPath := truncateMiddle(logPath, 50)
				parts = append(parts,
					styles.FaintText.Render("logs ")+styles.MutedText.Render(displayPath))
			}
		}

		return padBand(strings.Join(parts, sep), m.width, styles.Band)
	}

	return padBand(styles.Logo.Render("flyer")+sep+
		styles.WarningText.Bold(true).Render(m.spinnerGlyph()+" Connecting to Spindle..."),
		m.width, styles.Band)
}

// countProcessingItems returns the number of items with running tasks.
func (m Model) countProcessingItems() int {
	count := 0
	for _, item := range m.snapshot.Queue {
		if isProcessingItem(item) {
			count++
		}
	}
	return count
}

// buildProblemCountsPart builds the failed/review counts display.
// Returns empty string when both counts are zero.
func (m Model) buildProblemCountsPart(compact bool, failed, review int, styles Styles) string {
	if failed == 0 && review == 0 {
		return ""
	}

	failedStyle := styles.MutedText
	if failed > 0 {
		failedStyle = styles.DangerText
	}
	reviewStyle := styles.MutedText
	if review > 0 {
		reviewStyle = styles.WarningText
	}

	failedLabel := "Failed:"
	reviewLabel := "Review:"
	if compact {
		failedLabel = "F:"
		reviewLabel = "R:"
	}

	return styles.MutedText.Render(failedLabel+" ") + failedStyle.Render(fmt.Sprintf("%d", failed)) +
		styles.FaintText.Render("  •  ") +
		styles.MutedText.Render(reviewLabel+" ") + reviewStyle.Render(fmt.Sprintf("%d", review))
}

// buildErrorParts builds error indicator parts for the header.
func (m Model) buildErrorParts(compact bool, styles Styles) []string {
	var parts []string

	if workflowErr := strings.TrimSpace(m.snapshot.Status.Workflow.LastError); workflowErr != "" {
		errText := truncate(workflowErr, maxLen(compact, 80, 40))
		parts = append(parts,
			styles.DangerText.Bold(true).Render("WORKFLOW")+styles.DangerText.Render(" "+errText))
	}

	if m.snapshot.LastError != nil {
		errText := truncate(fmt.Sprintf("%v", m.snapshot.LastError), maxLen(compact, 80, 40))
		parts = append(parts,
			styles.DangerText.Bold(true).Render("ERROR")+styles.DangerText.Render(" "+errText))
	}

	if m.errorMsg != "" {
		parts = append(parts,
			styles.WarningText.Bold(true).Render("!")+styles.WarningText.Render(" "+m.errorMsg))
	}

	return parts
}

// countProblemCounts returns the number of failed and review items.
func (m Model) countProblemCounts() (failed, review int) {
	for _, item := range m.snapshot.Queue {
		if strings.EqualFold(item.Stage, "failed") {
			failed++
		}
		if item.NeedsReview {
			review++
		}
	}
	return
}

// formatTimestamp formats the last update time with relative indicator.
// Uses compact format (HH:MM) when data is fresh, adds relative time when stale.
func (m Model) formatTimestamp() string {
	if m.lastUpdated.IsZero() {
		return ""
	}

	timeSince := time.Since(m.lastUpdated)

	// Fresh data: just show HH:MM
	if timeSince < time.Minute {
		return m.lastUpdated.Format("15:04")
	}

	// Stale data: show relative time
	if timeSince < time.Hour {
		return fmt.Sprintf("%s (%dm)", m.lastUpdated.Format("15:04"), int(timeSince.Minutes()))
	}
	if timeSince < 24*time.Hour {
		return fmt.Sprintf("%s (%dh)", m.lastUpdated.Format("15:04"), int(timeSince.Hours()))
	}

	// Very stale: full timestamp
	return m.lastUpdated.Format("15:04:05")
}

// formatHealthWarning formats health warnings if any.
func (m Model) formatHealthWarning(compact bool, styles Styles) string {
	var unhealthy []string
	for _, dep := range m.snapshot.Status.Dependencies {
		if !dep.Available {
			label := dep.Name
			if dep.Detail != "" {
				label += " – " + dep.Detail
			}
			unhealthy = append(unhealthy, label)
		}
	}

	if len(unhealthy) == 0 {
		return ""
	}

	detail := unhealthy[0]
	if len(unhealthy) > 1 {
		detail = fmt.Sprintf("%s +%d more", detail, len(unhealthy)-1)
	}
	detail = truncate(detail, maxLen(compact, 80, 40))

	return styles.DangerText.Bold(true).Render("HEALTH") + styles.DangerText.Render(" "+detail)
}

// classifyConnectionError returns a short description of the connection error.
func classifyConnectionError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "OFFLINE"
	case strings.Contains(msg, "no such host"):
		return "HOST NOT FOUND"
	case strings.Contains(msg, "timeout"):
		return "TIMEOUT"
	default:
		return "ERROR"
	}
}

// renderCommandBar renders the footer key strip: context-sensitive key
// hints for the focused surface, pinned to the bottom row as a
// Surface-filled band. Hints carry drop ranks so lower-priority ones vanish
// first on narrow terminals instead of being cropped mid-hint.
func (m Model) renderCommandBar() string {
	styles := m.theme.BandStyles()

	type cmd struct {
		key, desc string
		rank      int
	}
	var commands []cmd

	switch {
	case m.inspecting:
		commands = []cmd{
			{"1-4", "Tabs", 2},
			{"Tab", "Next tab", 3},
			{"j/k", "Scroll", 3},
		}
		if m.inspectorTab == tabLogs {
			followLabel := "Pause"
			if !m.logState.follow {
				followLabel = "Follow"
			}
			commands = append(commands,
				cmd{"Space", followLabel, 2},
				cmd{"/", "Search", 2},
				cmd{"f", "Filters", 3},
			)
		}
		// The episode toggle only earns a hint when there is a list to
		// toggle; movies carry a single internal episode at most.
		if m.inspectorTab == tabOverview || m.inspectorTab == tabEpisodes {
			if item := m.getInspectedItem(); item != nil && isEpisodicItem(*item) {
				commands = append(commands, cmd{"t", "Episodes", 3})
			}
		}
		commands = append(commands, cmd{"Esc", "Back", 1})

	case m.currentView == ViewLogs:
		followLabel := "Pause"
		if !m.logState.follow {
			followLabel = "Follow"
		}
		commands = []cmd{
			{"Space", followLabel, 2},
			{"/", "Search", 2},
			{"n/N", "Next/Prev", 3},
			{"f", "Filters", 3},
			{"Esc", "Queue", 1},
		}

	case m.currentView == ViewProblems:
		commands = []cmd{
			{"j/k", "Navigate", 3},
			{"Enter", "Inspect", 2},
			{"Esc", "Queue", 1},
		}

	default: // ViewQueue
		commands = []cmd{
			{"/", "Filter", 2},
			{"f", m.filterLabel(), 2}, // Shows current filter state
			{"j/k", "Navigate", 3},
			{"Enter", "Inspect", 2},
			{"i", "Item logs", 3},
			{"l", "Daemon", 3},
			{"p", "Problems", 3},
			{"r", "Refresh", 3},
		}
	}

	commands = append(commands, cmd{"q", "Quit", 1}, cmd{"?", "More", 1})

	parts := make([]headerPart, 0, len(commands)+2)
	for _, c := range commands {
		parts = append(parts, headerPart{
			styles.AccentText.Render(c.key) + styles.MutedText.Render(":"+c.desc),
			c.rank,
		})
	}

	// Show active log search pattern
	logsActive := (m.currentView == ViewLogs && !m.inspecting) || (m.inspecting && m.inspectorTab == tabLogs)
	if logsActive && m.logState.searchQuery != "" {
		pattern := truncate(m.logState.searchQuery, 18)
		parts = append(parts, headerPart{styles.AccentText.Render("/" + pattern), 2})
	}

	// Add theme indicator
	parts = append(parts, headerPart{
		styles.AccentText.Render("T") + styles.FaintText.Render(":"+m.theme.Name),
		4,
	})

	return padBand(joinHeaderParts(parts, m.width, styles.Band), m.width, styles.Band)
}

// maxLen returns compactLen if compact is true, otherwise normalLen.
func maxLen(compact bool, normalLen, compactLen int) int {
	if compact {
		return compactLen
	}
	return normalLen
}

// truncate truncates a string to max length with ellipsis.
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// truncateMiddle truncates a string in the middle, preserving start and end.
func truncateMiddle(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 5 {
		return s[:max]
	}
	// Keep more of the end (file name) than the start
	endLen := (max - 3) * 2 / 3
	startLen := max - 3 - endLen
	return s[:startLen] + "..." + s[len(s)-endLen:]
}
