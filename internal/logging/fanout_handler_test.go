package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestNewFanoutHandlerNilHandlers(t *testing.T) {
	h := newFanoutHandler(nil, nil, nil)
	if _, ok := h.(NoopHandler); !ok {
		t.Errorf("expected NoopHandler for all nil handlers, got %T", h)
	}
}

func TestNewFanoutHandlerSingleHandler(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)

	h := newFanoutHandler(inner)

	// Should return the inner handler directly, not wrapped
	if h != inner {
		t.Error("expected single handler to be returned unwrapped")
	}
}

func TestNewFanoutHandlerFiltersNil(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)

	h := newFanoutHandler(nil, inner, nil)

	// Should return the inner handler directly since others are nil
	if h != inner {
		t.Error("expected single non-nil handler to be returned unwrapped")
	}
}

func TestFanoutHandlerEnabled(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug})

	h := newFanoutHandler(h1, h2)

	// Should be enabled if any handler is enabled
	if !h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected fanout to be enabled for debug (h2 accepts it)")
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected fanout to be enabled for info (both accept it)")
	}
}

func TestFanoutHandlerEnabledNoneEnabled(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelWarn})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelError})

	h := newFanoutHandler(h1, h2)

	// Debug should not be enabled by either
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected fanout to not be enabled for debug")
	}
}

func TestFanoutHandlerHandle(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelInfo})

	h := newFanoutHandler(h1, h2)
	logger := slog.New(h)

	logger.Info("test message")

	if buf1.Len() == 0 {
		t.Error("expected output in buf1")
	}
	if buf2.Len() == 0 {
		t.Error("expected output in buf2")
	}
}

func TestFanoutHandlerHandleRespectsLevel(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelWarn})

	h := newFanoutHandler(h1, h2)
	logger := slog.New(h)

	logger.Info("info message")

	if buf1.Len() == 0 {
		t.Error("expected output in buf1 (info level)")
	}
	if buf2.Len() != 0 {
		t.Error("expected no output in buf2 (warn level filter)")
	}
}

func TestFanoutHandlerWithAttrs(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	h := newFanoutHandler(h1, h2)
	hWithAttrs := h.WithAttrs([]slog.Attr{slog.String("key", "value")})

	logger := slog.New(hWithAttrs)
	logger.Info("test")

	// Both outputs should contain the attribute
	if !bytes.Contains(buf1.Bytes(), []byte(`"key"`)) {
		t.Error("expected key attribute in buf1")
	}
	if !bytes.Contains(buf2.Bytes(), []byte(`"key"`)) {
		t.Error("expected key attribute in buf2")
	}
}

func TestFanoutHandlerWithGroup(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	h := newFanoutHandler(h1, h2)
	hWithGroup := h.WithGroup("mygroup")

	logger := slog.New(hWithGroup)
	logger.Info("test", slog.String("field", "value"))

	// Both outputs should contain the group
	if !bytes.Contains(buf1.Bytes(), []byte(`"mygroup"`)) {
		t.Error("expected group in buf1")
	}
	if !bytes.Contains(buf2.Bytes(), []byte(`"mygroup"`)) {
		t.Error("expected group in buf2")
	}
}

func TestTeeLogger(t *testing.T) {
	var baseBuf, teeBuf bytes.Buffer
	baseHandler := slog.NewJSONHandler(&baseBuf, nil)
	teeHandler := slog.NewJSONHandler(&teeBuf, nil)

	base := slog.New(baseHandler)
	logger := TeeLogger(base, teeHandler)

	logger.Info("teed message")

	if baseBuf.Len() == 0 {
		t.Error("expected output in base buffer")
	}
	if teeBuf.Len() == 0 {
		t.Error("expected output in tee buffer")
	}
}

func TestTeeLoggerNilBase(t *testing.T) {
	var teeBuf bytes.Buffer
	teeHandler := slog.NewJSONHandler(&teeBuf, nil)

	logger := TeeLogger(nil, teeHandler)
	logger.Info("no base")

	if teeBuf.Len() == 0 {
		t.Error("expected output in tee buffer")
	}
}

func TestTeeHandler(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	h := TeeHandler(h1, h2)
	logger := slog.New(h)

	logger.Info("tee handler test")

	if buf1.Len() == 0 {
		t.Error("expected output in buf1")
	}
	if buf2.Len() == 0 {
		t.Error("expected output in buf2")
	}
}

func TestFanoutHandlerDebugFiltering(t *testing.T) {
	// This tests the specific bug fix: DEBUG logs should only go to handlers
	// that have DEBUG level enabled, not to all handlers

	var infoBuf, debugBuf bytes.Buffer
	infoHandler := slog.NewJSONHandler(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo})
	debugHandler := slog.NewJSONHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	h := newFanoutHandler(infoHandler, debugHandler)
	logger := slog.New(h)

	// Log a debug message
	logger.Debug("debug only message")

	// Info handler should NOT have the debug message
	if infoBuf.Len() != 0 {
		t.Error("info handler should not receive debug messages")
	}

	// Debug handler should have the debug message
	if debugBuf.Len() == 0 {
		t.Error("debug handler should receive debug messages")
	}
}

func TestFanoutHandlerPreservesRecordForLastHandler(t *testing.T) {
	// The fanout handler clones records for all handlers except the last
	// to avoid mutation issues. This test verifies behavior is correct.

	var buf1, buf2 bytes.Buffer
	h1 := slog.NewJSONHandler(&buf1, nil)
	h2 := slog.NewJSONHandler(&buf2, nil)

	h := newFanoutHandler(h1, h2)
	logger := slog.New(h)

	logger.Info("test", slog.String("attr", "value"))

	// Both should have the attribute
	if !bytes.Contains(buf1.Bytes(), []byte(`"attr"`)) {
		t.Error("expected attr in buf1")
	}
	if !bytes.Contains(buf2.Bytes(), []byte(`"attr"`)) {
		t.Error("expected attr in buf2")
	}
}
