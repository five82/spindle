package ripper

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSelectRipTargets_Movie(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, titleOverride: -1}
	h.cfg.MakeMKV.MinTitleLength = 120

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 7200}, // 2 hours - longest
			{ID: 1, Duration: 1800}, // 30 min
			{ID: 2, Duration: 60},   // 1 min - below minimum
		},
	}

	targets, err := h.selectRipTargets(testLogger(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].ID != 0 {
		t.Errorf("expected title ID 0 (longest), got %d", targets[0].ID)
	}
}

func TestSelectRipTargets_TV(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, titleOverride: -1}

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

	targets, err := h.selectRipTargets(testLogger(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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
	h := &Handler{cfg: &config.Config{}, titleOverride: -1}
	h.cfg.MakeMKV.MinTitleLength = 120

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "unknown"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 7200},
			{ID: 1, Duration: 60}, // below minimum
			{ID: 2, Duration: 3600},
		},
	}

	targets, err := h.selectRipTargets(testLogger(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
}

func TestSelectRipTargets_titleOverride(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, titleOverride: 1}
	h.cfg.MakeMKV.MinTitleLength = 120

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 7200},
			{ID: 1, Duration: 6000},
			{ID: 2, Duration: 3600},
		},
	}

	targets, err := h.selectRipTargets(testLogger(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].ID != 1 {
		t.Errorf("expected title ID 1 (override), got %d", targets[0].ID)
	}
}

func TestSelectRipTargets_titleOverrideZero(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, titleOverride: 0}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 3600},
			{ID: 1, Duration: 7200},
		},
	}

	targets, err := h.selectRipTargets(testLogger(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].ID != 0 {
		t.Errorf("expected title ID 0 (override), got %d", targets[0].ID)
	}
}

func TestSelectRipTargets_titleOverrideNotFound(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, titleOverride: 99}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 7200},
			{ID: 1, Duration: 6000},
		},
	}

	targets, err := h.selectRipTargets(testLogger(), env)

	if err == nil {
		t.Fatal("expected error for non-existent override, got nil")
	}
	if targets != nil {
		t.Fatalf("expected nil targets for non-existent override, got %d", len(targets))
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

func TestRestoreTitlesFromCachedRipSpec(t *testing.T) {
	// Simulate the disc ID cache fast-path: envelope has no titles.
	env := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	// Build cached RipSpecData that contains titles (from the original rip).
	cachedEnv := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles: []ripspec.Title{
			{ID: 0, Duration: 6595, Chapters: 32, Name: "Theatrical"},
			{ID: 1, Duration: 6801, Chapters: 34, Name: "Director's Cut"},
		},
	}
	cachedData, err := json.Marshal(cachedEnv)
	if err != nil {
		t.Fatal(err)
	}
	meta := &ripcache.EntryMetadata{
		RipSpecData: string(cachedData),
	}

	// Apply the same restoration logic as the cache-hit path.
	if len(env.Titles) == 0 && meta.RipSpecData != "" {
		if parsed, err := ripspec.Parse(meta.RipSpecData); err == nil && len(parsed.Titles) > 0 {
			env.Titles = parsed.Titles
		}
	}

	if len(env.Titles) != 2 {
		t.Fatalf("expected 2 titles restored, got %d", len(env.Titles))
	}
	if env.Titles[0].Name != "Theatrical" {
		t.Errorf("titles[0].Name = %q, want %q", env.Titles[0].Name, "Theatrical")
	}
	if env.Titles[1].Duration != 6801 {
		t.Errorf("titles[1].Duration = %d, want 6801", env.Titles[1].Duration)
	}
}

func TestRestoreTitlesSkippedWhenAlreadyPresent(t *testing.T) {
	// Envelope already has titles (normal scan path) -- should not be overwritten.
	env := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles:   []ripspec.Title{{ID: 0, Duration: 7200, Name: "Original"}},
	}

	cachedEnv := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles:   []ripspec.Title{{ID: 0, Duration: 3600, Name: "Cached"}},
	}
	cachedData, err := json.Marshal(cachedEnv)
	if err != nil {
		t.Fatal(err)
	}
	meta := &ripcache.EntryMetadata{
		RipSpecData: string(cachedData),
	}

	if len(env.Titles) == 0 && meta.RipSpecData != "" {
		if parsed, err := ripspec.Parse(meta.RipSpecData); err == nil && len(parsed.Titles) > 0 {
			env.Titles = parsed.Titles
		}
	}

	if len(env.Titles) != 1 {
		t.Fatalf("expected 1 title (unchanged), got %d", len(env.Titles))
	}
	if env.Titles[0].Name != "Original" {
		t.Errorf("titles should not be overwritten; got Name=%q, want %q", env.Titles[0].Name, "Original")
	}
}

func TestStagingResetRemovesStaleFiles(t *testing.T) {
	// Simulate a stale staging directory from a previous run.
	stagingRoot := filepath.Join(t.TempDir(), "FINGERPRINT")
	rippedDir := filepath.Join(stagingRoot, "ripped")
	encodedDir := filepath.Join(stagingRoot, "encoded")

	if err := os.MkdirAll(rippedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(encodedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rippedDir, "stale.mkv"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(encodedDir, "stale.mkv"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply the same reset the ripper does at entry.
	if err := os.RemoveAll(stagingRoot); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if _, err := os.Stat(stagingRoot); !os.IsNotExist(err) {
		t.Errorf("staging root should not exist after reset, got err=%v", err)
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
