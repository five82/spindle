package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// itemEventState follows one item's durable transition journal independently
// of the log buffer and its filters/cursor.
type itemEventState struct {
	itemID     int64
	cursor     int64
	events     []spindle.ItemEvent
	loaded     bool
	partial    bool
	fetchError error
}

type itemEventBatchMsg struct {
	itemID int64
	batch  spindle.ItemEventBatch
}

type itemEventErrorMsg struct {
	err    error
	itemID int64
}

func (m *Model) fetchItemEvents(item *spindle.QueueItem) tea.Cmd {
	if item == nil || m.client == nil {
		return nil
	}
	if item.ID != m.itemEvents.itemID {
		m.itemEvents = itemEventState{itemID: item.ID}
	}
	itemID, cursor := item.ID, m.itemEvents.cursor
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), logFetchTimeout)
		defer cancel()
		batch, err := m.client.FetchItemEvents(ctx, itemID, cursor)
		if err != nil {
			return itemEventErrorMsg{err: err, itemID: itemID}
		}
		return itemEventBatchMsg{itemID: itemID, batch: batch}
	}
}

func (m *Model) handleItemEventBatch(msg itemEventBatchMsg) {
	if !m.inspecting || m.inspectorTab != tabEvents || msg.itemID != m.inspectedID || msg.itemID != m.itemEvents.itemID {
		return
	}
	m.itemEvents.loaded, m.itemEvents.fetchError = true, nil
	m.itemEvents.partial = m.itemEvents.partial || len(msg.batch.Events) >= 500
	changed := true
	for _, event := range msg.batch.Events {
		if event.ID <= m.itemEvents.cursor {
			continue // A previous poll may have delivered this page already.
		}
		m.itemEvents.events = append(m.itemEvents.events, event)
		m.itemEvents.cursor = event.ID
		changed = true
	}
	if msg.batch.Next > m.itemEvents.cursor {
		m.itemEvents.cursor = msg.batch.Next
	}
	if changed {
		m.itemEvents.partial = m.itemEvents.partial || len(m.itemEvents.events) > logBufferLimit
		m.itemEvents.events = trimLogBuffer(m.itemEvents.events, logBufferLimit)
		m.updateInspectorViewport()
	}
}

// renderItemEvents renders the journal oldest-first (the tab follows the
// newest line). A start/end pair for one operation folds into the end row,
// which carries the duration, and every row wraps under its text column.
func (m *Model) renderItemEvents() string {
	styles := m.theme.Styles()
	var b strings.Builder
	if m.itemEvents.fetchError != nil {
		fmt.Fprintln(&b, styles.WarningText.Render("Events fetch failed: "+m.itemEvents.fetchError.Error()+"; retained history may be stale"))
	} else if !m.itemEvents.loaded {
		fmt.Fprintln(&b, styles.MutedText.Render("Loading task history"))
	}
	if m.itemEvents.partial {
		fmt.Fprintln(&b, styles.MutedText.Render("Partial history: bounded buffer/page; additional records may be available"))
	}

	// Walk backwards so each start pairs with the next end of the same
	// operation; paired starts are hidden.
	events := m.itemEvents.events
	activityKey := func(e spindle.ItemEvent) string {
		return fmt.Sprint(e.TaskID, "/", e.Attempt, "/", e.EpisodeKey, "/", e.Substage)
	}
	isStart := func(e spindle.ItemEvent) bool {
		return e.Type == "activity_running" || e.Type == "activity_waiting"
	}
	hidden := make([]bool, len(events))
	openEnds := make(map[string]int)
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		switch {
		case !strings.HasPrefix(e.Type, "activity_"):
		case isStart(e) && openEnds[activityKey(e)] > 0:
			openEnds[activityKey(e)]--
			hidden[i] = true
		case !isStart(e):
			openEnds[activityKey(e)]++
		}
	}

	now := m.clock()
	width := panelInnerWidth(m.width)
	var prevRow, prevStage string
	for i, event := range events {
		if hidden[i] {
			continue
		}
		ts := event.Time
		if parsed, err := time.Parse(time.RFC3339Nano, event.Time); err == nil {
			local := parsed.In(time.Local)
			if y, d := local.Year(), local.YearDay(); y == now.Year() && d == now.YearDay() {
				ts = local.Format("15:04:05")
			} else {
				ts = local.Format("Jan 02 15:04:05")
			}
		}
		label := strings.ReplaceAll(strings.TrimPrefix(event.Type, "stage_"), "_", " ")
		switch {
		case event.Type == "stage_start":
			label = "started"
			if event.Stage == "encoding" {
				label = "worker reserved (may wait for input)"
			}
		case event.Type == "stage_complete":
			label = "completed"
		case strings.HasPrefix(event.Type, "activity_"):
			label = event.Substage
			if state := strings.TrimPrefix(event.Type, "activity_"); state != "ended" {
				label += " " + state
			}
		}
		// Journal names mix snake_case and lowercase; render them as sentences.
		label = strings.ReplaceAll(label, "_", " ")
		if label != "" {
			label = strings.ToUpper(label[:1]) + label[1:]
		}
		// A bare row restating the previous one adds nothing.
		row := event.Stage + "|" + strings.ToLower(label) + "|" + event.EpisodeKey
		msg := strings.TrimSpace(event.Message)
		if strings.EqualFold(strings.TrimSuffix(msg, " ended"), event.Substage) {
			msg = ""
		}
		if row == prevRow && msg == "" && event.Percent <= 0 && event.DurationSeconds < 0.05 && event.Attempt <= 1 {
			continue
		}
		prevRow = row
		text := styles.Text.Render(label)
		if event.EpisodeKey != "" {
			text += " " + styles.MutedText.Render("("+event.EpisodeKey+")")
		}
		// Daemon messages often just restate the operation ("Video merge
		// ended"); keep only messages that add something.
		if msg != "" {
			text += " " + styles.FaintText.Render("-") + " " + styles.Text.Render(msg)
		}
		if event.Percent > 0 {
			text += " " + styles.MutedText.Render(fmt.Sprintf("%.1f%%", event.Percent))
		}
		if event.DurationSeconds >= 0.05 {
			text += " " + styles.MutedText.Render(formatEventDuration(event.DurationSeconds))
		}
		if event.Attempt > 1 {
			text += " " + styles.WarningText.Render(fmt.Sprintf("(run %d)", event.Attempt))
		}
		// A stage change stands out in accent; repeats within a run dim
		// rather than vanish, so a row scrolled to the top keeps its stage.
		stageStyle := styles.AccentText
		if event.Stage == prevStage {
			stageStyle = styles.FaintText
		}
		prevStage = event.Stage
		prefix := styles.FaintText.Render(ts) + " " + stageStyle.Render(fmt.Sprintf("%-12s", stageDisplay(event.Stage).label)) + " "
		indent := strings.Repeat(" ", lipgloss.Width(prefix))
		for j, line := range wrapText(text, max(width-lipgloss.Width(prefix), 20)) {
			if j == 0 {
				fmt.Fprintln(&b, prefix+line)
			} else {
				fmt.Fprintln(&b, indent+line)
			}
		}
	}
	if b.Len() == 0 {
		return styles.MutedText.Render("No stage events yet")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// formatEventDuration keeps sub-minute journal durations to a tenth of a
// second; longer ones use the shared h/m/s form.
func formatEventDuration(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	return formatDuration(time.Duration(seconds * float64(time.Second)))
}
