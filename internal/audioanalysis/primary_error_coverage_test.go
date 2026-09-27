package audioanalysis

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

func TestPrimaryFingerprintFallsBackWhenArtifactMissingAndTranscriptionFails(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.srt")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess := &stage.Session{Item: &queue.Item{ID: 1, DiscFingerprint: "abc"}, Env: &ripspec.Envelope{Assets: ripspec.Assets{Transcript: []ripspec.Asset{{EpisodeKey: "main", Path: missing, Status: ripspec.AssetStatusCompleted}}}}, Logger: logger}
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: filepath.Join(dir, "staging")}}, nil, transcription.New(transcription.Params{}, logger))
	// No ffmpeg/WhisperX binary can produce a transcript for a nonexistent source.
	if got := h.primaryFingerprint(context.Background(), sess, filepath.Join(dir, "missing.mkv"), 0, "main"); got != nil {
		t.Fatal("fingerprint from missing input")
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.cfg.Paths.StagingDir = blocker
	if got := h.primaryFingerprint(context.Background(), sess, "missing.mkv", 0, "other"); got != nil {
		t.Fatal("fingerprint despite inaccessible staging root")
	}
}
