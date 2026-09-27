package encoder

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/reel"
)

func testEncoderLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func encoderSession(t *testing.T, env ripspec.Envelope) *stage.Session {
	t.Helper()
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Movie", "fp")
	if err != nil {
		t.Fatal(err)
	}
	env.Version = ripspec.CurrentVersion
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
	return sess
}

func TestRunWithoutRipsAndCanceled(t *testing.T) {
	h := New(&config.Config{Paths: config.PathsConfig{StagingDir: t.TempDir()}})
	sess := encoderSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "movie"}})
	if err := h.Run(context.Background(), sess); err == nil || !strings.Contains(err.Error(), "no ripped assets") {
		t.Fatalf("no rips: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Run(ctx, sess); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestEncodeJobFailureAndSuccessPersistAssets(t *testing.T) {
	sess := encoderSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "one", TitleID: 1}}})
	h := New(&config.Config{})
	job := stage.AssetJob{Key: "one", Input: ripspec.Asset{EpisodeKey: "one", Path: "source.mkv"}, ProgressTotal: 1}
	if err := h.handleEncodeFailure(testEncoderLogger(), sess, job, errors.New("worker failed")); err != nil {
		t.Fatal(err)
	}
	if asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindEncoded, "one"); !ok || !asset.IsFailed() {
		t.Fatalf("failed asset: %+v", sess.Env.Assets.Encoded)
	}
	result, err := h.handleEncodeSuccess(testEncoderLogger(), sess, job, &reel.Result{OutputFile: "encoded.mkv", OriginalSize: 100, EncodedSize: 40, SizeReductionPercent: 60, ValidationPassed: false})
	if err != nil || result.failed || result.encodedSize != 40 {
		t.Fatalf("success: %+v %v", result, err)
	}
	fresh, err := sess.Store.GetByID(sess.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ripspec.Parse(fresh.RipSpecData)
	if err != nil {
		t.Fatal(err)
	}
	if asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, "one"); !ok || !asset.IsCompleted() || asset.Path != "encoded.mkv" {
		t.Fatalf("encoded asset: %+v", env.Assets.Encoded)
	}
	if !env.Episodes[0].NeedsReview || !strings.Contains(strings.Join(fresh.ReviewReasons(), " "), "validation failed") {
		t.Fatalf("validation review: %+v %v", env.Episodes, fresh.ReviewReasons())
	}
}

func TestEncodingProbeFailureAndCompletedJobSkip(t *testing.T) {
	h := New(&config.Config{})
	job := stage.AssetJob{Key: "one", Input: ripspec.Asset{Path: filepath.Join(t.TempDir(), "absent.mkv")}, ProgressTotal: 1}
	snap := h.initialEncodingSnapshot(context.Background(), testEncoderLogger(), job)
	if snap.Substage != "initializing" || snap.InputFile != "absent.mkv" {
		t.Fatalf("snapshot: %+v", snap)
	}
	sess := encoderSession(t, ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{{Key: "one"}}, Assets: ripspec.Assets{Encoded: []ripspec.Asset{{EpisodeKey: "one", Path: "encoded.mkv", Status: ripspec.AssetStatusCompleted}}}})
	summary, err := h.encodeJobs(context.Background(), sess, t.TempDir(), []stage.AssetJob{job})
	if err != nil || summary.errors != 0 {
		t.Fatalf("skip: %+v %v", summary, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = h.encodeJobs(ctx, sess, t.TempDir(), []stage.AssetJob{job})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
