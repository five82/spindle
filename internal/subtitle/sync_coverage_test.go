package subtitle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncSubtitleToReferenceFailuresAndSuccess(t *testing.T) {
	original := runFFSubsync
	t.Cleanup(func() { runFFSubsync = original })
	out := filepath.Join(t.TempDir(), "synced.srt")
	runFFSubsync = func(_ context.Context, args []string) ([]byte, error) {
		if strings.Join(args, " ") != strings.Join([]string{"ffsubsync", "reference.srt", "-i", "input.srt", "-o", out}, " ") {
			t.Fatalf("arguments: %q", args)
		}
		return []byte("sync failed\n"), errors.New("exit 1")
	}
	if err := syncSubtitleToReference(context.Background(), "reference.srt", "input.srt", out); err == nil || !strings.Contains(err.Error(), "exit 1: sync failed") {
		t.Fatalf("command failure: %v", err)
	}
	runFFSubsync = func(context.Context, []string) ([]byte, error) { return nil, nil }
	if err := syncSubtitleToReference(context.Background(), "reference.srt", "input.srt", out); err == nil || !strings.Contains(err.Error(), "produced no output") {
		t.Fatalf("missing output: %v", err)
	}
	if err := os.WriteFile(out, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncSubtitleToReference(context.Background(), "reference.srt", "input.srt", out); err != nil {
		t.Fatal(err)
	}
}
