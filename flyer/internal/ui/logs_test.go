package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/five82/flyer/internal/spindle"
)

// stripANSI is defined in problems_test.go and reused here to check styled
// output for plain-text content.

func sampleLogEvent() spindle.LogEvent {
	return spindle.LogEvent{
		Sequence:  1,
		Timestamp: "2026-07-05T12:34:56Z",
		Level:     "warn",
		Message:   "disc read retry",
		Component: "ripper",
		Stage:     "ripping",
		ItemID:    42,
		Fields: map[string]string{
			"Attempt": "2",
			"Drive":   "/dev/sr0",
		},
	}
}

func TestLogEventTimestampFallsBackToRawWhenUnparsable(t *testing.T) {
	evt := spindle.LogEvent{Timestamp: "not-a-time"}
	if got := logEventTimestamp(evt); got != "not-a-time" {
		t.Fatalf("logEventTimestamp() = %q, want raw fallback %q", got, "not-a-time")
	}
}

func TestLogEventTimestampUsesParsedLocalTime(t *testing.T) {
	evt := sampleLogEvent()
	want := evt.ParsedTime().In(time.Local).Format("2006-01-02 15:04:05")
	if got := logEventTimestamp(evt); got != want {
		t.Fatalf("logEventTimestamp() = %q, want %q", got, want)
	}
}

func TestFormatLogEventIncludesComponentAndFields(t *testing.T) {
	evt := sampleLogEvent()
	text := formatLogEvent(evt)

	for _, want := range []string{"WARN", "[ripper]", "ID #42 (ripping)", "disc read retry", "Attempt=2", "Drive=/dev/sr0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatLogEvent() = %q, missing %q", text, want)
		}
	}
}

// TestStyleLogEventMatchesPlainTextContent verifies that the styled line
// built directly from the structured event carries the same visual content
// (level, subject, message, fields) as the regex era, minus the component
// tag which the log viewer intentionally does not display (the stage is
// already surfaced via the subject).
func TestStyleLogEventMatchesPlainTextContent(t *testing.T) {
	theme := GetTheme("Nightfox")
	styles := theme.Styles()
	m := &Model{theme: theme}

	evt := sampleLogEvent()
	styled := stripANSI(m.styleLogEvent(evt, styles, false))

	for _, want := range []string{
		logEventTimestamp(evt),
		"WARN",
		"ID #42 (ripping)",
		"disc read retry",
		"- Attempt: 2",
		"- Drive: /dev/sr0",
	} {
		if !strings.Contains(styled, want) {
			t.Fatalf("styleLogEvent() = %q, missing %q", styled, want)
		}
	}

	if strings.Contains(styled, "[ripper]") {
		t.Fatalf("styleLogEvent() = %q, should not render the [component] tag (stage already shown)", styled)
	}
}

func TestStyleLogEventUppercasesLevel(t *testing.T) {
	theme := GetTheme("Nightfox")
	styles := theme.Styles()
	m := &Model{theme: theme}

	evt := spindle.LogEvent{Level: "info", Message: "hello"}
	styled := stripANSI(m.styleLogEvent(evt, styles, false))
	if !strings.Contains(styled, "INFO") {
		t.Fatalf("styleLogEvent() = %q, want level rendered as INFO", styled)
	}
}

func TestStyleLogEventOmitsSubjectAndMessageWhenEmpty(t *testing.T) {
	theme := GetTheme("Nightfox")
	styles := theme.Styles()
	m := &Model{theme: theme}

	evt := spindle.LogEvent{Level: "info"}
	styled := stripANSI(m.styleLogEvent(evt, styles, false))
	if strings.Contains(styled, "–") {
		t.Fatalf("styleLogEvent() = %q, should not render a message separator with no message", styled)
	}
}

func TestTrimLogBufferGenericOverStringsAndEvents(t *testing.T) {
	strs := []string{"a", "b", "c", "d"}
	got := trimLogBuffer(strs, 2)
	if want := []string{"c", "d"}; !equalStringSlices(got, want) {
		t.Fatalf("trimLogBuffer(strings) = %v, want %v", got, want)
	}

	events := []spindle.LogEvent{
		{Sequence: 1}, {Sequence: 2}, {Sequence: 3}, {Sequence: 4},
	}
	gotEvents := trimLogBuffer(events, 2)
	if len(gotEvents) != 2 || gotEvents[0].Sequence != 3 || gotEvents[1].Sequence != 4 {
		t.Fatalf("trimLogBuffer(events) = %+v, want last 2 entries (seq 3, 4)", gotEvents)
	}

	// Under the limit: unchanged.
	under := trimLogBuffer(strs[:1], 5)
	if !equalStringSlices(under, strs[:1]) {
		t.Fatalf("trimLogBuffer() under limit = %v, want unchanged %v", under, strs[:1])
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFindSearchMatchesAgainstStructuredEvents verifies that log search still
// matches against the plain-text form of each structured event now that
// rawLines stores spindle.LogEvent rather than pre-formatted strings.
func TestFindSearchMatchesAgainstStructuredEvents(t *testing.T) {
	m := &Model{}
	m.logState.rawLines = []spindle.LogEvent{
		{Message: "starting rip"},
		{Message: "disc read retry", ItemID: 42, Stage: "ripping"},
		{Message: "encode complete"},
	}
	m.logState.searchRegex = regexp.MustCompile("(?i)retry")

	m.findSearchMatches()

	if len(m.logState.searchMatches) != 1 || m.logState.searchMatches[0] != 1 {
		t.Fatalf("searchMatches = %v, want [1]", m.logState.searchMatches)
	}
}

// TestOrderedFieldKeys verifies known structured-log keys sort first in the
// given priority order, followed by any remaining keys sorted alphabetically.
func TestOrderedFieldKeys(t *testing.T) {
	fields := map[string]string{
		"stage_duration":  "1.2s",
		"zzz_extra":       "z",
		"aaa_extra":       "a",
		"decision_reason": "cache hit",
		"decision_type":   "cache",
		"error_hint":      "check disk",
		"decision_result": "hit",
	}

	got := orderedFieldKeys(fields)
	want := []string{
		"decision_type", "decision_result", "decision_reason",
		"error_hint", "stage_duration",
		"aaa_extra", "zzz_extra",
	}
	if !equalStringSlices(got, want) {
		t.Fatalf("orderedFieldKeys() = %v, want %v", got, want)
	}
}

func TestOrderedFieldKeys_EmptyMap(t *testing.T) {
	if got := orderedFieldKeys(nil); got != nil {
		t.Fatalf("orderedFieldKeys(nil) = %v, want nil", got)
	}
	if got := orderedFieldKeys(map[string]string{}); got != nil {
		t.Fatalf("orderedFieldKeys(empty) = %v, want nil", got)
	}
}

// TestHandleLogBatchDedupesOverlappingSeq verifies that handleLogBatch drops
// events whose Seq was already appended, guarding against duplicate or
// overlapping batches from the streaming API.
func TestHandleLogBatchDedupesOverlappingSeq(t *testing.T) {
	m := &Model{theme: GetTheme("Nightfox")}

	m.handleLogBatch(logBatchMsg{
		source: logSourceDaemon,
		next:   3,
		events: []spindle.LogEvent{{Sequence: 1}, {Sequence: 2}, {Sequence: 3}},
	})
	if got := len(m.logState.rawLines); got != 3 {
		t.Fatalf("rawLines len after first batch = %d, want 3", got)
	}

	// Overlapping batch: seq 2 and 3 were already appended, only 4 is new.
	m.handleLogBatch(logBatchMsg{
		source: logSourceDaemon,
		next:   4,
		events: []spindle.LogEvent{{Sequence: 2}, {Sequence: 3}, {Sequence: 4}},
	})
	if got := len(m.logState.rawLines); got != 4 {
		t.Fatalf("rawLines len after overlapping batch = %d, want 4 (dedup should drop seq<=3)", got)
	}
	if last := m.logState.rawLines[len(m.logState.rawLines)-1]; last.Sequence != 4 {
		t.Fatalf("last appended seq = %d, want 4", last.Sequence)
	}
}
