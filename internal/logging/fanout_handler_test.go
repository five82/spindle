package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestNewFanoutHandlerEdgeCases(t *testing.T) {
	t.Run("all nil returns NoopHandler", func(t *testing.T) {
		h := newFanoutHandler(nil, nil, nil)
		if _, ok := h.(NoopHandler); !ok {
			t.Errorf("expected NoopHandler, got %T", h)
		}
	})

	t.Run("single handler returned unwrapped", func(t *testing.T) {
		var buf bytes.Buffer
		inner := slog.NewJSONHandler(&buf, nil)
		if h := newFanoutHandler(inner); h != inner {
			t.Error("expected single handler to be returned unwrapped")
		}
	})

	t.Run("filters nil handlers", func(t *testing.T) {
		var buf bytes.Buffer
		inner := slog.NewJSONHandler(&buf, nil)
		if h := newFanoutHandler(nil, inner, nil); h != inner {
			t.Error("expected single non-nil handler to be returned unwrapped")
		}
	})
}

func TestFanoutHandlerEnabled(t *testing.T) {
	var buf1, buf2 bytes.Buffer

	t.Run("enabled if any handler accepts level", func(t *testing.T) {
		h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
		h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug})
		h := newFanoutHandler(h1, h2)

		if !h.Enabled(context.Background(), slog.LevelDebug) {
			t.Error("expected enabled for debug (h2 accepts it)")
		}
		if !h.Enabled(context.Background(), slog.LevelInfo) {
			t.Error("expected enabled for info (both accept it)")
		}
	})

	t.Run("disabled when no handler accepts level", func(t *testing.T) {
		h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelWarn})
		h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelError})
		h := newFanoutHandler(h1, h2)

		if h.Enabled(context.Background(), slog.LevelDebug) {
			t.Error("expected not enabled for debug")
		}
	})
}

func TestFanoutHandlerHandle(t *testing.T) {
	t.Run("writes to all handlers", func(t *testing.T) {
		var buf1, buf2 bytes.Buffer
		h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
		h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelInfo})

		logger := slog.New(newFanoutHandler(h1, h2))
		logger.Info("test message")

		if buf1.Len() == 0 || buf2.Len() == 0 {
			t.Error("expected output in both buffers")
		}
	})

	t.Run("respects level filtering", func(t *testing.T) {
		var buf1, buf2 bytes.Buffer
		h1 := slog.NewJSONHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
		h2 := slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelWarn})

		logger := slog.New(newFanoutHandler(h1, h2))
		logger.Info("info message")

		if buf1.Len() == 0 {
			t.Error("expected output in buf1 (info level)")
		}
		if buf2.Len() != 0 {
			t.Error("expected no output in buf2 (warn level filter)")
		}
	})
}

func TestFanoutHandlerWithAttrsAndGroup(t *testing.T) {
	t.Run("WithAttrs propagates to all handlers", func(t *testing.T) {
		var buf1, buf2 bytes.Buffer
		h := newFanoutHandler(slog.NewJSONHandler(&buf1, nil), slog.NewJSONHandler(&buf2, nil))
		logger := slog.New(h.WithAttrs([]slog.Attr{slog.String("key", "value")}))
		logger.Info("test")

		if !bytes.Contains(buf1.Bytes(), []byte(`"key"`)) || !bytes.Contains(buf2.Bytes(), []byte(`"key"`)) {
			t.Error("expected key attribute in both buffers")
		}
	})

	t.Run("WithGroup propagates to all handlers", func(t *testing.T) {
		var buf1, buf2 bytes.Buffer
		h := newFanoutHandler(slog.NewJSONHandler(&buf1, nil), slog.NewJSONHandler(&buf2, nil))
		logger := slog.New(h.WithGroup("mygroup"))
		logger.Info("test", slog.String("field", "value"))

		if !bytes.Contains(buf1.Bytes(), []byte(`"mygroup"`)) || !bytes.Contains(buf2.Bytes(), []byte(`"mygroup"`)) {
			t.Error("expected group in both buffers")
		}
	})
}

func TestTeeLogger(t *testing.T) {
	t.Run("writes to both base and tee", func(t *testing.T) {
		var baseBuf, teeBuf bytes.Buffer
		base := slog.New(slog.NewJSONHandler(&baseBuf, nil))
		logger := TeeLogger(base, slog.NewJSONHandler(&teeBuf, nil))
		logger.Info("teed message")

		if baseBuf.Len() == 0 || teeBuf.Len() == 0 {
			t.Error("expected output in both buffers")
		}
	})

	t.Run("handles nil base", func(t *testing.T) {
		var teeBuf bytes.Buffer
		logger := TeeLogger(nil, slog.NewJSONHandler(&teeBuf, nil))
		logger.Info("no base")

		if teeBuf.Len() == 0 {
			t.Error("expected output in tee buffer")
		}
	})
}

func TestTeeHandler(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	logger := slog.New(TeeHandler(slog.NewJSONHandler(&buf1, nil), slog.NewJSONHandler(&buf2, nil)))
	logger.Info("tee handler test")

	if buf1.Len() == 0 || buf2.Len() == 0 {
		t.Error("expected output in both buffers")
	}
}

func TestFanoutHandlerDebugFiltering(t *testing.T) {
	var infoBuf, debugBuf bytes.Buffer
	infoHandler := slog.NewJSONHandler(&infoBuf, &slog.HandlerOptions{Level: slog.LevelInfo})
	debugHandler := slog.NewJSONHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	logger := slog.New(newFanoutHandler(infoHandler, debugHandler))
	logger.Debug("debug only message")

	if infoBuf.Len() != 0 {
		t.Error("info handler should not receive debug messages")
	}
	if debugBuf.Len() == 0 {
		t.Error("debug handler should receive debug messages")
	}
}

func TestFanoutHandlerPreservesRecordForAllHandlers(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	logger := slog.New(newFanoutHandler(slog.NewJSONHandler(&buf1, nil), slog.NewJSONHandler(&buf2, nil)))
	logger.Info("test", slog.String("attr", "value"))

	if !bytes.Contains(buf1.Bytes(), []byte(`"attr"`)) || !bytes.Contains(buf2.Bytes(), []byte(`"attr"`)) {
		t.Error("expected attr in both buffers")
	}
}
