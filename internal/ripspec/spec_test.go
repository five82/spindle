package ripspec

import (
	"testing"
)

func TestParseEncodeRoundTrip(t *testing.T) {
	env := Envelope{
		Fingerprint: "fp-1",
		ContentKey:  "content-1",
		Metadata: map[string]any{
			"title": "Show",
		},
		Attributes: map[string]any{
			"disc_number": 2,
		},
		Titles: []Title{{ID: 1, Name: "Title 1", Duration: 1200}},
		Episodes: []Episode{{
			Key:     "s01e01",
			TitleID: 1,
		}},
		Assets: Assets{
			Ripped: []Asset{{EpisodeKey: "s01e01", Path: "/tmp/episode1.mkv"}},
		},
	}
	encoded, err := env.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	decoded, err := Parse(encoded)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if decoded.Fingerprint != env.Fingerprint || decoded.ContentKey != env.ContentKey {
		t.Fatalf("unexpected decoded envelope: %+v", decoded)
	}
	if decoded.Metadata["title"] != "Show" || decoded.Attributes["disc_number"] != float64(2) {
		t.Fatalf("unexpected metadata/attributes: %+v %+v", decoded.Metadata, decoded.Attributes)
	}
	if len(decoded.Titles) != 1 || len(decoded.Episodes) != 1 || len(decoded.Assets.Ripped) != 1 {
		t.Fatalf("unexpected decoded sizes: %+v", decoded)
	}
}

func TestEpisodeKey(t *testing.T) {
	if EpisodeKey(0, 0) != "" {
		t.Fatalf("expected empty key for zero values")
	}
	if EpisodeKey(0, 3) != "s01e03" {
		t.Fatalf("unexpected episode key: %s", EpisodeKey(0, 3))
	}
}

func TestPlaceholderKey(t *testing.T) {
	tests := []struct {
		season, discIndex int
		want              string
	}{
		{1, 1, "s01_001"},
		{1, 3, "s01_003"},
		{2, 10, "s02_010"},
		{0, 1, "s01_001"},   // season defaults to 1
		{1, 0, "s01_001"},   // discIndex defaults to 1
		{-1, -1, "s01_001"}, // both default
	}
	for _, tt := range tests {
		got := PlaceholderKey(tt.season, tt.discIndex)
		if got != tt.want {
			t.Errorf("PlaceholderKey(%d, %d) = %q, want %q", tt.season, tt.discIndex, got, tt.want)
		}
	}
}

func TestEpisodeByKeyCaseInsensitive(t *testing.T) {
	env := Envelope{
		Episodes: []Episode{{Key: "S01E02", TitleID: 2}},
	}
	episode := env.EpisodeByKey("s01e02")
	if episode == nil || episode.TitleID != 2 {
		t.Fatalf("expected case-insensitive match, got %+v", episode)
	}
}

func TestAssetsAddAssetReplacesAndSorts(t *testing.T) {
	var assets Assets
	assets.AddAsset("ripped", Asset{EpisodeKey: "s01e02", TitleID: 2, Path: "/tmp/2.mkv"})
	assets.AddAsset("ripped", Asset{EpisodeKey: "s01e01", TitleID: 1, Path: "/tmp/1.mkv"})
	if len(assets.Ripped) != 2 || assets.Ripped[0].EpisodeKey != "s01e01" {
		t.Fatalf("expected assets sorted by key, got %+v", assets.Ripped)
	}
	assets.AddAsset("ripped", Asset{EpisodeKey: "s01e01", TitleID: 1, Path: "/tmp/1b.mkv"})
	if len(assets.Ripped) != 2 || assets.Ripped[0].Path != "/tmp/1b.mkv" {
		t.Fatalf("expected asset replacement, got %+v", assets.Ripped)
	}
	if _, ok := assets.FindAsset("ripped", "S01E01"); !ok {
		t.Fatalf("expected FindAsset to match case-insensitively")
	}
}
