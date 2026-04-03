package ripspec

import (
	"testing"
)

func TestParseEncodeRoundTrip(t *testing.T) {
	env := Envelope{
		Version:     CurrentVersion,
		Fingerprint: "abc123",
		ContentKey:  "movie-test-2024",
		Metadata: Metadata{
			ID:        42,
			Title:     "Test Movie",
			MediaType: "movie",
			Year:      "2024",
		},
		Titles: []Title{
			{ID: 0, Name: "title_0", Duration: 7200, Chapters: 20},
		},
		Episodes: []Episode{
			{Key: "s01e01", TitleID: 0, Season: 1, Episode: 1},
		},
	}

	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got.Version != env.Version {
		t.Errorf("Version = %d, want %d", got.Version, env.Version)
	}
	if got.Fingerprint != env.Fingerprint {
		t.Errorf("Fingerprint = %q, want %q", got.Fingerprint, env.Fingerprint)
	}
	if got.Metadata.Title != env.Metadata.Title {
		t.Errorf("Metadata.Title = %q, want %q", got.Metadata.Title, env.Metadata.Title)
	}
	if len(got.Titles) != 1 {
		t.Fatalf("Titles len = %d, want 1", len(got.Titles))
	}
	if got.Titles[0].Duration != 7200 {
		t.Errorf("Title duration = %d, want 7200", got.Titles[0].Duration)
	}
}

func TestParseRejectsUnknownVersion(t *testing.T) {
	raw := `{"version": 99, "fingerprint": "x"}`
	_, err := Parse(raw)
	if err == nil {
		t.Fatal("expected error for unknown version, got nil")
	}
}

func TestParseEmptyString(t *testing.T) {
	env, err := Parse("")
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if env.Version != 0 {
		t.Errorf("Version = %d, want 0", env.Version)
	}
	if env.Fingerprint != "" {
		t.Errorf("Fingerprint = %q, want empty", env.Fingerprint)
	}
}

func TestParseBlankString(t *testing.T) {
	env, err := Parse("   \n  ")
	if err != nil {
		t.Fatalf("Parse blank: %v", err)
	}
	if env.Version != 0 {
		t.Errorf("Version = %d, want 0", env.Version)
	}
}

func TestEpisodeByKeyCaseInsensitive(t *testing.T) {
	env := Envelope{
		Episodes: []Episode{
			{Key: "s01e03", TitleID: 2, Season: 1, Episode: 3},
			{Key: "s01e04", TitleID: 3, Season: 1, Episode: 4},
		},
	}

	ep := env.EpisodeByKey("S01E03")
	if ep == nil {
		t.Fatal("EpisodeByKey returned nil for case-insensitive match")
	}
	if ep.Episode != 3 {
		t.Errorf("Episode = %d, want 3", ep.Episode)
	}

	if env.EpisodeByKey("s99e99") != nil {
		t.Error("EpisodeByKey should return nil for missing key")
	}
}

func TestAddAssetAppendAndReplace(t *testing.T) {
	var assets Assets

	a1 := Asset{EpisodeKey: "s01e01", Path: "/ripped/ep1.mkv", Status: "ok"}
	assets.AddAsset("ripped", a1)

	if len(assets.Ripped) != 1 {
		t.Fatalf("Ripped len = %d, want 1", len(assets.Ripped))
	}
	if assets.Ripped[0].Path != "/ripped/ep1.mkv" {
		t.Errorf("Path = %q, want /ripped/ep1.mkv", assets.Ripped[0].Path)
	}

	// Replace existing
	a2 := Asset{EpisodeKey: "s01e01", Path: "/ripped/ep1_v2.mkv", Status: "ok"}
	assets.AddAsset("ripped", a2)

	if len(assets.Ripped) != 1 {
		t.Fatalf("after replace: Ripped len = %d, want 1", len(assets.Ripped))
	}
	if assets.Ripped[0].Path != "/ripped/ep1_v2.mkv" {
		t.Errorf("after replace: Path = %q, want /ripped/ep1_v2.mkv", assets.Ripped[0].Path)
	}

	// Append different key
	a3 := Asset{EpisodeKey: "s01e02", Path: "/ripped/ep2.mkv", Status: "ok"}
	assets.AddAsset("ripped", a3)
	if len(assets.Ripped) != 2 {
		t.Fatalf("after append: Ripped len = %d, want 2", len(assets.Ripped))
	}
}

func TestEpisodeAppendReviewReason(t *testing.T) {
	ep := &Episode{}
	ep.AppendReviewReason("low confidence")
	ep.AppendReviewReason("sequence gap")
	if !ep.NeedsReview {
		t.Fatal("NeedsReview = false, want true")
	}
	if ep.ReviewReason != "low confidence; sequence gap" {
		t.Fatalf("ReviewReason = %q", ep.ReviewReason)
	}
}

func TestFindAssetSuccessAndMiss(t *testing.T) {
	assets := Assets{
		Encoded: []Asset{
			{EpisodeKey: "s01e05", Path: "/enc/ep5.mkv", Status: "ok"},
		},
	}

	found, ok := assets.FindAsset("encoded", "s01e05")
	if !ok {
		t.Fatal("FindAsset returned false for existing asset")
	}
	if found.Path != "/enc/ep5.mkv" {
		t.Errorf("Path = %q, want /enc/ep5.mkv", found.Path)
	}

	_, ok = assets.FindAsset("encoded", "s01e99")
	if ok {
		t.Error("FindAsset returned true for missing asset")
	}

	_, ok = assets.FindAsset("bogus", "s01e05")
	if ok {
		t.Error("FindAsset returned true for invalid stage")
	}
}

func TestRemapEpisodeKeys(t *testing.T) {
	assets := Assets{
		Ripped:    []Asset{{EpisodeKey: "s01_001", Path: "/rip/ep1.mkv", Status: "completed"}},
		Encoded:   []Asset{{EpisodeKey: "s01_001", Path: "/enc/ep1.mkv", Status: "completed"}},
		Subtitled: []Asset{{EpisodeKey: "s01_001", Path: "/sub/ep1.mkv", Status: "completed"}},
		Final:     []Asset{{EpisodeKey: "s01_001", Path: "/final/ep1.mkv", Status: "completed"}},
	}

	assets.RemapEpisodeKeys(map[string]string{"s01_001": "s01e03"})

	for _, stage := range []string{"ripped", "encoded", "subtitled", "final"} {
		asset, ok := assets.FindAsset(stage, "s01e03")
		if !ok {
			t.Fatalf("FindAsset(%q, remapped key) = false, want true", stage)
		}
		if asset.EpisodeKey != "s01e03" {
			t.Fatalf("stage %s EpisodeKey = %q, want s01e03", stage, asset.EpisodeKey)
		}
	}

	if _, ok := assets.FindAsset("encoded", "s01_001"); ok {
		t.Fatal("old encoded key still present after remap")
	}
}

func TestPlaceholderKey(t *testing.T) {
	tests := []struct {
		season, disc int
		want         string
	}{
		{1, 1, "s01_001"},
		{2, 3, "s02_003"},
		{0, 0, "s01_001"},
		{-1, -5, "s01_001"},
		{3, 0, "s03_001"},
	}
	for _, tt := range tests {
		got := PlaceholderKey(tt.season, tt.disc)
		if got != tt.want {
			t.Errorf("PlaceholderKey(%d, %d) = %q, want %q", tt.season, tt.disc, got, tt.want)
		}
	}
}

func TestEpisodeKeyFormatting(t *testing.T) {
	tests := []struct {
		season, episode int
		want            string
	}{
		{1, 3, "s01e03"},
		{10, 12, "s10e12"},
		{0, 0, ""},
		{-1, -1, ""},
		{0, 5, "s00e05"},
	}
	for _, tt := range tests {
		got := EpisodeKey(tt.season, tt.episode)
		if got != tt.want {
			t.Errorf("EpisodeKey(%d, %d) = %q, want %q", tt.season, tt.episode, got, tt.want)
		}
	}
}

func TestHasResolvedEpisodes(t *testing.T) {
	resolved := []Episode{
		{Key: "s01e01", Episode: 1},
		{Key: "s01e02", Episode: 2},
	}
	if !HasResolvedEpisodes(resolved) {
		t.Error("HasResolvedEpisodes = false, want true")
	}

	unresolved := []Episode{
		{Key: "s01_001", Episode: 0},
		{Key: "s01_002", Episode: 0},
	}
	if HasResolvedEpisodes(unresolved) {
		t.Error("HasResolvedEpisodes = true for placeholders, want false")
	}

	if HasResolvedEpisodes(nil) {
		t.Error("HasResolvedEpisodes = true for nil, want false")
	}
}

func TestHasUnresolvedEpisodes(t *testing.T) {
	unresolved := []Episode{
		{Key: "s01_001", Episode: 0},
	}
	if !HasUnresolvedEpisodes(unresolved) {
		t.Error("HasUnresolvedEpisodes = false for placeholder, want true")
	}

	mixed := []Episode{
		{Key: "s01e01", Episode: 1},
		{Key: "s01_002", Episode: 0},
	}
	if !HasUnresolvedEpisodes(mixed) {
		t.Error("HasUnresolvedEpisodes = false for mixed, want true")
	}

	resolved := []Episode{
		{Key: "s01e01", Episode: 1},
		{Key: "s01e02", Episode: 2},
	}
	if HasUnresolvedEpisodes(resolved) {
		t.Error("HasUnresolvedEpisodes = true for resolved, want false")
	}

	if HasUnresolvedEpisodes(nil) {
		t.Error("HasUnresolvedEpisodes should be false for nil")
	}
}

func TestExpectedCountMovieVsTV(t *testing.T) {
	movie := Envelope{
		Metadata: Metadata{MediaType: "movie"},
		Episodes: []Episode{{Key: "s01e01"}},
	}
	if movie.ExpectedCount() != 1 {
		t.Errorf("movie ExpectedCount = %d, want 1", movie.ExpectedCount())
	}

	tv := Envelope{
		Metadata: Metadata{MediaType: "tv"},
		Episodes: []Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}
	if tv.ExpectedCount() != 3 {
		t.Errorf("tv ExpectedCount = %d, want 3", tv.ExpectedCount())
	}
}

func TestAssetCounts(t *testing.T) {
	env := Envelope{
		Metadata: Metadata{MediaType: "tv"},
		Episodes: []Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
		},
		Assets: Assets{
			Ripped: []Asset{
				{EpisodeKey: "s01e01", Path: "/r/1.mkv", Status: "ok"},
				{EpisodeKey: "s01e02", Path: "/r/2.mkv", Status: "ok"},
			},
			Encoded: []Asset{
				{EpisodeKey: "s01e01", Path: "/e/1.mkv", Status: "ok"},
			},
			Final: []Asset{},
		},
	}

	expected, ripped, encoded, final := env.AssetCounts()
	if expected != 2 {
		t.Errorf("expected = %d, want 2", expected)
	}
	if ripped != 2 {
		t.Errorf("ripped = %d, want 2", ripped)
	}
	if encoded != 1 {
		t.Errorf("encoded = %d, want 1", encoded)
	}
	if final != 0 {
		t.Errorf("final = %d, want 0", final)
	}
}

func TestMissingEpisodes(t *testing.T) {
	env := Envelope{
		Metadata: Metadata{MediaType: "tv"},
		Episodes: []Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
		Assets: Assets{
			Ripped: []Asset{
				{EpisodeKey: "s01e01", Path: "/r/1.mkv", Status: "ok"},
			},
		},
	}

	missing := env.MissingEpisodes("ripped")
	if len(missing) != 2 {
		t.Fatalf("MissingEpisodes len = %d, want 2", len(missing))
	}
	if missing[0] != "s01e02" || missing[1] != "s01e03" {
		t.Errorf("MissingEpisodes = %v, want [s01e02 s01e03]", missing)
	}

	// Movies always return nil.
	movieEnv := Envelope{Metadata: Metadata{MediaType: "movie"}}
	if movieEnv.MissingEpisodes("ripped") != nil {
		t.Error("MissingEpisodes for movie should be nil")
	}
}

func TestCloneIndependence(t *testing.T) {
	orig := Assets{
		Ripped: []Asset{
			{EpisodeKey: "s01e01", Path: "/r/1.mkv", Status: "ok"},
		},
		Encoded: []Asset{
			{EpisodeKey: "s01e01", Path: "/e/1.mkv", Status: "ok"},
		},
	}

	cloned := orig.Clone()

	// Mutate clone.
	cloned.Ripped[0].Path = "/changed.mkv"
	cloned.AddAsset("encoded", Asset{EpisodeKey: "s01e02", Path: "/e/2.mkv", Status: "ok"})

	if orig.Ripped[0].Path != "/r/1.mkv" {
		t.Error("Clone mutation affected original Ripped path")
	}
	if len(orig.Encoded) != 1 {
		t.Error("Clone mutation affected original Encoded length")
	}
}

func TestCompletedAssetCount(t *testing.T) {
	assets := Assets{
		Ripped: []Asset{
			{EpisodeKey: "s01e01", Path: "/r/1.mkv", Status: "ok"},
			{EpisodeKey: "s01e02", Path: "/r/2.mkv", Status: "failed"},
			{EpisodeKey: "s01e03", Path: "", Status: "ok"},
			{EpisodeKey: "s01e04", Path: "/r/4.mkv", Status: "ok"},
		},
	}

	got := assets.CompletedAssetCount("ripped")
	if got != 2 {
		t.Errorf("CompletedAssetCount = %d, want 2", got)
	}

	if assets.CompletedAssetCount("bogus") != 0 {
		t.Error("CompletedAssetCount for invalid stage should be 0")
	}
}

func TestAssetIsCompletedAndIsFailed(t *testing.T) {
	ok := Asset{Path: "/file.mkv", Status: "ok"}
	if !ok.IsCompleted() {
		t.Error("IsCompleted = false for valid asset")
	}
	if ok.IsFailed() {
		t.Error("IsFailed = true for valid asset")
	}

	failed := Asset{Path: "/file.mkv", Status: "failed"}
	if failed.IsCompleted() {
		t.Error("IsCompleted = true for failed asset")
	}
	if !failed.IsFailed() {
		t.Error("IsFailed = false for failed asset")
	}

	empty := Asset{Path: "", Status: "ok"}
	if empty.IsCompleted() {
		t.Error("IsCompleted = true for empty path")
	}
}

func TestClearFailedAsset(t *testing.T) {
	assets := Assets{
		Encoded: []Asset{
			{EpisodeKey: "s01e01", Path: "/e/1.mkv", Status: "failed", ErrorMsg: "encode error"},
		},
	}

	assets.ClearFailedAsset("encoded", "s01e01")

	a := assets.Encoded[0]
	if a.Status != "" {
		t.Errorf("Status = %q, want empty", a.Status)
	}
	if a.ErrorMsg != "" {
		t.Errorf("ErrorMsg = %q, want empty", a.ErrorMsg)
	}
	if a.Path != "" {
		t.Errorf("Path = %q, want empty", a.Path)
	}
}

func TestCountUnresolvedEpisodes(t *testing.T) {
	episodes := []Episode{
		{Key: "s01e01", Episode: 1},
		{Key: "s01_001", Episode: 0},
		{Key: "", Episode: 0},
		{Key: "s01e04", Episode: 4},
	}
	got := CountUnresolvedEpisodes(episodes)
	if got != 2 {
		t.Errorf("CountUnresolvedEpisodes = %d, want 2", got)
	}
}
