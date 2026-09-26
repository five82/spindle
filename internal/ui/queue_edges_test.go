package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

func TestQueueScrollFilterPromptAndProgressEdges(t *testing.T) {
	m := newAppTestModel(t)
	m.width, m.height = 70, 8
	for i := int64(4); i <= 12; i++ {
		m.snapshot.Queue = append(m.snapshot.Queue, spindle.QueueItem{ID: i, DisplayTitle: "Later", Stage: "completed"})
	}
	m.selectedRow = 10
	m.ensureQueueVisible()
	got := stripANSI(m.renderQueue())
	if !strings.Contains(got, "of 12") || !strings.Contains(got, "#11") {
		t.Fatalf("scrolling queue = %q", got)
	}
	m.queueFilterQuery = "Later"
	if got := stripANSI(m.renderQueue()); !strings.Contains(got, "Esc to clear") || !strings.Contains(got, "Queue (9/12)") {
		t.Fatalf("applied filter: %q", got)
	}
	m.height = 3
	if got := m.queueVisibleRows(); got != 1 {
		t.Fatalf("minimum visible rows = %d", got)
	}
	m.selectedRow = 0
	m.queueScroll = 0
	m.queueFilterQuery = ""
	m.snapshot.Queue = []spindle.QueueItem{{ID: 42, DisplayTitle: "Active", Stage: "encoding", Tasks: []spindle.Task{{Type: "encoding", State: "running", Progress: spindle.TaskProgress{Percent: 150}}, {Type: "ripping", State: "pending"}}}}
	styles := m.theme.Styles()
	cols := computeQueueColumns(m.snapshot.Queue, 120)
	if got := runningTaskPercent(m.snapshot.Queue[0]); got != 100 {
		t.Fatalf("clamped running percent = %v", got)
	}
	for _, plain := range []bool{true, false} {
		got := stripANSI(m.queueProgressCell(m.snapshot.Queue[0], cols, styles.Text, styles, plain))
		if !strings.Contains(got, "100%") {
			t.Fatalf("bar progress: %q", got)
		}
	}
	m.snapshot.Queue[0].Tasks[0].Progress.Percent = 0
	if got := m.queueProgressCell(m.snapshot.Queue[0], cols, styles.Text, styles, false); got != "" {
		t.Fatalf("unknown progress = %q", got)
	}
	if got := stripANSI(m.renderTaskStrip(m.snapshot.Queue[0], styles)); got == "" {
		t.Fatal("running strip empty")
	}
	m.snapshot.Queue[0].Tasks[0].State = "failed"
	if got := stripANSI(m.renderTaskStrip(m.snapshot.Queue[0], styles)); !strings.Contains(got, "✗") {
		t.Fatalf("failed strip: %q", got)
	}
}
