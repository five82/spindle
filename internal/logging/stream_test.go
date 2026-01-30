package logging

import (
	"context"
	"log/slog"
	"testing"
)

func TestStreamHandler_WithAttrs(t *testing.T) {
	hub := NewStreamHub(100)

	// Create a handler that wraps a discard handler
	base := slog.NewTextHandler(discardWriter{}, nil)
	handler := newStreamHandler(base, hub)

	// Create logger with item_id attribute (simulating item logger)
	logger := slog.New(handler).With(slog.Int64("item_id", 42))

	// Log a message
	logger.Info("test message", slog.String("extra", "value"))

	// Fetch the event from the hub
	events, _ := hub.Tail(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	// Verify the item_id from WithAttrs is included
	if events[0].ItemID != 42 {
		t.Errorf("expected item_id=42, got %d", events[0].ItemID)
	}
	if events[0].Message != "test message" {
		t.Errorf("expected message='test message', got %q", events[0].Message)
	}
}

func TestStreamHandler_NestedWithAttrs(t *testing.T) {
	hub := NewStreamHub(100)
	base := slog.NewTextHandler(discardWriter{}, nil)
	handler := newStreamHandler(base, hub)

	// Create logger with multiple layers of WithAttrs (simulating item logger hierarchy)
	logger := slog.New(handler).
		With(slog.String("lane", "background")).
		With(slog.Int64("item_id", 99)).
		With(slog.String("stage", "encoding"))

	logger.Info("encoding progress")

	events, _ := hub.Tail(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	evt := events[0]
	if evt.ItemID != 99 {
		t.Errorf("expected item_id=99, got %d", evt.ItemID)
	}
	if evt.Lane != "background" {
		t.Errorf("expected lane='background', got %q", evt.Lane)
	}
	if evt.Stage != "encoding" {
		t.Errorf("expected stage='encoding', got %q", evt.Stage)
	}
}

func TestStreamHandler_CallSiteOverridesWithAttrs(t *testing.T) {
	hub := NewStreamHub(100)
	base := slog.NewTextHandler(discardWriter{}, nil)
	handler := newStreamHandler(base, hub)

	// Create logger with a stage via WithAttrs
	logger := slog.New(handler).With(slog.String("stage", "original"))

	// Log with a different stage at call site - should override
	logger.Info("message", slog.String("stage", "overridden"))

	events, _ := hub.Tail(10)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Stage != "overridden" {
		t.Errorf("expected stage='overridden', got %q", events[0].Stage)
	}
}

func TestStreamHandler_NilHub(t *testing.T) {
	base := slog.NewTextHandler(discardWriter{}, nil)
	handler := newStreamHandler(base, nil)

	// Should return the base handler when hub is nil
	if handler != base {
		t.Errorf("expected base handler when hub is nil")
	}
}

func TestStreamHandler_Enabled(t *testing.T) {
	hub := NewStreamHub(100)
	base := slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	handler := newStreamHandler(base, hub)

	// Should delegate to base handler
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected INFO to be disabled when base level is WARN")
	}
	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("expected WARN to be enabled when base level is WARN")
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
