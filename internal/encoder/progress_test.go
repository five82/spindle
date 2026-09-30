package encoder

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/reel"
)

func TestReporterRetainsScopedCountersAndConcurrentAudio(t *testing.T) {
	sess := encoderSession(t, ripspec.Envelope{})
	rep := newSpindleReporter(sess, testEncoderLogger(), "title03")
	rep.EncodingStarted(100)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 20 {
			rep.StageProgress(reel.StageProgress{Lane: "audio", Stage: "audio", Message: "Audio processing"})
		}
	})
	wg.Go(func() {
		for range 20 {
			rep.Log(wireRecord{Level: slog.LevelWarn, Msg: "Worker concurrency reduced"})
		}
	})
	wg.Go(func() {
		for range 20 {
			rep.EncodingProgress(reel.ProgressSnapshot{CurrentFrame: 10, TotalFrames: 100, Percent: 10, FPS: 5, Speed: 2, RecentSpeed: 3, ETA: time.Minute, ChunksComplete: 2, ChunksTotal: 10, ActiveWorkers: 3, TargetWorkers: 4, MaxWorkers: 6, InFlight: 5, Probing: 2, Scoring: 1, Finishing: 1, EncodeSlotWaitSeconds: 8})
		}
	})
	wg.Wait()
	snap, err := encodingstate.Unmarshal(sess.Task.EncodingDetailsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if snap.AssetKey != "title03" || snap.CurrentFrame != 10 || snap.ChunksComplete != 2 || snap.FPS != 5 || snap.RecentSpeed != 3 || snap.EncodeSlotWaitSeconds != 8 || snap.Warning == "" {
		t.Fatalf("lost snapshot: %+v", snap)
	}
	if len(sess.Task.Activities) != 2 {
		t.Fatal(sess.Task.Activities)
	}
	rep.StageProgress(reel.StageProgress{Lane: "video", Stage: "encoding", State: "ended", Message: "Video encoding ended"})
	rep.StageProgress(reel.StageProgress{Lane: "work", Stage: "Video merge", Message: "Merging encoded chunks"})
	snap, err = encodingstate.Unmarshal(sess.Task.EncodingDetailsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ETASeconds != 0 || snap.CurrentFrame != 0 || snap.FPS != 0 || snap.ActiveWorkers != 0 {
		t.Fatalf("video telemetry leaked into merge: %+v", snap)
	}
	if a := sess.Task.Activities[0]; a.State != "ended" || a.Completed != 10 || a.Total != 100 {
		t.Fatalf("scope/duration lost on phase end: %+v", a)
	}
	rep.EncodingComplete(reel.EncodingOutcome{EncodedSize: 50})
	if a := sess.Task.Activities[len(sess.Task.Activities)-1]; a.State != "done" {
		t.Fatalf("file outcome rendered as new work: %+v", a)
	}
}
