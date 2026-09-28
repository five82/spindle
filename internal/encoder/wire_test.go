package encoder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/five82/spindle/reel"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
)

// TestWireRoundTrip drives the exact worker-to-daemon path: a wireReporter
// emits events into a buffer, the daemon-side dispatch replays them into a
// real spindleReporter backed by an in-memory queue, and the persisted
// encoding snapshot must reflect every event.
func TestWireRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := &wireWriter{enc: json.NewEncoder(&buf)}
	rep := &wireReporter{w: w}

	rep.Initialization(reel.InitializationSummary{InputFile: "in.mkv", Resolution: "1920x1080", DynamicRange: "SDR"})
	rep.EncodingConfig(reel.EncodingConfigSummary{Encoder: "svt-av1", Preset: "6", Quality: "target", AudioCodec: "opus"})
	rep.CropResult(reel.CropSummary{Crop: "crop=1920:800:0:140", Required: true})
	rep.StageProgress(reel.StageProgress{Stage: "Chunking", Message: "Detecting shot cuts"})
	rep.EncodingStarted(1234)
	rep.EncodingProgress(reel.ProgressSnapshot{Percent: 42.5, FPS: 60, ETA: 90 * time.Second, CurrentFrame: 524, TotalFrames: 1234})
	rep.Warning("test warning")
	// reporter.ValidationStep lives in reel's internal package (only the
	// summary is aliased), so construct it through JSON -- which is exactly
	// what the wire does.
	var validation reel.ValidationSummary
	if err := json.Unmarshal([]byte(`{"Passed":true,"Steps":[{"Name":"duration","Passed":true},{"Name":"Source timeline normalization","Passed":true,"Details":"1 source audio track ended 35.562s past video; output bounded to the video endpoint"}]}`), &validation); err != nil {
		t.Fatalf("build validation summary: %v", err)
	}
	rep.ValidationComplete(validation)
	rep.EncodingComplete(reel.EncodingOutcome{OriginalSize: 1000, EncodedSize: 400, TotalTime: 2 * time.Minute})
	w.emit(wireResult, reel.Result{OutputFile: "/out/in.mkv", OriginalSize: 1000, EncodedSize: 400, SizeReductionPercent: 60, ValidationPassed: true})

	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()
	item, _ := store.NewDisc("A", "fp1")
	if err := store.EnsureTasks(item, []queue.TaskSpec{{Type: queue.StageEncoding}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartTask(tasks[0]); err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, tasks[0])
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	var logBuf bytes.Buffer
	sess.Logger = slog.New(slog.NewJSONHandler(io.MultiWriter(io.Discard, &logBuf), nil))
	daemonRep := newSpindleReporter(sess, sess.Logger, "s01_001")
	daemonRep.now = func() time.Time { return time.Now().Add(time.Hour) } // defeat throttle

	var result *reel.Result
	var sawChunking bool
	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		var ev wireEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		res, failure, err := dispatchWireEvent(ev, daemonRep)
		if err != nil {
			t.Fatalf("dispatch %s: %v", ev.Event, err)
		}
		if failure != "" {
			t.Fatalf("unexpected failure event: %s", failure)
		}
		if res != nil {
			result = res
		}
		if ev.Event == wireStageProgress {
			got, getErr := store.TasksForItem(item.ID)
			if getErr != nil {
				t.Fatalf("get after stage progress: %v", getErr)
			}
			snap, snapErr := encodingstate.Unmarshal(got[0].EncodingDetailsJSON)
			if snapErr != nil {
				t.Fatalf("snapshot after stage progress: %v", snapErr)
			}
			if snap.Substage != "chunking" {
				t.Fatalf("substage after stage progress = %q, want chunking", snap.Substage)
			}
			sawChunking = true
		}
	}

	if !sawChunking {
		t.Fatal("stage progress event not delivered")
	}
	events, _, err := store.Events(item.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "activity_running" && event.EpisodeKey == "s01_001" && event.Substage == "Chunking" && event.Message == "Detecting shot cuts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing chunking transition: %+v", events)
	}
	if bytes.Contains(logBuf.Bytes(), []byte("encoding_substage")) {
		t.Fatalf("substage emitted through log: %s", logBuf.String())
	}
	if result == nil {
		t.Fatal("result event not delivered")
	}
	if result.OutputFile != "/out/in.mkv" || !result.ValidationPassed || result.SizeReductionPercent != 60 {
		t.Fatalf("result round-trip mismatch: %+v", result)
	}

	got, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	snap, err := encodingstate.Unmarshal(got[0].EncodingDetailsJSON)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.InputFile != "in.mkv" || snap.Resolution != "1920x1080" || snap.OutputResolution != "1920x800" {
		t.Fatalf("initialization or crop not applied: %+v", snap)
	}
	if snap.Encoder != "svt-av1" || snap.AudioCodec != "opus" {
		t.Fatalf("config not applied: %+v", snap)
	}
	if snap.TotalFrames != 0 || snap.ETASeconds != 0 {
		t.Fatalf("completed encode retained live counters: %+v", snap)
	}
	if snap.Warning != "test warning" {
		t.Fatalf("warning not applied: %+v", snap)
	}
	if snap.Validation == nil || !snap.Validation.Passed {
		t.Fatalf("validation not applied: %+v", snap)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte(`"decision_type":"source_timeline_normalization"`)) ||
		!bytes.Contains(logBuf.Bytes(), []byte(`"decision_result":"bounded_to_video"`)) {
		t.Fatalf("source timeline decision not logged: %s", logBuf.String())
	}
	if snap.Substage != "complete" || snap.EncodedSize != 400 {
		t.Fatalf("completion not applied: %+v", snap)
	}
}

// TestWireFailureEvent verifies the failure path round-trips.
func TestWireFailureEvent(t *testing.T) {
	var buf bytes.Buffer
	w := &wireWriter{enc: json.NewEncoder(&buf)}
	w.emit(wireFailure, wireMessage{Message: "boom"})

	var ev wireEvent
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &ev); err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, failure, err := dispatchWireEvent(ev, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if failure != "boom" {
		t.Fatalf("failure = %q, want boom", failure)
	}
}
