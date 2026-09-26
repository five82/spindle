package subtitle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateCandidateRejectsUnreadableAndEmptyDownloads(t *testing.T) {
	h := &Handler{}
	dir := t.TempDir()
	for _, tc := range []struct{ name, path, reason string }{
		{"missing", filepath.Join(dir, "missing.srt"), "unreadable candidate file"},
		{"empty", filepath.Join(dir, "empty.srt"), "no cues remain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "empty" {
				if err := os.WriteFile(tc.path, []byte("garbage"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			result, err := h.evaluateCandidate(context.Background(), subtitleCandidate{LocalPath: tc.path}, adoptContext{VideoSeconds: 90, WorkDir: dir})
			if err != nil || !strings.Contains(result.RejectReason, tc.reason) {
				t.Fatalf("evaluation: %+v, %v", result, err)
			}
		})
	}
}
