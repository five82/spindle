package organizer

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/mediameta"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

func TestAssetKeys_Movie(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	keys := env.AssetKeys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0] != "main" {
		t.Errorf("expected key 'main', got %q", keys[0])
	}
}

func TestAssetKeys_TV(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}

	keys := env.AssetKeys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	expected := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("key[%d]: expected %q, got %q", i, want, keys[i])
		}
	}
}

func TestAssetKeys_TVNoEpisodes(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
	}

	keys := env.AssetKeys()
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestDestFilename_Movie(t *testing.T) {
	meta := &mediameta.Metadata{
		Title:     "The Matrix",
		MediaType: "movie",
		Year:      "1999",
		Movie:     true,
	}

	got := mediameta.DestFilename(meta, "main", ".mkv", 0, 0, 0)
	want := "The Matrix (1999).mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVEpisode(t *testing.T) {
	meta := &mediameta.Metadata{
		Title:        "Breaking Bad",
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	got := mediameta.DestFilename(meta, "s01_001", ".mkv", 1, 3, 0)
	want := "Breaking Bad - S01E03.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVRange(t *testing.T) {
	meta := &mediameta.Metadata{
		Title:        "Breaking Bad",
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	got := mediameta.DestFilename(meta, "s01_001", ".mkv", 1, 1, 2)
	want := "Breaking Bad - S01E01-E02.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVFallback(t *testing.T) {
	meta := &mediameta.Metadata{
		Title:        "Some Show",
		ShowTitle:    "Some Show",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	// Unresolved episode: season/episode not yet set, so the key is used as-is.
	got := mediameta.DestFilename(meta, "s01_001", ".mkv", 0, 0, 0)
	want := "Some Show - s01_001.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestOverallBytePercent(t *testing.T) {
	if got := overallBytePercent(50, 200); math.Abs(got-25) > 1e-9 {
		t.Fatalf("overallBytePercent() = %f, want 25", got)
	}
	if got := overallBytePercent(250, 200); math.Abs(got-100) > 1e-9 {
		t.Fatalf("overallBytePercent clamp = %f, want 100", got)
	}
	if got := overallBytePercent(50, 0); got != 0 {
		t.Fatalf("overallBytePercent zero total = %f, want 0", got)
	}
}

func TestOrganizationInputForKeyPrefersSubtitledPerEpisode(t *testing.T) {
	env := &ripspec.Envelope{Assets: ripspec.Assets{
		Encoded: []ripspec.Asset{
			{EpisodeKey: "s01e01", Path: "one-encoded.mkv"},
			{EpisodeKey: "s01e02", Path: "two-encoded.mkv"},
		},
		Subtitled: []ripspec.Asset{{EpisodeKey: "s01e02", Path: "two-subtitled.mkv"}},
	}}

	first, ok := organizationInputForKey(env, "s01e01")
	if !ok || first.stage != ripspec.AssetKindEncoded || first.asset.Path != "one-encoded.mkv" {
		t.Fatalf("first input = %#v, found=%v", first, ok)
	}
	second, ok := organizationInputForKey(env, "s01e02")
	if !ok || second.stage != ripspec.AssetKindSubtitled || second.asset.Path != "two-subtitled.mkv" {
		t.Fatalf("second input = %#v, found=%v", second, ok)
	}
}

func TestRemoveMuxedSubtitleSidecarRemovesOnlyMatchingLanguage(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Episode.mkv")
	english := filepath.Join(dir, "Episode.en.srt")
	french := filepath.Join(dir, "Episode.fr.srt")
	for _, path := range []string{english, french} {
		if err := os.WriteFile(path, []byte("subtitle"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := &ripspec.Envelope{Attributes: ripspec.EnvelopeAttributes{
		SubtitleGenerationResults: []ripspec.SubtitleGenRecord{{EpisodeKey: "s01_001", Language: "eng"}},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := removeMuxedSubtitleSidecar(logger, env, "s01_001", video); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(english); !os.IsNotExist(err) {
		t.Fatalf("matching sidecar still exists: %v", err)
	}
	if _, err := os.Stat(french); err != nil {
		t.Fatalf("other-language sidecar removed: %v", err)
	}
}

func TestTotalOrganizationBytes(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "one.mkv")
	file2 := filepath.Join(dir, "two.mkv")
	if err := os.WriteFile(file1, []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}
	inputs := []organizationInput{
		{key: "s01e01", asset: ripspec.Asset{Path: file1}},
		{key: "s01e02", asset: ripspec.Asset{Path: file2}},
		{key: "s01e03", asset: ripspec.Asset{Path: filepath.Join(dir, "missing.mkv")}},
	}
	if got := totalOrganizationBytes(inputs); got != 10 {
		t.Fatalf("totalOrganizationBytes() = %d, want 10", got)
	}
}

func TestCopyAssetsToDirRejectsMissingAssetBeforeCopy(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	item, err := store.NewDisc("Test", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := stage.NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess.SetEnvelope(&ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}},
	})
	dest := filepath.Join(t.TempDir(), "library")
	h := &Handler{cfg: &config.Config{}}
	_, _, err = h.copyAssetsToDir(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), sess, &mediameta.Metadata{}, dest, []string{"s01e01"}, "library")
	if err == nil || !strings.Contains(err.Error(), "no completed subtitled or encoded asset") {
		t.Fatalf("copyAssetsToDir error = %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination created before asset preflight: %v", statErr)
	}
}

func TestPartitionTVOrganizationKeys(t *testing.T) {
	env := &ripspec.Envelope{Episodes: []ripspec.Episode{
		{Key: "s01e01", Episode: 1},
		{Key: "s01e02", Episode: 2, NeedsReview: true},
		{Key: "s01_003", Episode: 0},
	}}
	libraryKeys, reviewKeys := partitionTVOrganizationKeys(env)
	if len(libraryKeys) != 1 || libraryKeys[0] != "s01e01" {
		t.Fatalf("libraryKeys = %#v, want [s01e01]", libraryKeys)
	}
	if len(reviewKeys) != 2 || reviewKeys[0] != "s01e02" || reviewKeys[1] != "s01_003" {
		t.Fatalf("reviewKeys = %#v, want [s01e02 s01_003]", reviewKeys)
	}
}

func TestReviewPathForItemUsesBoundedPrimaryReviewReason(t *testing.T) {
	item := &queue.Item{ID: 9, DiscFingerprint: "5483099ec8089977f7b31644c5898356b4173617ab9a2f62d997a6187d95cf91"}
	item.AppendReviewReason("srt_validation: high_reading_speed (s01e01)")
	item.AppendReviewReason("srt_validation: short_cue_duration (s01e01)")

	got := reviewPathForItem("/review", item)
	want := filepath.Join("/review", "srt_validation-high_reading_speed-(s01e01)_5483099e")
	if got != want {
		t.Fatalf("reviewPathForItem() = %q, want %q", got, want)
	}
}

func TestReviewPathForItemCapsLongReason(t *testing.T) {
	item := &queue.Item{ID: 9, DiscFingerprint: "5483099ec8089977f7b31644c5898356b4173617ab9a2f62d997a6187d95cf91"}
	item.AppendReviewReason("subtitle validation: " + strings.Repeat("very-long-reason-", 20))

	dirName := filepath.Base(reviewPathForItem("/review", item))
	maxDirBytes := reviewReasonDirMaxBytes + 1 + 8
	if len(dirName) > maxDirBytes {
		t.Fatalf("review dir name length = %d, want <= %d (%q)", len(dirName), maxDirBytes, dirName)
	}
	if !strings.HasSuffix(dirName, "_5483099e") {
		t.Fatalf("review dir name = %q, want fingerprint suffix", dirName)
	}
}

func TestReviewPathForItemUsesManualReviewFallback(t *testing.T) {
	item := &queue.Item{ID: 7}
	got := reviewPathForItem("/review", item)
	want := filepath.Join("/review", "manual-review_id7")
	if got != want {
		t.Fatalf("reviewPathForItem() = %q, want %q", got, want)
	}
}

func TestMoveOrCopyWithProgressRenamesOnSameDevice(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	dst := filepath.Join(dir, "dst.mkv")
	data := []byte("test payload")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var calls int
	var last fileutil.CopyProgress
	if err := moveOrCopyWithProgress(src, dst, func(p fileutil.CopyProgress) {
		calls++
		last = p
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still exists after rename, err=%v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("destination contents = %q, want %q", got, data)
	}
	if calls != 1 {
		t.Fatalf("progress calls = %d, want 1", calls)
	}
	if last.BytesCopied != int64(len(data)) || last.TotalBytes != int64(len(data)) {
		t.Fatalf("progress = %+v, want copied=total=%d", last, len(data))
	}
}

func TestSendTerminalNotificationCleanSuccess(t *testing.T) {
	var gotTitle, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{notifier: notify.New(srv.URL, 5)}
	item := &queue.Item{ID: 1, DiscTitle: "Avatar (2009)"}
	sess := &stage.Session{Store: store, Item: item}

	h.sendTerminalNotification(context.Background(), logger, sess, 1, 0)

	if gotTitle != "Imported: Avatar (2009)" {
		t.Fatalf("title = %q, want %q", gotTitle, "Imported: Avatar (2009)")
	}
	if gotBody != "Imported 1 item to the library." {
		t.Fatalf("body = %q, want %q", gotBody, "Imported 1 item to the library.")
	}
}

func TestSendTerminalNotificationImportedButReviewRequired(t *testing.T) {
	var gotTitle, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	defer func() { _ = store.Close() }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{notifier: notify.New(srv.URL, 5)}
	item := &queue.Item{ID: 2, DiscTitle: "Example Season 01", NeedsReview: 1}
	item.AppendReviewReason("low-confidence identification")
	sess := &stage.Session{
		Store: store,
		Item:  item,
		Env:   &ripspec.Envelope{Metadata: ripspec.Metadata{DiscNumber: 2}},
	}

	h.sendTerminalNotification(context.Background(), logger, sess, 3, 0)

	if gotTitle != "Review needed: Example Season 01 - Disc 2" {
		t.Fatalf("title = %q, want %q", gotTitle, "Review needed: Example Season 01 - Disc 2")
	}
	want := "Imported 3 items to the library, but review is still required.\nReason: low-confidence identification"
	if gotBody != want {
		t.Fatalf("body = %q, want %q", gotBody, want)
	}
}
