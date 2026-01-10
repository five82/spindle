package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSessionIDHandler(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := newSessionIDHandler(base, "test-session-123")

	logger := slog.New(handler)
	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, `"session_id":"test-session-123"`) {
		t.Errorf("expected session_id in output, got: %s", output)
	}
}

func TestSessionIDHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := newSessionIDHandler(base, "session-abc")

	logger := slog.New(handler).With("extra", "value")
	logger.Info("test message")

	output := buf.String()
	if !strings.Contains(output, `"session_id":"session-abc"`) {
		t.Errorf("expected session_id in output, got: %s", output)
	}
	if !strings.Contains(output, `"extra":"value"`) {
		t.Errorf("expected extra attr in output, got: %s", output)
	}
}

func TestSessionIDHandler_NilBase(t *testing.T) {
	handler := newSessionIDHandler(nil, "session-123")
	if _, ok := handler.(NoopHandler); !ok {
		t.Errorf("expected NoopHandler when base is nil, got: %T", handler)
	}
}
