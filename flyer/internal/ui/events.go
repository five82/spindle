package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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
	for _, event := range m.itemEvents.events {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		ts := event.Time
		if parsed, err := time.Parse(time.RFC3339Nano, event.Time); err == nil {
			ts = parsed.In(time.Local).Format("2006-01-02 15:04:05")
		}
		label := strings.ReplaceAll(strings.TrimPrefix(event.Type, "stage_"), "_", " ")
		switch event.Type {
		case "stage_start":
			label = "started"
			if event.Stage == "encoding" {
				label = "worker reserved (may wait for input)"
			}
		case "stage_complete":
			label = "completed"
		case "encoding_substage":
			label = event.Substage
		}
		fmt.Fprintf(&b, "%s %s %s", styles.FaintText.Render(ts),
			styles.AccentText.Render(event.Stage), styles.Text.Render(label))
		if event.TaskID != 0 {
			fmt.Fprintf(&b, " [task %d/run %d]", event.TaskID, event.Attempt)
		}
		if strings.HasPrefix(event.Type, "activity_") {
			fmt.Fprintf(&b, " %s", event.Substage)
		}
		if event.EpisodeKey != "" {
			fmt.Fprintf(&b, " %s", styles.MutedText.Render("("+event.EpisodeKey+")"))
		}
		if event.Message != "" {
			fmt.Fprintf(&b, " - %s", styles.Text.Render(event.Message))
		}
		if event.Percent > 0 {
			fmt.Fprintf(&b, " %s", styles.MutedText.Render(fmt.Sprintf("%.1f%%", event.Percent)))
		}
		if event.DurationSeconds > 0 {
			fmt.Fprintf(&b, " %s", styles.MutedText.Render(fmt.Sprintf("%.1fs", event.DurationSeconds)))
		}
	}
	if b.Len() == 0 {
		return styles.MutedText.Render("No stage events yet")
	}
	return b.String()
}
