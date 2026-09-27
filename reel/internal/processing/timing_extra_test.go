package processing

import (
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/reporter"
)

type verboseReporter struct {
	reporter.NullReporter
	messages []string
}

func (r *verboseReporter) Verbose(s string) { r.messages = append(r.messages, s) }

func TestPhaseTrackerClosesPreviousPhaseAndEndIsIdempotent(t *testing.T) {
	rep := &verboseReporter{}
	tracker := newPhaseTracker(nil, rep)
	tracker.end()
	tracker.start("Analyze")
	tracker.start("Encode")
	tracker.end()
	tracker.end()
	if len(rep.messages) != 4 {
		t.Fatalf("messages: %v", rep.messages)
	}
	for i, want := range []string{"Analyze started at ", "Analyze stopped at ", "Encode started at ", "Encode stopped at "} {
		if !strings.HasPrefix(rep.messages[i], want) {
			t.Errorf("message %d: %q", i, rep.messages[i])
		}
	}
	stop := startVerboseStep(rep, "Audio")
	stop()
	if len(rep.messages) != 6 || !strings.Contains(rep.messages[5], "(duration ") {
		t.Fatalf("verbose timing: %v", rep.messages)
	}
}
