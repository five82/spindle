package audioanalysis

import (
	"testing"

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
		t.Fatalf("expected key 'main', got %q", keys[0])
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

func TestAssetKeys_TV_SkipsEmptyKeys(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: ""},
			{Key: "s01e03"},
		},
	}

	keys := env.AssetKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys (skipping empty), got %d", len(keys))
	}
	if keys[0] != "s01e01" || keys[1] != "s01e03" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestTempOutputDir(t *testing.T) {
	dir := tempOutputDir("abc123", "s01e01", 2)
	want := "/tmp/spindle-commentary-abc123-s01e01-2"
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestAllowedAudioLanguageKeepsEnglishAndUnknown(t *testing.T) {
	tests := []struct {
		name string
		tags map[string]string
		want bool
	}{
		{"english iso3", map[string]string{"language": "eng"}, true},
		{"english iso2", map[string]string{"language": "en"}, true},
		{"missing", nil, true},
		{"undetermined", map[string]string{"language": "und"}, true},
		{"no language", map[string]string{"language": "nolang"}, true},
		{"japanese", map[string]string{"language": "jpn"}, false},
		{"unrecognized explicit language", map[string]string{"language": "tha"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := allowedAudioLanguage(tt.tags)
			if got != tt.want {
				t.Fatalf("allowedAudioLanguage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifySimilarityExclusion(t *testing.T) {
	const (
		similarity = 0.9322
		threshold  = 0.920
	)
	commentary := &ripspec.CommentaryTrackRef{Index: 5, Confidence: 0.99}

	tests := []struct {
		name       string
		channels   int
		commentary *ripspec.CommentaryTrackRef
		want       string
	}{
		{"stereo", 2, nil, "stereo downmix of primary"},
		{"multichannel", 6, nil, "duplicate/core of primary"},
		{"commentary is preserved", 2, commentary, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifySimilarityExclusion(tt.channels, similarity, threshold, tt.commentary)
			if got != tt.want {
				t.Fatalf("classifySimilarityExclusion() = %q, want %q", got, tt.want)
			}
		})
	}
}
