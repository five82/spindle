package ripper

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripspec"
)

func TestMapRippedAssets_Movie(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "title00.mkv"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir, nil)

	if len(env.Assets.Ripped) != 1 {
		t.Fatalf("expected 1 ripped asset, got %d", len(env.Assets.Ripped))
	}
	asset := env.Assets.Ripped[0]
	if asset.EpisodeKey != "main" {
		t.Errorf("expected episode key 'main', got %q", asset.EpisodeKey)
	}
	if asset.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", asset.Status)
	}
	if filepath.Base(asset.Path) != "title00.mkv" {
		t.Errorf("expected path ending in title00.mkv, got %q", asset.Path)
	}
}

func TestMapRippedAssets_TVEpisodes(t *testing.T) {
	dir := t.TempDir()
	files := []string{"title00.mkv", "title01.mkv", "title02.mkv"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir, nil)

	if len(env.Assets.Ripped) != 3 {
		t.Fatalf("expected 3 ripped assets, got %d", len(env.Assets.Ripped))
	}

	expectedKeys := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expectedKeys {
		got := env.Assets.Ripped[i].EpisodeKey
		if got != want {
			t.Errorf("asset[%d]: expected episode key %q, got %q", i, want, got)
		}
	}
}

func TestMapRippedAssets_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir, nil)

	if len(env.Assets.Ripped) != 0 {
		t.Fatalf("expected 0 ripped assets, got %d", len(env.Assets.Ripped))
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSelectRipTargets_Movie(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 7200}, // 2 hours - longest
			{ID: 1, Duration: 1800}, // 30 min
			{ID: 2, Duration: 60},   // 1 min - below minimum
		},
	}

	targets := h.selectRipTargets(testLogger(), env)

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].ID != 0 {
		t.Errorf("expected title ID 0 (longest), got %d", targets[0].ID)
	}
}

func TestSelectRipTargets_TV(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 2700},
			{ID: 1, Duration: 2700},
			{ID: 2, Duration: 2700},
			{ID: 3, Duration: 30}, // not referenced by any episode
		},
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 0},
			{Key: "s01_002", TitleID: 2},
		},
	}

	targets := h.selectRipTargets(testLogger(), env)

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	wantIDs := map[int]bool{0: true, 2: true}
	for _, tgt := range targets {
		if !wantIDs[tgt.ID] {
			t.Errorf("unexpected target title ID %d", tgt.ID)
		}
	}
}

func TestSelectRipTargets_Unknown(t *testing.T) {
	h := &Handler{cfg: &config.Config{}}
	h.cfg.MakeMKV.MinTitleLength = 120

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "unknown"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 7200},
			{ID: 1, Duration: 60}, // below minimum
			{ID: 2, Duration: 3600},
		},
	}

	targets := h.selectRipTargets(testLogger(), env)

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
}

func TestMapRippedAssets_TVWithTitleFileMap(t *testing.T) {
	dir := t.TempDir()

	// Create files that simulate MakeMKV output.
	file1 := filepath.Join(dir, "title00.mkv")
	file2 := filepath.Join(dir, "title01.mkv")
	if err := os.WriteFile(file1, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 5},
			{Key: "s01_002", TitleID: 8},
		},
	}

	// TitleID mapping from the rip loop.
	titleFileMap := map[int]string{
		5: file1,
		8: file2,
	}

	h := &Handler{}
	h.mapRippedAssets(env, dir, titleFileMap)

	if len(env.Assets.Ripped) != 2 {
		t.Fatalf("expected 2 ripped assets, got %d", len(env.Assets.Ripped))
	}

	// Verify TitleID-based mapping (not index order).
	if env.Assets.Ripped[0].EpisodeKey != "s01_001" {
		t.Errorf("asset[0].EpisodeKey = %q, want %q", env.Assets.Ripped[0].EpisodeKey, "s01_001")
	}
	if env.Assets.Ripped[0].TitleID != 5 {
		t.Errorf("asset[0].TitleID = %d, want 5", env.Assets.Ripped[0].TitleID)
	}
	if env.Assets.Ripped[1].EpisodeKey != "s01_002" {
		t.Errorf("asset[1].EpisodeKey = %q, want %q", env.Assets.Ripped[1].EpisodeKey, "s01_002")
	}
	if env.Assets.Ripped[1].TitleID != 8 {
		t.Errorf("asset[1].TitleID = %d, want 8", env.Assets.Ripped[1].TitleID)
	}
}

func TestListMKVFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"title00.mkv", "title01.mkv", "readme.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := listMKVFiles(dir)
	if len(files) != 2 {
		t.Errorf("expected 2 mkv files, got %d", len(files))
	}
}

func TestFindNewFile(t *testing.T) {
	before := map[string]bool{"/a/title00.mkv": true}
	after := map[string]bool{"/a/title00.mkv": true, "/a/title01.mkv": true}

	got := findNewFile(before, after)
	if got != "/a/title01.mkv" {
		t.Errorf("findNewFile() = %q, want %q", got, "/a/title01.mkv")
	}

	// No new file.
	got = findNewFile(before, before)
	if got != "" {
		t.Errorf("findNewFile() = %q, want empty", got)
	}
}
