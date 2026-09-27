package subtitle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
)

func TestCandidateRejectsShortSpanAndFailedSync(t *testing.T) {
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "candidate.srt")
	cues := dialogueCues(12, 10, 10)
	if err := os.WriteFile(candidatePath, []byte(srtutil.Format(cues)), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &Handler{}
	candidate := subtitleCandidate{FileID: 77, LocalPath: candidatePath}
	result, err := h.evaluateCandidate(context.Background(), candidate, adoptContext{VideoSeconds: 1000, WorkDir: dir})
	if err != nil || !strings.Contains(result.RejectReason, "candidate spans") {
		t.Fatalf("short span: %+v %v", result, err)
	}
	original := runFFSubsync
	t.Cleanup(func() { runFFSubsync = original })
	runFFSubsync = func(context.Context, []string) ([]byte, error) {
		return []byte("failed"), errors.New("sync unavailable")
	}
	ctx := adoptContext{VideoSeconds: 140, WorkDir: dir, ReferenceSRTPath: candidatePath, ReferenceCues: cues}
	result, err = h.evaluateCandidate(context.Background(), candidate, ctx)
	if err != nil || !strings.Contains(result.RejectReason, "sync failed") {
		t.Fatalf("sync failure: %+v %v", result, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = h.evaluateCandidate(canceled, candidate, ctx)
	if err != context.Canceled {
		t.Fatalf("canceled sync: %v", err)
	}
}
