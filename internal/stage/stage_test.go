package stage_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/stage"
)

func TestLoggerFromContext_WithLogger(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(nil, nil))
	ctx := stage.WithLogger(context.Background(), logger)
	got := stage.LoggerFromContext(ctx)
	if got != logger {
		t.Fatalf("expected attached logger, got %v", got)
	}
}

func TestLoggerFromContext_WithoutLogger(t *testing.T) {
	got := stage.LoggerFromContext(context.Background())
	if got != slog.Default() {
		t.Fatalf("expected slog.Default(), got %v", got)
	}
}

func TestParseRipSpec_Valid(t *testing.T) {
	env, err := stage.ParseRipSpec(`{"version":1,"metadata":{"media_type":"movie","title":"Test"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Metadata.MediaType != "movie" {
		t.Fatalf("expected media_type movie, got %s", env.Metadata.MediaType)
	}
}

func TestParseRipSpec_Empty(t *testing.T) {
	_, err := stage.ParseRipSpec("")
	if err != nil {
		t.Fatalf("unexpected error for empty string: %v", err)
	}
}

func TestParseRipSpec_InvalidJSON(t *testing.T) {
	_, err := stage.ParseRipSpec("{bad json")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid rip spec") {
		t.Fatalf("expected invalid rip spec error, got %T: %v", err, err)
	}
}
