package organizer

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/fileutil"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
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
	meta := &queue.Metadata{
		Title:     "The Matrix",
		MediaType: "movie",
		Year:      "1999",
		Movie:     true,
	}

	got := destFilename(meta, "main", ".mkv")
	want := "The Matrix (1999).mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVEpisode(t *testing.T) {
	meta := &queue.Metadata{
		Title:        "Breaking Bad",
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	got := destFilename(meta, "s01e03", ".mkv")
	want := "Breaking Bad - S01E03.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVRange(t *testing.T) {
	meta := &queue.Metadata{
		Title:        "Breaking Bad",
		ShowTitle:    "Breaking Bad",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	got := destFilename(meta, "s01e01-e02", ".mkv")
	want := "Breaking Bad - S01E01-E02.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestDestFilename_TVFallback(t *testing.T) {
	meta := &queue.Metadata{
		Title:        "Some Show",
		ShowTitle:    "Some Show",
		MediaType:    "tv",
		SeasonNumber: 1,
	}

	// Non-standard key that does not parse as sNNeNN.
	got := destFilename(meta, "s01_001", ".mkv")
	want := "Some Show - s01_001.mkv"
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestParseEpisodeKey(t *testing.T) {
	tests := []struct {
		key            string
		wantSeason     int
		wantEpisode    int
		wantEpisodeEnd int
	}{
		{"s01e03", 1, 3, 0},
		{"S02E10", 2, 10, 0},
		{"s01e01-e02", 1, 1, 2},
		{"s01_001", 0, 0, 0},
		{"main", 0, 0, 0},
		{"", 0, 0, 0},
	}
	for _, tt := range tests {
		s, e, end := parseEpisodeKey(tt.key)
		if s != tt.wantSeason || e != tt.wantEpisode || end != tt.wantEpisodeEnd {
			t.Errorf("parseEpisodeKey(%q) = (%d, %d, %d), want (%d, %d, %d)",
				tt.key, s, e, end, tt.wantSeason, tt.wantEpisode, tt.wantEpisodeEnd)
		}
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

func TestTotalCompletedStageBytes(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "one.mkv")
	file2 := filepath.Join(dir, "two.mkv")
	if err := os.WriteFile(file1, []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("123456"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &ripspec.Envelope{Assets: ripspec.Assets{Encoded: []ripspec.Asset{
		{EpisodeKey: "s01e01", Path: file1, Status: ripspec.AssetStatusCompleted},
		{EpisodeKey: "s01e02", Path: file2, Status: ripspec.AssetStatusCompleted},
		{EpisodeKey: "s01e03", Path: filepath.Join(dir, "missing.mkv"), Status: ripspec.AssetStatusCompleted},
		{EpisodeKey: "s01e04", Path: "", Status: ripspec.AssetStatusFailed},
	}}}
	got := totalCompletedStageBytes(env, ripspec.AssetKindEncoded, []string{"s01e01", "s01e02", "s01e03", "s01e04"})
	if got != 10 {
		t.Fatalf("totalCompletedStageBytes() = %d, want 10", got)
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
