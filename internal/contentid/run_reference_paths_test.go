package contentid

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
)

func TestRunWithTranscriptAndNoReferenceSubtitles(t *testing.T) {
	bin := t.TempDir()
	for name, script := range map[string]string{
		"ffprobe": "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"index\":0,\"codec_type\":\"audio\",\"channels\":2,\"tags\":{\"language\":\"eng\"}}]}'\n",
		"ffmpeg":  "#!/bin/sh\nfor arg do last=\"$arg\"; done\nprintf wave > \"$last\"\n",
		"uvx":     "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n if [ \"$1\" = '--output-dir' ]; then\n  shift\n  printf '1\\n00:00:01,000 --> 00:00:03,500\\nEpisode dialogue here.\\n' > \"$1/audio.srt\"\n  printf '{}' > \"$1/audio.json\"\n fi\n shift\ndone\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/42/season/1":
			_, _ = w.Write([]byte(`{"episodes":[{"episode_number":1,"name":"Pilot"}]}`))
		case "/subtitles":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer server.Close()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Show", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env := ripspec.Envelope{Version: ripspec.CurrentVersion, Metadata: ripspec.Metadata{MediaType: "tv", ID: 42}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}}}
	env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{EpisodeKey: "one", Path: "one.mkv", Status: ripspec.AssetStatusCompleted})
	item.RipSpecData, err = env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}}, nil, opensubtitles.New(opensubtitles.Params{APIKey: "test", BaseURL: server.URL}, nil), tmdb.New("test", server.URL, "en-US", nil), transcription.New(transcription.Params{}, nil))
	err = h.Run(context.Background(), sess)
	if err == nil || !strings.Contains(err.Error(), "no reference subtitles") {
		t.Fatalf("Run: %v", err)
	}
	got, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ripspec.Parse(got.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Assets.Transcript) != 1 || saved.Attributes.ContentID == nil || saved.Attributes.ContentID.Completed {
		t.Fatalf("persisted result: %+v", saved)
	}
}
