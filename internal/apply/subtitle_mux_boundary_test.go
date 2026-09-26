package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestApplySubtitlesMuxSuccessAndFailure(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Movie", "fp")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.Env.Version = ripspec.CurrentVersion
	dir := t.TempDir()
	encoded := filepath.Join(dir, "movie.mkv")
	srt := filepath.Join(dir, "generated.srt")
	if err := os.WriteFile(encoded, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess.Env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{{EpisodeKey: "main", SubtitlePath: srt, Language: "eng"}}
	cfg := &config.Config{}
	cfg.Subtitles.MuxIntoMKV = true
	h := New(cfg)
	installTool(t, "mkvmerge", "printf muxed > \"$2\"\n")
	if err := h.applySubtitles(context.Background(), sess, "main", encoded); err != nil {
		t.Fatal(err)
	}
	asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindSubtitled, "main")
	if !ok || !asset.SubtitlesMuxed || asset.Path != filepath.Join(dir, "movie.subtitled.mkv") {
		t.Fatalf("muxed asset: %+v %v", asset, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "movie.en.srt")); err != nil {
		t.Fatal(err)
	}
	installTool(t, "mkvmerge", "exit 2\n")
	if err := h.applySubtitles(context.Background(), sess, "main", encoded); err != nil {
		t.Fatal(err)
	}
	asset, ok = sess.Env.Assets.FindAsset(ripspec.AssetKindSubtitled, "main")
	if !ok || asset.SubtitlesMuxed || asset.Path != encoded {
		t.Fatalf("fallback asset: %+v %v", asset, ok)
	}
	persisted, err := store.GetByID(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.NeedsReview != 1 || !strings.Contains(persisted.ReviewReason, "subtitle_mux") {
		t.Fatalf("review: %+v", persisted)
	}
}
