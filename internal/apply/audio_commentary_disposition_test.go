package apply

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestPostRefinementAudioPreservesCommentaryOnDispositionFailure(t *testing.T) {
	bin := t.TempDir()
	for name, script := range map[string]string{
		"ffprobe": "#!/bin/sh\necho '{\"streams\":[{\"codec_type\":\"audio\",\"index\":0,\"tags\":{\"language\":\"eng\"}},{\"codec_type\":\"audio\",\"index\":1,\"tags\":{\"language\":\"eng\",\"title\":\"Director\"}}]}'\n",
		"ffmpeg":  "#!/bin/sh\necho 'mux rejected' >&2\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	input := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(input, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	comms := []ripspec.CommentaryTrackRef{{Index: 1}}
	primary, label, mapped, err := applyPostRefinementAudio(context.Background(), logger, input, &audioRefinementResult{KeptIndices: []int{0, 1}}, comms)
	if err != nil || primary.Index != 0 || label == "" || len(mapped) != 1 || mapped[0].Index != 1 {
		t.Fatalf("degraded disposition: %+v %q %+v %v", primary, label, mapped, err)
	}
}
