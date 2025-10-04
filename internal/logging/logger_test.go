package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
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
