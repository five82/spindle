package encoder

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/reel"
)

// The encode worker re-executes this binary, runs Reel in the child, and
// forwards reporter callbacks and Reel's structured log records as JSON
// lines. The daemon replays the events into spindleReporter so persistence
// and logging stay daemon-owned (replayed records gain the stage's item,
// stage, and episode attribution), while a Reel/cgo crash kills only the
// file's worker process.

type wireEvent struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

const (
	wireInitialization     = "initialization"
	wireStageProgress      = "stage_progress"
	wireCropResult         = "crop_result"
	wireEncodingConfig     = "encoding_config"
	wireEncodingStarted    = "encoding_started"
	wireEncodingProgress   = "encoding_progress"
	wireValidationComplete = "validation_complete"
	wireEncodingComplete   = "encoding_complete"
	wireLog                = "log"
	wireError              = "error"
	wireResult             = "result"
	wireFailure            = "failure"
)

type wireStarted struct {
	TotalFrames uint64 `json:"total_frames"`
}

type wireMessage struct {
	Message string `json:"message"`
}

// wireRecord is one Reel slog record. Attributes stay an ordered list so the
// replayed line reads like the original.
type wireRecord struct {
	Level slog.Level `json:"level"`
	Msg   string     `json:"msg"`
	Attrs []wireAttr `json:"attrs,omitempty"`
}

type wireAttr struct {
	Key   string `json:"k"`
	Value any    `json:"v"`
}

// wireLogHandler is the worker's slog handler: it forwards every Reel record
// to the daemon, which applies the configured file level on replay.
type wireLogHandler struct {
	w      *wireWriter
	attrs  []wireAttr
	prefix string
}

func (h *wireLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *wireLogHandler) Handle(_ context.Context, r slog.Record) error {
	rec := wireRecord{Level: r.Level, Msg: r.Message, Attrs: append([]wireAttr(nil), h.attrs...)}
	r.Attrs(func(a slog.Attr) bool {
		rec.Attrs = appendWireAttr(rec.Attrs, h.prefix, a)
		return true
	})
	h.w.emit(wireLog, rec)
	return nil
}

func (h *wireLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &wireLogHandler{w: h.w, attrs: append([]wireAttr(nil), h.attrs...), prefix: h.prefix}
	for _, a := range attrs {
		next.attrs = appendWireAttr(next.attrs, h.prefix, a)
	}
	return next
}

func (h *wireLogHandler) WithGroup(name string) slog.Handler {
	return &wireLogHandler{w: h.w, attrs: h.attrs, prefix: h.prefix + name + "."}
}

// appendWireAttr flattens groups and reduces values to JSON-stable forms:
// errors, durations, and times would otherwise marshal as {} or nanoseconds.
func appendWireAttr(out []wireAttr, prefix string, a slog.Attr) []wireAttr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		for _, ga := range v.Group() {
			out = appendWireAttr(out, prefix+a.Key+".", ga)
		}
		return out
	case slog.KindDuration, slog.KindTime:
		return append(out, wireAttr{Key: prefix + a.Key, Value: v.String()})
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return append(out, wireAttr{Key: prefix + a.Key, Value: err.Error()})
		}
	}
	if a.Key == "" {
		return out
	}
	return append(out, wireAttr{Key: prefix + a.Key, Value: v.Any()})
}

// wireWriter serializes events to the worker's stdout. Reel invokes
// reporter callbacks from multiple goroutines, so emission is locked.
type wireWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func (w *wireWriter) emit(event string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.enc.Encode(wireEvent{Event: event, Payload: raw})
}

// wireReporter forwards the reporter callbacks spindle consumes; everything
// else stays a NullReporter no-op, mirroring spindleReporter's surface.
type wireReporter struct {
	reel.NullReporter
	w *wireWriter
}

func (r *wireReporter) Initialization(s reel.InitializationSummary) { r.w.emit(wireInitialization, s) }
func (r *wireReporter) StageProgress(s reel.StageProgress)          { r.w.emit(wireStageProgress, s) }
func (r *wireReporter) CropResult(s reel.CropSummary)               { r.w.emit(wireCropResult, s) }
func (r *wireReporter) EncodingConfig(s reel.EncodingConfigSummary) { r.w.emit(wireEncodingConfig, s) }
func (r *wireReporter) EncodingStarted(totalFrames uint64) {
	r.w.emit(wireEncodingStarted, wireStarted{TotalFrames: totalFrames})
}
func (r *wireReporter) EncodingProgress(p reel.ProgressSnapshot) { r.w.emit(wireEncodingProgress, p) }
func (r *wireReporter) ValidationComplete(s reel.ValidationSummary) {
	r.w.emit(wireValidationComplete, s)
}
func (r *wireReporter) EncodingComplete(s reel.EncodingOutcome) { r.w.emit(wireEncodingComplete, s) }
func (r *wireReporter) Error(e reel.ReporterError)              { r.w.emit(wireError, e) }

// RunWorker is the `spindle encode-worker` entry point: encode one file in
// this process and stream reporter events to out as JSON lines, ending with
// a result or failure event.
func RunWorker(ctx context.Context, input, outputDir string, out io.Writer) error {
	w := &wireWriter{enc: json.NewEncoder(out)}

	enc, err := reel.New(reel.WithQualityMode("target"), reel.WithLogger(slog.New(&wireLogHandler{w: w})))
	if err != nil {
		w.emit(wireFailure, wireMessage{Message: fmt.Sprintf("create reel encoder: %v", err)})
		return err
	}

	result, err := enc.Encode(ctx, input, outputDir, &wireReporter{w: w})
	if err != nil {
		w.emit(wireFailure, wireMessage{Message: err.Error()})
		return err
	}
	w.emit(wireResult, result)
	return nil
}

// dispatchWireEvent replays one worker event into the daemon-side reporter.
// It returns the final result or failure message when the event carries one.
func dispatchWireEvent(ev wireEvent, rep *spindleReporter) (*reel.Result, string, error) {
	switch ev.Event {
	case wireInitialization:
		var s reel.InitializationSummary
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.Initialization(s)
	case wireStageProgress:
		var s reel.StageProgress
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.StageProgress(s)
	case wireCropResult:
		var s reel.CropSummary
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.CropResult(s)
	case wireEncodingConfig:
		var s reel.EncodingConfigSummary
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.EncodingConfig(s)
	case wireEncodingStarted:
		var s wireStarted
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.EncodingStarted(s.TotalFrames)
	case wireEncodingProgress:
		var s reel.ProgressSnapshot
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.EncodingProgress(s)
	case wireValidationComplete:
		var s reel.ValidationSummary
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.ValidationComplete(s)
	case wireEncodingComplete:
		var s reel.EncodingOutcome
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.EncodingComplete(s)
	case wireLog:
		var rec wireRecord
		if err := json.Unmarshal(ev.Payload, &rec); err != nil {
			return nil, "", err
		}
		rep.Log(rec)
	case wireError:
		var e reel.ReporterError
		if err := json.Unmarshal(ev.Payload, &e); err != nil {
			return nil, "", err
		}
		rep.Error(e)
	case wireResult:
		var result reel.Result
		if err := json.Unmarshal(ev.Payload, &result); err != nil {
			return nil, "", err
		}
		return &result, "", nil
	case wireFailure:
		var s wireMessage
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		return nil, s.Message, nil
	}
	// Unknown events are ignored: a newer worker may emit more than an
	// older reader understands, and vice versa (same binary in practice).
	return nil, "", nil
}

// runWorkerProcess spawns the encode worker for one file and replays its
// event stream into the daemon-side reporter. The worker is this same
// binary, so versions cannot skew.
func runWorkerProcess(ctx context.Context, logger *slog.Logger, input, outputDir string, rep *spindleReporter) (*reel.Result, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve spindle binary: %w", err)
	}

	cmd := exec.CommandContext(ctx, exe, "encode-worker", "--input", input, "--output-dir", outputDir)
	cmd.WaitDelay = 10 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("encode worker stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start encode worker: %w", err)
	}
	logger.Info("encode worker started",
		"event_type", "encode_worker_start",
		"pid", cmd.Process.Pid,
		"input", input,
	)

	var result *reel.Result
	var failureMsg string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var ev wireEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			logger.Warn("unparseable encode worker event",
				"event_type", "encode_worker_event_error",
				"error_hint", err.Error(),
				"impact", "one progress event dropped",
			)
			continue
		}
		res, failure, err := dispatchWireEvent(ev, rep)
		if err != nil {
			logger.Warn("encode worker event dispatch failed",
				"event_type", "encode_worker_event_error",
				"error_hint", err.Error(),
				"impact", "one progress event dropped",
			)
			continue
		}
		if res != nil {
			result = res
		}
		if failure != "" {
			failureMsg = failure
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if failureMsg != "" {
		return nil, fmt.Errorf("encode worker: %s", failureMsg)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("encode worker exited: %w (stderr: %s)", waitErr, textutil.Excerpt(stderr.Bytes()))
	}
	// Reel reports through the event stream and silences SVT-AV1, so stderr
	// from a successful worker is unexpected native or runtime output.
	if detail := textutil.Excerpt(stderr.Bytes()); detail != "" {
		logger.Warn("encode worker wrote to stderr",
			"event_type", "encode_worker_stderr",
			"error_hint", detail,
			"impact", "encode succeeded; output may indicate a native library problem",
		)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("encode worker stream: %w", scanErr)
	}
	if result == nil {
		return nil, fmt.Errorf("encode worker produced no result")
	}
	return result, nil
}
