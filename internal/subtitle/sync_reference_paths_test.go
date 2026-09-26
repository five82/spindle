package subtitle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

func TestEnsureSyncReferenceTranscribesAndReusesArtifact(t *testing.T) {
	bin := t.TempDir()
	scripts := map[string]string{
		"ffprobe": "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"index\":0,\"codec_type\":\"audio\",\"channels\":2,\"tags\":{\"language\":\"eng\"}}]}'\n",
		"ffmpeg":  "#!/bin/sh\nfor arg do last=\"$arg\"; done\nprintf wave > \"$last\"\n",
		"uvx":     "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n if [ \"$1\" = '--output-dir' ]; then\n  shift\n  printf '1\\n00:00:01,000 --> 00:00:03,500\\nReference speech here.\\n' > \"$1/audio.srt\"\n  printf '{}' > \"$1/audio.json\"\n fi\n shift\ndone\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sess := newSubtitleTestSession(t, &ripspec.Envelope{Episodes: []ripspec.Episode{{Key: "main"}}})
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, transcription.New(transcription.Params{}, nil), nil)
	job := stage.AssetJob{Key: "main", Input: ripspec.Asset{EpisodeKey: "main", TitleID: 3, Path: "movie.mkv"}}
	first, err := h.ensureSyncReference(context.Background(), sess, job)
	if err != nil || first == nil || first.Segments != 1 {
		t.Fatalf("transcribe: %+v, %v", first, err)
	}
	asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindTranscript, "main")
	if !ok || asset.TitleID != 3 || asset.Path != first.SRTPath {
		t.Fatalf("recorded transcript: %+v", asset)
	}
	// Removing the transcription executables proves that the second call uses the saved artifact.
	t.Setenv("PATH", t.TempDir())
	second, err := h.ensureSyncReference(context.Background(), sess, job)
	if err != nil || second.SRTPath != first.SRTPath {
		t.Fatalf("reuse: %+v, %v", second, err)
	}
}
