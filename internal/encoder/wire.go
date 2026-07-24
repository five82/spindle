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
	"strings"
	"sync"
	"time"

	"github.com/five82/reel"
)

// The encode worker re-executes this binary, runs Reel in the child, and
// forwards reporter callbacks as JSON lines. The daemon replays the events
// into spindleReporter so persistence and logging stay daemon-owned, while a
// Reel/cgo crash kills only the file's worker process.

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
	wireWarning            = "warning"
	wireVerbose            = "verbose"
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
func (r *wireReporter) Warning(message string)                  { r.w.emit(wireWarning, wireMessage{Message: message}) }
func (r *wireReporter) Verbose(message string)                  { r.w.emit(wireVerbose, wireMessage{Message: message}) }
func (r *wireReporter) Error(e reel.ReporterError)              { r.w.emit(wireError, e) }

// RunWorker is the `spindle encode-worker` entry point: encode one file in
// this process and stream reporter events to out as JSON lines, ending with
// a result or failure event.
func RunWorker(ctx context.Context, input, outputDir string, out io.Writer) error {
	w := &wireWriter{enc: json.NewEncoder(out)}

	enc, err := reel.New(reel.WithQualityMode("target"))
	if err != nil {
		w.emit(wireFailure, wireMessage{Message: fmt.Sprintf("create reel encoder: %v", err)})
		return err
	}

	result, err := enc.EncodeWithReporter(ctx, input, outputDir, &wireReporter{w: w})
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
	case wireWarning:
		var s wireMessage
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.Warning(s.Message)
	case wireVerbose:
		var s wireMessage
		if err := json.Unmarshal(ev.Payload, &s); err != nil {
			return nil, "", err
		}
		rep.Verbose(s.Message)
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
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 500 {
			detail = "..." + detail[len(detail)-500:]
		}
		return nil, fmt.Errorf("encode worker exited: %w (stderr: %s)", waitErr, detail)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("encode worker stream: %w", scanErr)
	}
	if result == nil {
		return nil, fmt.Errorf("encode worker produced no result")
	}
	return result, nil
}
