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
	itemID int64
	cursor int64
	events []spindle.ItemEvent
}

type itemEventBatchMsg struct {
	itemID int64
	batch  spindle.ItemEventBatch
}

type itemEventErrorMsg struct{ err error }

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
			return itemEventErrorMsg{err: err}
		}
		return itemEventBatchMsg{itemID: itemID, batch: batch}
	}
}

func (m *Model) handleItemEventBatch(msg itemEventBatchMsg) {
	if !m.inspecting || m.inspectorTab != tabEvents || msg.itemID != m.inspectedID || msg.itemID != m.itemEvents.itemID {
		return
	}
	follow := m.inspectorViewport.AtBottom()
	changed := false
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
		m.itemEvents.events = trimLogBuffer(m.itemEvents.events, logBufferLimit)
		m.updateInspectorViewport()
		if follow {
			m.inspectorViewport.GotoBottom()
		}
	}
}

func (m *Model) renderItemEvents() string {
	styles := m.theme.Styles()
	if len(m.itemEvents.events) == 0 {
		return styles.MutedText.Render("No stage events yet")
	}
	var b strings.Builder
	for i, event := range m.itemEvents.events {
		if i > 0 {
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
		case "stage_complete":
			label = "completed"
		case "encoding_substage":
			label = event.Substage
		}
		fmt.Fprintf(&b, "%s %s %s", styles.FaintText.Render(ts),
			styles.AccentText.Render(event.Stage), styles.Text.Render(label))
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
	return b.String()
}
