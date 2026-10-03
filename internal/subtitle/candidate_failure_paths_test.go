package subtitle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestEvaluateCandidateRejectsUnreadableAndEmptyDownloads(t *testing.T) {
	dir := t.TempDir()
	h := &Handler{cfg: &config.Config{}}
	for _, tc := range []struct {
		name   string
		fileID int
		reason string
	}{
		{"missing", 1, "unreadable candidate file"},
		{"empty", 2, "no cues remain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "empty" {
				cacheCandidate(t, h, tc.fileID, []byte("garbage"))
			} else {
				// A cache entry that is a directory exists but cannot be read.
				cacheCandidate(t, h, tc.fileID, nil)
			}
			result, err := h.evaluateCandidate(context.Background(), subtitleCandidate{FileID: tc.fileID}, adoptContext{VideoSeconds: 90, WorkDir: dir})
			if err != nil || !strings.Contains(result.RejectReason, tc.reason) {
				t.Fatalf("evaluation: %+v, %v", result, err)
			}
		})
	}
}

// cacheCandidate places one candidate in the OpenSubtitles cache under a
// fresh per-test cache home; nil data creates an unreadable (directory) entry.
func cacheCandidate(t *testing.T, h *Handler, fileID int, data []byte) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := filepath.Join(h.cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", fileID))
	if data == nil {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
