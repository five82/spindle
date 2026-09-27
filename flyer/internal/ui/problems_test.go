package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes styling escape codes so assertions can match on plain text.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

func TestRenderStructuredProblems_LeadsWithFailedTask(t *testing.T) {
	m := &Model{theme: GetTheme("slate")}
	styles := m.theme.Styles()

	item := &spindle.QueueItem{
		NeedsReview:   true,
		ReviewReasons: []string{"subtitle no-match"},
		Tasks: []spindle.Task{
			{Type: "encoding", State: "failed", Attempts: 3, Error: "ffmpeg exited 1"},
		},
	}

	var b strings.Builder
	m.renderStructuredProblems(&b, item, styles)
	got := stripANSI(b.String())

	failedIdx := strings.Index(got, "Failed Task")
	reviewIdx := strings.Index(got, "Review Reasons")
	if failedIdx == -1 {
		t.Fatalf("renderStructuredProblems() missing Failed Task header, got %q", got)
	}
	if reviewIdx == -1 {
		t.Fatalf("renderStructuredProblems() missing Review Reasons header, got %q", got)
	}
	if failedIdx > reviewIdx {
		t.Fatalf("Failed Task section should precede Review Reasons: got %q", got)
	}
	if !strings.Contains(got, "Encoding") {
		t.Fatalf("renderStructuredProblems() missing task label, got %q", got)
	}
	if !strings.Contains(got, "attempt 3") {
		t.Fatalf("renderStructuredProblems() missing attempts, got %q", got)
	}
	if !strings.Contains(got, "ffmpeg exited 1") {
		t.Fatalf("renderStructuredProblems() missing task error, got %q", got)
	}
}

func TestRenderStructuredProblems_FailedAtStageFallbackWhenTasksAbsent(t *testing.T) {
	m := &Model{theme: GetTheme("slate")}
	styles := m.theme.Styles()

	item := &spindle.QueueItem{
		FailedAtStage: "ripping",
	}

	var b strings.Builder
	m.renderStructuredProblems(&b, item, styles)
	got := stripANSI(b.String())

	if !strings.Contains(got, "Failed Task") {
		t.Fatalf("renderStructuredProblems() missing fallback Failed Task header, got %q", got)
	}
	if !strings.Contains(got, "Ripping") {
		t.Fatalf("renderStructuredProblems() missing fallback stage label, got %q", got)
	}
}

func TestRenderStructuredProblems_NoFailedTaskComposesAsBefore(t *testing.T) {
	m := &Model{theme: GetTheme("slate")}
	styles := m.theme.Styles()

	item := &spindle.QueueItem{
		NeedsReview:   true,
		ReviewReasons: []string{"subtitle no-match"},
		Tasks: []spindle.Task{
			{Type: "encoding", State: "done"},
		},
	}

	var b strings.Builder
	m.renderStructuredProblems(&b, item, styles)
	got := stripANSI(b.String())

	if strings.Contains(got, "Failed Task") {
		t.Fatalf("renderStructuredProblems() should not show Failed Task section, got %q", got)
	}
	if !strings.HasPrefix(strings.TrimLeft(got, " "), "Review Reasons") {
		t.Fatalf("renderStructuredProblems() should lead with Review Reasons as before, got %q", got)
	}
}

// TestHandleProblemsLogBatchDedupesOverlappingSeq mirrors
// TestHandleLogBatchDedupesOverlappingSeq in logs_test.go: overlapping
// batches from the streaming API must not produce duplicate rows.
func TestHandleProblemsLogBatchDedupesOverlappingSeq(t *testing.T) {
	m := &Model{theme: GetTheme("slate")}
	m.problemsState.lastItemID = 7

	m.handleProblemsLogBatch(problemsLogBatchMsg{
		itemID: 7,
		next:   3,
		events: []spindle.LogEvent{{Sequence: 1}, {Sequence: 2}, {Sequence: 3}},
	})
	if got := len(m.problemsState.logLines); got != 3 {
		t.Fatalf("logLines len after first batch = %d, want 3", got)
	}

	// Overlapping batch: seq 2 and 3 were already appended, only 4 is new.
	m.handleProblemsLogBatch(problemsLogBatchMsg{
		itemID: 7,
		next:   4,
		events: []spindle.LogEvent{{Sequence: 2}, {Sequence: 3}, {Sequence: 4}},
	})
	if got := len(m.problemsState.logLines); got != 4 {
		t.Fatalf("logLines len after overlapping batch = %d, want 4 (dedup should drop seq<=3)", got)
	}
	if last := m.problemsState.logLines[len(m.problemsState.logLines)-1]; last.Sequence != 4 {
		t.Fatalf("last appended seq = %d, want 4", last.Sequence)
	}
}

// TestStyleLogEventHighlightsErrorHint verifies that when highlightErrorHint
// is set (as the problems view does), the error_hint field renders with the
// warning/danger style matching the event's level, distinguishing it from
// plain fields.
func TestStyleLogEventHighlightsErrorHint(t *testing.T) {
	theme := GetTheme("slate")
	styles := theme.Styles()
	m := &Model{theme: theme}

	evt := spindle.LogEvent{
		Level:   "error",
		Message: "encode failed",
		Fields:  map[string]string{"error_hint": "check disk space"},
	}

	plain := m.styleLogEvent(evt, styles, false)
	prominent := m.styleLogEvent(evt, styles, true)

	if stripANSI(plain) != stripANSI(prominent) {
		t.Fatalf("highlightErrorHint should not change rendered text, only styling: plain=%q prominent=%q", stripANSI(plain), stripANSI(prominent))
	}
	if plain == prominent {
		t.Fatalf("highlightErrorHint=true should style error_hint differently than highlightErrorHint=false")
	}
}
