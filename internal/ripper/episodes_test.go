package ripper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestParseTitleID(t *testing.T) {
	tests := []struct {
		name   string
		wantID int
		wantOK bool
	}{
		{"Batman_t00.mkv", 0, true},
		{"Batman_t02.mkv", 2, true},
		{"title_T15.mkv", 15, true},
		{"Show_t123.mkv", 123, true},
		{"no_title.mkv", 0, false},
		{"readme.txt", 0, false},
		{"t5.mkv", 0, false}, // only 1 digit, need 2+
	}

	for _, tt := range tests {
		id, ok := parseTitleID(tt.name)
		if ok != tt.wantOK || id != tt.wantID {
			t.Errorf("parseTitleID(%q) = (%d, %v), want (%d, %v)", tt.name, id, ok, tt.wantID, tt.wantOK)
		}
	}
}

func TestScanTitleFiles(t *testing.T) {
	dir := t.TempDir()

	files := []string{
		"Batman_t00.mkv",
		"Batman_t01.mkv",
		"Batman_t02.mkv",
		"readme.txt",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := scanTitleFiles(dir)
	if err != nil {
		t.Fatalf("scanTitleFiles: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 title files, got %d", len(result))
	}
	for _, id := range []int{0, 1, 2} {
		if _, ok := result[id]; !ok {
			t.Errorf("missing title ID %d", id)
		}
	}
}

func TestAssignEpisodeAssets(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Show_t00.mkv", "Show_t02.mkv", "Show_t05.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 0},
			{Key: "s01_002", TitleID: 2},
			{Key: "s01_003", TitleID: 5},
		},
	}

	result := assignEpisodeAssets(env, dir, testLogger())

	if result.Assigned != 3 {
		t.Fatalf("expected 3 assigned, got %d", result.Assigned)
	}
	if len(result.Missing) != 0 {
		t.Fatalf("expected 0 missing, got %v", result.Missing)
	}
	if len(env.Assets.Ripped) != 3 {
		t.Fatalf("expected 3 ripped assets, got %d", len(env.Assets.Ripped))
	}
	if env.Assets.Ripped[0].EpisodeKey != "s01_001" {
		t.Errorf("asset[0].EpisodeKey = %q, want s01_001", env.Assets.Ripped[0].EpisodeKey)
	}
	if env.Assets.Ripped[0].TitleID != 0 {
		t.Errorf("asset[0].TitleID = %d, want 0", env.Assets.Ripped[0].TitleID)
	}
}

func TestAssignEpisodeAssets_Partial(t *testing.T) {
	dir := t.TempDir()
	// Only create file for title 0, not title 3.
	if err := os.WriteFile(filepath.Join(dir, "Show_t00.mkv"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 0},
			{Key: "s01_002", TitleID: 3},
		},
	}

	result := assignEpisodeAssets(env, dir, testLogger())

	if result.Assigned != 1 {
		t.Fatalf("expected 1 assigned, got %d", result.Assigned)
	}
	if len(result.Missing) != 1 || result.Missing[0] != "s01_002" {
		t.Fatalf("expected missing=[s01_002], got %v", result.Missing)
	}
}

func TestAssignEpisodeAssets_Empty(t *testing.T) {
	dir := t.TempDir()
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 0},
		},
	}

	result := assignEpisodeAssets(env, dir, testLogger())

	if result.Assigned != 0 {
		t.Fatalf("expected 0 assigned, got %d", result.Assigned)
	}
	if len(result.Missing) != 1 {
		t.Fatalf("expected 1 missing, got %d", len(result.Missing))
	}
}

func TestCacheHasAllEpisodeFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Show_t00.mkv", "Show_t02.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 0},
			{Key: "s01_002", TitleID: 2},
		},
	}

	missing := cacheHasAllEpisodeFiles(env, dir)
	if len(missing) != 0 {
		t.Fatalf("expected no missing, got %v", missing)
	}
}

func TestCacheHasAllEpisodeFiles_Missing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Show_t00.mkv"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Episodes: []ripspec.Episode{
			{Key: "s01_001", TitleID: 0},
			{Key: "s01_002", TitleID: 5}, // not on disk
		},
	}

	missing := cacheHasAllEpisodeFiles(env, dir)
	if len(missing) != 1 || missing[0] != "s01_002" {
		t.Fatalf("expected missing=[s01_002], got %v", missing)
	}
}
