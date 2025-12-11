package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/services"
)

func TestNewFromConfigConsole(t *testing.T) {
	cfg := config.Default()
	cfg.TMDBAPIKey = "test"
	cfg.LogDir = t.TempDir()

	logger, err := logging.NewFromConfig(&cfg)
	if err != nil {
		t.Fatalf("NewFromConfig returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger instance")
	}
	logger.Info("debug message")
}

func TestConsoleLoggerOmitsCallerForInfo(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "console-info.log")

	opts := logging.Options{
		Format:           "console",
		Level:            "info",
		OutputPaths:      []string{logPath},
		ErrorOutputPaths: []string{logPath},
	}

	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Info("message without caller")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if strings.Contains(string(content), ".go:") {
		t.Fatalf("expected no caller information in info logs, got %q", content)
	}
}

func TestConsoleLoggerAvoidsDuplicateStdStreams(t *testing.T) {
	origStdout := os.Stdout
	origStderr := os.Stderr

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW

	t.Cleanup(func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
		stdoutW.Close()
		stderrW.Close()
		stdoutR.Close()
		stderrR.Close()
	})

	opts := logging.Options{
		Format:           "console",
		Level:            "info",
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Info("single stream")

	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}

	stdoutBytes, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read stdout pipe: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("read stderr pipe: %v", err)
	}

	if len(stdoutBytes) == 0 {
		t.Fatal("expected stdout output, got none")
	}
	if len(stderrBytes) != 0 {
		t.Fatalf("expected no stderr output, got %q", string(stderrBytes))
	}
}

func TestConsoleLoggerIncludesCallerForDebug(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "console-debug.log")

	opts := logging.Options{
		Format:           "console",
		Level:            "debug",
		OutputPaths:      []string{logPath},
		ErrorOutputPaths: []string{logPath},
	}

	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger.Info("message with caller")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if !strings.Contains(string(content), ".go:") {
		t.Fatalf("expected caller information in debug logs, got %q", content)
	}
}

func TestNewJSONLogger(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "json.log")
	opts := logging.Options{
		Format:           "json",
		Level:            "debug",
		OutputPaths:      []string{logPath},
		ErrorOutputPaths: []string{logPath},
	}

	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger instance")
	}
	logger.Info("json message", logging.String("k", "v"))

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	line := strings.TrimSpace(string(content))
	if line == "" {
		t.Fatal("expected JSON log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if payload["level"] != "info" {
		t.Fatalf("expected level=info, got %v", payload["level"])
	}
	if payload["msg"] != "json message" {
		t.Fatalf("expected msg=json message, got %v", payload["msg"])
	}
	if payload["k"] != "v" {
		t.Fatalf("expected custom field, got %v", payload["k"])
	}
	if _, ok := payload["ts"].(string); !ok {
		t.Fatalf("expected ts string, got %v", payload["ts"])
	}
}

func TestNewInvalidLevelDefaultsToInfo(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "default.log")
	opts := logging.Options{Format: "console", Level: "invalid", OutputPaths: []string{logPath}}
	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger instance")
	}
	logger.Info("should use info level")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(content), "INFO") {
		t.Fatalf("expected info level output, got %q", content)
	}
}

func TestWithContextAddsFields(t *testing.T) {
	ctx := context.Background()
	ctx = services.WithItemID(ctx, 123)
	ctx = services.WithStage(ctx, "encoding")
	ctx = services.WithRequestID(ctx, "req-xyz")

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, nil)
	logger := slog.New(handler)

	logging.WithContext(ctx, logger).Info("contextual log")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got := payload[logging.FieldItemID]; got != float64(123) { // JSON numbers decode as float64
		t.Fatalf("item_id = %v, want 123", got)
	}
	if payload[logging.FieldStage] != "encoding" {
		t.Fatalf("stage = %v, want encoding", payload[logging.FieldStage])
	}
	if payload[logging.FieldCorrelationID] != "req-xyz" {
		t.Fatalf("correlation_id = %v, want req-xyz", payload[logging.FieldCorrelationID])
	}
}

func TestConsoleInfoFormattingHighlightsHumanContext(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "info-readable.log")

	opts := logging.Options{
		Format:           "console",
		Level:            "info",
		OutputPaths:      []string{logPath},
		ErrorOutputPaths: []string{logPath},
	}

	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger = logger.With(
		logging.String("component", "workflow-runner"),
		logging.Int("item_id", 9),
		logging.String("stage", "ripper"),
		logging.String("disc_title", "50 First Dates"),
		logging.String("processing_status", "ripping"),
		logging.String("correlation_id", "abc-123"),
	)

	logger.Info("stage started")
	logger.Info("stage started")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 5 {
		t.Fatalf("unexpected line count: %v", lines)
	}
	if !strings.Contains(lines[0], "INFO [workflow-runner] Item #9 (ripper) – stage started") {
		t.Fatalf("first header missing stage context: %q", lines[0])
	}
	if !strings.Contains(lines[1], "- Disc: \"50 First Dates\"") {
		t.Fatalf("expected disc bullet, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "- Status: ripping") {
		t.Fatalf("expected status bullet, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "- Correlation Id: abc-123") {
		t.Fatalf("expected correlation bullet, got %q", lines[3])
	}
	if !strings.Contains(lines[4], "INFO [workflow-runner] Item #9 (ripper) – stage started") {
		t.Fatalf("second header should be present, got %q", lines[4])
	}
}

func TestConsoleInfoFormattingResetsPerStage(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "info-stage.log")

	opts := logging.Options{
		Format:           "console",
		Level:            "info",
		OutputPaths:      []string{logPath},
		ErrorOutputPaths: []string{logPath},
	}

	baseLogger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	identifierLogger := baseLogger.With(
		logging.String("component", "workflow-runner"),
		logging.Int("item_id", 12),
		logging.String("stage", "identifier"),
		logging.String("disc_title", "Sample Disc"),
		logging.String("processing_status", "identifying"),
	)

	ripperLogger := baseLogger.With(
		logging.String("component", "workflow-runner"),
		logging.Int("item_id", 12),
		logging.String("stage", "ripper"),
		logging.String("disc_title", "Sample Disc"),
		logging.String("processing_status", "ripping"),
	)

	identifierLogger.Info("stage started")
	ripperLogger.Info("stage started")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	output := strings.TrimSpace(string(content))
	if strings.Count(output, "- Disc: \"Sample Disc\"") != 1 {
		t.Fatalf("disc line should appear once, got %q", output)
	}
	if !strings.Contains(output, "- Status: identifying") || !strings.Contains(output, "- Status: ripping") {
		t.Fatalf("status updates missing, got %q", output)
	}
}

func TestConsoleDebugFormattingEmitsDetailedContext(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "debug-details.log")

	opts := logging.Options{
		Format:           "console",
		Level:            "debug",
		OutputPaths:      []string{logPath},
		ErrorOutputPaths: []string{logPath},
	}

	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	logger = logger.With(
		logging.String("component", "identifier"),
		logging.Int("item_id", 42),
		logging.String("stage", "identifier"),
		logging.String("disc_title", "Example Disc"),
		logging.String("correlation_id", "debug-xyz"),
	)

	logger.Debug("scanning disc")

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multi-line debug output, got %q", content)
	}
	if !strings.Contains(lines[0], "DEBUG [identifier] Item #42 (identifier) – scanning disc") {
		t.Fatalf("expected detailed prefix in first line, got %q", lines[0])
	}
	var hasCorrelation bool
	var hasDisc bool
	for _, line := range lines[1:] {
		if strings.Contains(line, "correlation_id: debug-xyz") {
			hasCorrelation = true
		}
		if strings.Contains(line, "disc_title: \"Example Disc\"") {
			hasDisc = true
		}
	}
	if !hasCorrelation {
		t.Fatalf("expected correlation_id in debug details, got %q", content)
	}
	if !hasDisc {
		t.Fatalf("expected disc title in debug details, got %q", content)
	}
}
