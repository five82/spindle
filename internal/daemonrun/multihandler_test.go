package daemonrun

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestMultiHandlerFiltersAndFansOutAttributes(t *testing.T) {
	var debug, warn bytes.Buffer
	debugHandler := slog.NewTextHandler(&debug, &slog.HandlerOptions{Level: slog.LevelDebug})
	warnHandler := slog.NewTextHandler(&warn, &slog.HandlerOptions{Level: slog.LevelWarn})
	multi := newMultiHandler(debugHandler, warnHandler)
	if !multi.Enabled(context.Background(), slog.LevelInfo) || multi.Enabled(context.Background(), slog.LevelDebug-1) {
		t.Fatal("incorrect combined level filter")
	}
	logger := slog.New(multi.WithAttrs([]slog.Attr{slog.String("component", "daemon")}).WithGroup("job"))
	logger.Info("starting", "id", 1)
	logger.Warn("slow", "id", 2)
	if !strings.Contains(debug.String(), "starting") || !strings.Contains(debug.String(), "slow") || !strings.Contains(debug.String(), "component=daemon") || !strings.Contains(debug.String(), "job.id=1") {
		t.Fatalf("debug output: %s", debug.String())
	}
	if strings.Contains(warn.String(), "starting") || !strings.Contains(warn.String(), "slow") || !strings.Contains(warn.String(), "job.id=2") {
		t.Fatalf("warn output: %s", warn.String())
	}
}
