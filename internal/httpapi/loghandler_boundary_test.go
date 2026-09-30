package httpapi

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogHandlerGroupsAndStructuredFields(t *testing.T) {
	buf := NewLogBuffer(4)
	logger := slog.New(NewLogHandler(slog.NewTextHandler(io.Discard, nil), buf))
	logger.With("item_id", int64(12), "stage", "encoding").Info("started", "nested", slog.GroupValue(slog.String("reason", "started")))
	logger.WithGroup("outer").With("name", "prebound").Warn("grouped", "stage", "scoped")
	entries, _ := buf.Query(LogQueryOpts{})
	if len(entries) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	a := entries[0]
	if a.ItemID != 12 || a.Stage != "encoding" || a.Fields["nested.reason"] != "started" {
		t.Fatalf("entry: %+v", a)
	}
	b := entries[1]
	if b.Fields["outer.name"] != "prebound" || b.Fields["outer.stage"] != "scoped" || b.Stage != "" {
		t.Fatalf("group: %+v", b)
	}
	for _, tc := range []struct {
		level string
		rank  int
	}{{"debug", 0}, {"info", 1}, {"warn", 2}, {"warning", 2}, {"error", 3}, {"unknown", -1}} {
		if got := levelRank(tc.level); got != tc.rank {
			t.Errorf("levelRank(%q)=%d", tc.level, got)
		}
	}
	if entries, _ := buf.Query(LogQueryOpts{Level: "error"}); len(entries) != 0 {
		t.Fatalf("unexpected errors: %+v", entries)
	}
	if entries, _ := buf.Query(LogQueryOpts{MinTime: time.Now().Add(time.Hour)}); len(entries) != 0 {
		t.Fatalf("future entries: %+v", entries)
	}
	if !strings.Contains(b.Msg, "grouped") {
		t.Fatal(b)
	}
	// The handler must still accept calls through the slog.Handler interface.
	if err := NewLogHandler(slog.NewTextHandler(io.Discard, nil), buf).Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelDebug, "direct", 0)); err != nil {
		t.Fatal(err)
	}
}
