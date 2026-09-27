package audioanalysis

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

func TestDetectCommentaryTranscribesOnceAndExcludesStereoDuplicate(t *testing.T) {
	bin := t.TempDir()
	for name, script := range map[string]string{
		"ffmpeg": "#!/bin/sh\nfor arg do last=\"$arg\"; done\nprintf wave > \"$last\"\n",
		"uvx":    "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n if [ \"$1\" = '--output-dir' ]; then\n  shift\n  printf '1\\n00:00:01,000 --> 00:00:03,500\\nShared program dialogue here.\\n' > \"$1/audio.srt\"\n  printf '{}' > \"$1/audio.json\"\n fi\n shift\ndone\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"answers":{"decision":{"type":"choice","choice":"not_commentary","confidence":0.9,"probabilities":{"commentary":0.01,"not_commentary":0.99}}}}`)
	}))
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Movie", "abc")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Logger = logger
	sess.SetEnvelope(&ripspec.Envelope{Version: ripspec.CurrentVersion})
	cfg := &config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}, Commentary: config.CommentaryConfig{SimilarityThreshold: .85}}
	h := New(cfg, llm.New(config.LLMConfig{APIKey: "test", BaseURL: server.URL}, logger), transcription.New(transcription.Params{}, logger))
	fingerprint := filepath.Base(t.TempDir())
	t.Cleanup(func() { _ = os.RemoveAll(tempOutputDir(fingerprint, "main", 1)) })
	result := &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video"}, {CodecType: "audio", Channels: 6, Tags: map[string]string{"language": "eng"}}, {CodecType: "audio", Channels: 2, Tags: map[string]string{"language": "eng"}}}}
	commentary, excluded := h.detectCommentary(context.Background(), sess, result, "movie.mkv", fingerprint, "main")
	if len(commentary) != 0 || len(excluded) != 1 || excluded[0].Reason != "stereo downmix of primary" {
		t.Fatalf("commentary=%+v excluded=%+v", commentary, excluded)
	}
	asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindTranscript, "main")
	if !ok || !asset.IsCompleted() {
		t.Fatalf("primary transcript not recorded: %+v", asset)
	}
	if fp := h.primaryFingerprint(context.Background(), sess, "missing.mkv", 0, "main"); fp == nil {
		t.Fatal("transcript not reused")
	}
}
