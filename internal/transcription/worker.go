package transcription

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/five82/spindle/internal/logs"
)

type workerClient interface {
	Transcribe(ctx context.Context, req workerTranscribeRequest) (*workerTranscribeResponse, error)
	Close() error
}

type workerConfig struct {
	ASRModel              string
	ForcedAlignerModel    string
	Device                string
	DType                 string
	UseFlashAttention     bool
	MaxInferenceBatchSize int
}

type workerCommand struct {
	ID                    string `json:"id"`
	Command               string `json:"command"`
	AudioPath             string `json:"audio_path,omitempty"`
	Language              string `json:"language,omitempty"`
	ReturnTimeStamps      bool   `json:"return_time_stamps,omitempty"`
	ASRModel              string `json:"asr_model,omitempty"`
	ForcedAlignerModel    string `json:"forced_aligner_model,omitempty"`
	Device                string `json:"device,omitempty"`
	DType                 string `json:"dtype,omitempty"`
	UseFlashAttention     bool   `json:"use_flash_attention,omitempty"`
	MaxInferenceBatchSize int    `json:"max_inference_batch_size,omitempty"`
}

type workerTimeStamp struct {
	Text      string  `json:"text"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

type workerTranscribeRequest struct {
	AudioPath        string
	Language         string
	ReturnTimeStamps bool
}

type workerTranscribeResponse struct {
	Language   string            `json:"language"`
	Text       string            `json:"text"`
	TimeStamps []workerTimeStamp `json:"time_stamps,omitempty"`
}

type subprocessWorker struct {
	runtime *Runtime
	cfg     workerConfig
	logger  *slog.Logger

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	stderr   io.ReadCloser
	running  bool
	restarts int
	seq      uint64
}

func newSubprocessWorker(runtime *Runtime, cfg workerConfig, logger *slog.Logger) *subprocessWorker {
	return &subprocessWorker{runtime: runtime, cfg: cfg, logger: logs.Default(logger)}
}

func (w *subprocessWorker) Transcribe(ctx context.Context, req workerTranscribeRequest) (*workerTranscribeResponse, error) {
	resp, err := w.transcribeOnce(ctx, req)
	if err == nil {
		return resp, nil
	}
	w.logger.Warn("transcription worker failed",
		"event_type", "transcription_worker_failure",
		"error_hint", "worker request failed",
		"impact", "retrying worker once",
		"error", err,
	)
	w.mu.Lock()
	_ = w.stopLocked()
	w.restarts++
	w.mu.Unlock()
	return w.transcribeOnce(ctx, req)
}

func (w *subprocessWorker) transcribeOnce(ctx context.Context, req workerTranscribeRequest) (*workerTranscribeResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureStartedLocked(ctx); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("req-%d", atomic.AddUint64(&w.seq, 1))
	cmd := workerCommand{
		ID:               id,
		Command:          "transcribe",
		AudioPath:        req.AudioPath,
		Language:         req.Language,
		ReturnTimeStamps: req.ReturnTimeStamps,
	}
	if err := json.NewEncoder(w.stdin).Encode(cmd); err != nil {
		return nil, fmt.Errorf("write worker request: %w", err)
	}
	return readWorkerResponse(ctx, w.stdout, id)
}

func (w *subprocessWorker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopLocked()
}

func (w *subprocessWorker) ensureStartedLocked(ctx context.Context) error {
	if w.running && w.cmd != nil && w.cmd.Process != nil {
		return nil
	}
	pythonPath, scriptPath, err := w.runtime.EnsureReady(ctx, w.cfg)
	if err != nil {
		return err
	}
	cmd := exec.Command(pythonPath, scriptPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("worker stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("worker stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("worker stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	w.cmd = cmd
	w.stdin = stdin
	w.stdout = bufio.NewReader(stdout)
	w.stderr = stderr
	w.running = true
	w.logger.Info("transcription worker started",
		"event_type", "transcription_worker_start",
		"asr_model", w.cfg.ASRModel,
		"forced_aligner_model", w.cfg.ForcedAlignerModel,
		"device", w.cfg.Device,
		"dtype", w.cfg.DType,
		"use_flash_attention", w.cfg.UseFlashAttention,
		"max_inference_batch_size", w.cfg.MaxInferenceBatchSize,
	)
	go w.logStderr(stderr)
	if err := json.NewEncoder(w.stdin).Encode(workerCommand{
		ID:                    "startup-health",
		Command:               "health",
		ASRModel:              w.cfg.ASRModel,
		ForcedAlignerModel:    w.cfg.ForcedAlignerModel,
		Device:                w.cfg.Device,
		DType:                 w.cfg.DType,
		UseFlashAttention:     w.cfg.UseFlashAttention,
		MaxInferenceBatchSize: w.cfg.MaxInferenceBatchSize,
	}); err != nil {
		return fmt.Errorf("initialize worker: %w", err)
	}
	if _, err := readWorkerHealthResponse(w.stdout, "startup-health"); err != nil {
		_ = w.stopLocked()
		return fmt.Errorf("worker startup health: %w", err)
	}
	w.logger.Info("transcription worker ready", "event_type", "transcription_worker_ready")
	go func(cmd *exec.Cmd) {
		if err := cmd.Wait(); err != nil {
			w.logger.Warn("transcription worker exited",
				"event_type", "transcription_worker_exit",
				"error_hint", "worker process exited unexpectedly",
				"impact", "next request will restart worker",
				"error", err,
			)
		}
		w.mu.Lock()
		if w.cmd == cmd {
			w.running = false
			w.cmd = nil
			w.stdin = nil
			w.stdout = nil
			w.stderr = nil
		}
		w.mu.Unlock()
	}(cmd)
	return nil
}

func (w *subprocessWorker) stopLocked() error {
	if !w.running || w.cmd == nil {
		w.running = false
		w.cmd = nil
		w.stdin = nil
		w.stdout = nil
		w.stderr = nil
		return nil
	}
	if w.stdin != nil {
		_ = json.NewEncoder(w.stdin).Encode(workerCommand{ID: "shutdown", Command: "shutdown"})
		_ = w.stdin.Close()
	}
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	w.running = false
	w.cmd = nil
	w.stdin = nil
	w.stdout = nil
	w.stderr = nil
	return nil
}

func (w *subprocessWorker) logStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		w.logger.Debug("transcription worker stderr", "line", line)
	}
}

func readWorkerHealthResponse(stdout *bufio.Reader, wantID string) (*RuntimeStatus, error) {
	for {
		line, err := stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read worker response: %w", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(line, &generic); err != nil {
			return nil, fmt.Errorf("decode worker response: %w", err)
		}
		id, _ := generic["id"].(string)
		if id != wantID {
			continue
		}
		if ok, _ := generic["ok"].(bool); !ok {
			return nil, fmt.Errorf("worker error: %v", generic["error"])
		}
		return &RuntimeStatus{Ready: true}, nil
	}
}

func readWorkerResponse(ctx context.Context, stdout *bufio.Reader, wantID string) (*workerTranscribeResponse, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		line, err := stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read worker response: %w", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(line, &generic); err != nil {
			return nil, fmt.Errorf("decode worker response: %w", err)
		}
		id, _ := generic["id"].(string)
		if id != wantID {
			continue
		}
		if ok, _ := generic["ok"].(bool); !ok {
			return nil, fmt.Errorf("worker error: %v", generic["error"])
		}
		var resp workerTranscribeResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return nil, fmt.Errorf("decode worker transcription: %w", err)
		}
		return &resp, nil
	}
}
