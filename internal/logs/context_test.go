package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestContextAttrsAttributeSharedLoggersWithoutDuplicates(t *testing.T) {
	var buf bytes.Buffer
	daemon := slog.New(NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := ContextWith(context.Background(), "item_id", 7, "stage", "encoding")
	ctx = ContextWith(ctx, "episode_key", "s01e02")

	daemon.InfoContext(ctx, "shared client", "event_type", "llm_request_start")
	daemon.With("item_id", 7).InfoContext(ctx, "session line")
	daemon.InfoContext(ctx, "explicit", "stage", "apply")
	daemon.Info("no context")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines: %q", lines)
	}
	for i, want := range []map[string]any{
		{"item_id": float64(7), "stage": "encoding", "episode_key": "s01e02"},
		{"item_id": float64(7), "stage": "encoding"},
		{"stage": "apply"},
		{},
	} {
		var got map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatal(err)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("line %d %s=%v, want %v: %s", i, k, got[k], v, lines[i])
			}
		}
		if i == 3 && got["item_id"] != nil {
			t.Fatalf("context-free line attributed: %s", lines[i])
		}
		if strings.Count(lines[i], `"item_id"`) > 1 || strings.Count(lines[i], `"stage"`) > 1 {
			t.Fatalf("line %d duplicated attribution: %s", i, lines[i])
		}
	}
}
