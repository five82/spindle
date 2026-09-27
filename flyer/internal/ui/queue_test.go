package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

func TestQueueDiscColumnForTVItems(t *testing.T) {
	items := []spindle.QueueItem{{
		ID:           7,
		DisplayTitle: "Example Show Season 01",
		DiscNumber:   2,
		Stage:        "encoding",
	}}
	cols := computeQueueColumns(items, 120)
	if cols.disc != len("DISC") {
		t.Fatalf("disc column width = %d, want %d", cols.disc, len("DISC"))
	}

	m := New(Options{ThemeName: "slate"})
	m.width = 120
	styles := m.theme.Styles()
	header := stripANSI(renderQueueHeaderRow(cols, styles))
	if !strings.Contains(header, "DISC") {
		t.Fatalf("queue header missing DISC column: %q", header)
	}
	row := stripANSI(m.renderQueueRow(items[0], cols, false, styles))
	if !strings.Contains(row, "  2   ") {
		t.Fatalf("queue row missing disc number: %q", row)
	}
}

func TestQueueCompletedStripCollapses(t *testing.T) {
	doneTasks := make([]spindle.Task, 8)
	for i := range doneTasks {
		doneTasks[i].State = "done"
	}
	completed := spindle.QueueItem{ID: 10, Stage: "completed", Tasks: doneTasks}
	if got := plainTaskStrip(completed); got != "✓" {
		t.Fatalf("completed strip = %q, want single ✓", got)
	}
	if got := taskStripWidth(completed); got != 1 {
		t.Fatalf("completed strip width = %d, want 1", got)
	}

	// Failed items keep the full strip: the ✗ position carries information.
	failed := spindle.QueueItem{ID: 11, Stage: "failed", Tasks: []spindle.Task{
		{State: "done"}, {State: "failed"}, {State: "pending"},
	}}
	if got := plainTaskStrip(failed); got != "✓✗○" {
		t.Fatalf("failed strip = %q, want per-task glyphs", got)
	}
}

func TestQueueCompletedItemShowsSizeReduction(t *testing.T) {
	items := []spindle.QueueItem{{
		ID:           12,
		DisplayTitle: "Example Movie",
		Stage:        "completed",
		Encoding: &spindle.EncodingStatus{
			EncodedSize:          5 << 30,
			SizeReductionPercent: 79,
		},
	}}
	m := New(Options{ThemeName: "slate"})
	m.width = 120
	styles := m.theme.Styles()
	cols := computeQueueColumns(items, 120)
	row := stripANSI(m.renderQueueRow(items[0], cols, false, styles))
	if !strings.Contains(row, "-79%") {
		t.Fatalf("completed row missing size reduction, got %q", row)
	}
}

func TestQueueOmitsDiscColumnWithoutDiscNumbers(t *testing.T) {
	items := []spindle.QueueItem{{ID: 8, DisplayTitle: "Example Movie", Stage: "encoding"}}
	cols := computeQueueColumns(items, 120)
	if cols.disc != 0 {
		t.Fatalf("disc column width = %d, want 0", cols.disc)
	}
	if header := stripANSI(renderQueueHeaderRow(cols, New(Options{ThemeName: "slate"}).theme.Styles())); strings.Contains(header, "DISC") {
		t.Fatalf("movie-only queue unexpectedly has DISC column: %q", header)
	}
}
