package logging_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

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
	logger.Debug("debug message")
	logger.Sync() //nolint:errcheck // ignore sync errors on stdout
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
	logger.Sync() //nolint:errcheck

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
	logger.Sync() //nolint:errcheck

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if !strings.Contains(string(content), ".go:") {
		t.Fatalf("expected caller information in debug logs, got %q", content)
	}
}

func TestNewJSONLogger(t *testing.T) {
	opts := logging.Options{Format: "json", Level: "debug"}
	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger instance")
	}
	logger.Info("json message", zap.String("k", "v"))
	logger.Sync() //nolint:errcheck
}

func TestNewInvalidLevelDefaultsToInfo(t *testing.T) {
	opts := logging.Options{Format: "console", Level: "invalid"}
	logger, err := logging.New(opts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected logger instance")
	}
	logger.Info("should use info level")
	logger.Sync() //nolint:errcheck
}

func TestWithContextAddsFields(t *testing.T) {
	ctx := context.Background()
	ctx = services.WithItemID(ctx, 123)
	ctx = services.WithStage(ctx, "encoding")
	ctx = services.WithRequestID(ctx, "req-xyz")

	core, observed := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	logging.WithContext(ctx, logger).Info("contextual log")

	records := observed.All()
	if len(records) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(records))
	}
	fields := records[0].Context
	assertField := func(key string, want any) {
		for _, f := range fields {
			if f.Key != key {
				continue
			}
			switch f.Type {
			case zapcore.Int64Type:
				wantInt, ok := want.(int64)
				if !ok || f.Integer != wantInt {
					t.Fatalf("field %s = %d, want %v", key, f.Integer, want)
				}
			case zapcore.StringType:
				wantStr, ok := want.(string)
				if !ok || f.String != wantStr {
					t.Fatalf("field %s = %q, want %v", key, f.String, want)
				}
			default:
				if f.Interface != want {
					t.Fatalf("field %s = %v, want %v", key, f.Interface, want)
				}
			}
			return
		}
		t.Fatalf("field %s not found", key)
	}

	assertField(logging.FieldItemID, int64(123))
	assertField(logging.FieldStage, "encoding")
	assertField(logging.FieldCorrelationID, "req-xyz")
}
