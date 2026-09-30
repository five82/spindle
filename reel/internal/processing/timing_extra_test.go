package processing

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/reporter"
)

func TestPhaseTrackerClosesPreviousPhaseAndEndIsIdempotent(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tracker := newPhaseTracker(nil, reporter.NullReporter{}, log)
	tracker.end()
	tracker.start("Analyze")
	tracker.start("Encode")
	tracker.end()
	tracker.end()
	stop := startStep(log, "Audio")
	stop()
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("phase logs: %v", lines)
	}
	for i, want := range []string{"phase=Analyze", "phase=Encode", "phase=Audio"} {
		if !strings.Contains(lines[i], `msg="phase finished" `+want) || !strings.Contains(lines[i], "duration_seconds=") {
			t.Errorf("line %d: %q", i, lines[i])
		}
	}
}
