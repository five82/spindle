package contentid

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

func TestGenerateEpisodeTranscriptsRecordsBatchTranscripts(t *testing.T) {
	bin := t.TempDir()
	scripts := map[string]string{
		"ffprobe": "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"index\":0,\"codec_type\":\"audio\",\"channels\":2,\"tags\":{\"language\":\"eng\"}}]}'\n",
		"ffmpeg":  "#!/bin/sh\nfor arg do last=\"$arg\"; done\nprintf wave > \"$last\"\n",
		"uvx":     "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n if [ \"$1\" = '--output-dir' ]; then\n  shift\n  printf '1\\n00:00:01,000 --> 00:00:03,500\\nEpisode dialogue here.\\n' > \"$1/audio.srt\"\n  printf '{}' > \"$1/audio.json\"\n fi\n shift\ndone\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Show", "fp")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := &ripspec.Envelope{Version: ripspec.CurrentVersion, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}, {Key: "two", TitleID: 2}, {Key: "missing", TitleID: 3}}}
	for _, key := range []string{"one", "two"} {
		env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: key, Path: key + ".mkv", Status: ripspec.AssetStatusCompleted})
	}
	sess.SetEnvelope(env)
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, nil, transcription.New(transcription.Params{}, nil))
	if err := h.generateEpisodeTranscripts(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"one", "two"} {
		asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindTranscript, key)
		if !ok || !asset.IsCompleted() || asset.Path == "" {
			t.Fatalf("transcript for %s: %+v", key, asset)
		}
	}
	fresh, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Assets.Transcript) != 2 {
		t.Fatalf("saved transcripts: %+v", saved.Assets.Transcript)
	}
}
