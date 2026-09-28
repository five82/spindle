package encoder

import (
	"testing"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/reel"
)

func TestReporterPersistsReelErrorAndVerboseMessageDoesNotMutateSnapshot(t *testing.T) {
	sess := encoderSession(t, ripspec.Envelope{})
	sess.Task = &queue.Task{}
	reporter := newSpindleReporter(sess, testEncoderLogger(), "main")
	reporter.Verbose("raw encoder message")
	reporter.Error(reel.ReporterError{Title: "encode failed", Message: "bad input", Context: "chunk", Suggestion: "retry"})
	snap, err := encodingstate.Unmarshal(sess.Task.EncodingDetailsJSON)
	if err != nil || snap.Error == nil || snap.Error.Title != "encode failed" || snap.Error.Message != "bad input" || snap.Error.Context != "chunk" || snap.Error.Suggestion != "retry" {
		t.Fatalf("snapshot: %+v %v", snap, err)
	}
}
