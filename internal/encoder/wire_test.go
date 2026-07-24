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

	"github.com/five82/reel"

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
	rep.EncodingStarted(1234)
	rep.EncodingProgress(reel.ProgressSnapshot{Percent: 42.5, FPS: 60, ETA: 90 * time.Second, CurrentFrame: 524, TotalFrames: 1234})
	rep.Warning("test warning")
	// reporter.ValidationStep lives in reel's internal package (only the
	// summary is aliased), so construct it through JSON -- which is exactly
	// what the wire does.
	var validation reel.ValidationSummary
	if err := json.Unmarshal([]byte(`{"Passed":true,"Steps":[{"Name":"duration","Passed":true}]}`), &validation); err != nil {
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
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	sess.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	daemonRep := newSpindleReporter(sess, sess.Logger, "s01_001", 0, 1)
	daemonRep.now = func() time.Time { return time.Now().Add(time.Hour) } // defeat throttle

	var result *reel.Result
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
	}

	if result == nil {
		t.Fatal("result event not delivered")
	}
	if result.OutputFile != "/out/in.mkv" || !result.ValidationPassed || result.SizeReductionPercent != 60 {
		t.Fatalf("result round-trip mismatch: %+v", result)
	}

	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	snap, err := encodingstate.Unmarshal(got.EncodingDetailsJSON)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.InputFile != "in.mkv" || snap.Resolution != "1920x1080" {
		t.Fatalf("initialization not applied: %+v", snap)
	}
	if snap.Encoder != "svt-av1" || snap.AudioCodec != "opus" {
		t.Fatalf("config not applied: %+v", snap)
	}
	if snap.TotalFrames != 1234 {
		t.Fatalf("encoding started not applied: %+v", snap)
	}
	if snap.Warning != "test warning" {
		t.Fatalf("warning not applied: %+v", snap)
	}
	if snap.Validation == nil || !snap.Validation.Passed {
		t.Fatalf("validation not applied: %+v", snap)
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
