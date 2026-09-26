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

func TestApplySubtitleSidecarAndMissingRecords(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("disc", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	item.RipSpecData, err = (&ripspec.Envelope{Version: ripspec.CurrentVersion}).Encode()
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
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mkv")
	source := filepath.Join(dir, "generated.srt")
	if err := os.WriteFile(source, []byte("1\n00:00:00,000 --> 00:00:02,000\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(&config.Config{})
	if err := h.applySubtitles(context.Background(), sess, "movie", video); err != nil {
		t.Fatal(err)
	}
	sess.Env.Attributes.SubtitleGenerationResults = []ripspec.SubtitleGenRecord{
		{EpisodeKey: "movie", SubtitlePath: source, Language: "eng", SevereIssues: []string{"bad"}},
	}
	if err := h.applySubtitles(context.Background(), sess, "movie", video); err != nil {
		t.Fatal(err)
	}
	sess.Env.Attributes.SubtitleGenerationResults[0].SevereIssues = nil
	sess.Env.Attributes.SubtitleGenerationResults[0].SubtitlePath = filepath.Join(dir, "missing.srt")
	if err := h.applySubtitles(context.Background(), sess, "movie", video); err != nil {
		t.Fatal(err)
	}
	sess.Env.Attributes.SubtitleGenerationResults[0].SubtitlePath = source
	if err := h.applySubtitles(context.Background(), sess, "MOVIE", video); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "movie.en.srt")
	data, err := os.ReadFile(sidecar)
	if err != nil || !strings.Contains(string(data), "Hello") {
		t.Fatalf("sidecar: %q %v", data, err)
	}
	asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindSubtitled, "MOVIE")
	if !ok || asset.Path != video || asset.SubtitlesMuxed {
		t.Fatalf("asset: %+v %v", asset, ok)
	}
	sess.Env.Attributes.SubtitleGenerationResults[0].SubtitlePath = dir
	if err := h.applySubtitles(context.Background(), sess, "movie", video); err == nil || !strings.Contains(err.Error(), "place subtitle sidecar") {
		t.Fatalf("copy directory: %v", err)
	}
}

func TestApplyNoEncodedAssetsAndReviewReason(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("disc", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(&config.Config{}).Run(context.Background(), sess); err == nil || !strings.Contains(err.Error(), "no encoded assets") {
		t.Fatalf("empty apply: %v", err)
	}
	sess.Env.Episodes = []ripspec.Episode{{Key: "movie"}}
	if err := flagForReview(sess, "movie", "missing subtitle"); err != nil {
		t.Fatal(err)
	}
	if err := flagForReview(sess, "movie", "missing subtitle"); err != nil {
		t.Fatal(err)
	}
	if len(sess.Item.ReviewReasons()) != 1 || !strings.Contains(sess.Item.ReviewReasons()[0], "movie") {
		t.Fatalf("review: %v", sess.Item.ReviewReasons())
	}
}
