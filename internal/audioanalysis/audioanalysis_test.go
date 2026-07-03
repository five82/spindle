package audioanalysis

import (
	"errors"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/textutil"
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

func TestPrimaryFingerprintCacheUsesSuccessfulFingerprintOnce(t *testing.T) {
	cache := primaryFingerprintCache{}
	calls := 0
	load := func() (*textutil.Fingerprint, error) {
		calls++
		return textutil.NewFingerprint("primary dialogue"), nil
	}

	first, err := cache.get(load)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		got, err := cache.get(load)
		if err != nil {
			t.Fatalf("cached get failed: %v", err)
		}
		if got != first {
			t.Fatalf("cached fingerprint pointer changed")
		}
	}
	if calls != 1 {
		t.Fatalf("load called %d times, want 1", calls)
	}
}

func TestPrimaryFingerprintCacheRetriesAfterFailure(t *testing.T) {
	cache := primaryFingerprintCache{}
	wantErr := errors.New("temporary transcription failure")
	calls := 0
	load := func() (*textutil.Fingerprint, error) {
		calls++
		if calls == 1 {
			return nil, wantErr
		}
		return textutil.NewFingerprint("primary dialogue"), nil
	}

	if _, err := cache.get(load); !errors.Is(err, wantErr) {
		t.Fatalf("first get error = %v, want %v", err, wantErr)
	}
	if _, err := cache.get(load); err != nil {
		t.Fatalf("second get failed: %v", err)
	}
	if _, err := cache.get(load); err != nil {
		t.Fatalf("cached get failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("load called %d times, want 2", calls)
	}
}

func TestPrimaryFingerprintCacheCachesEmptyFingerprint(t *testing.T) {
	cache := primaryFingerprintCache{}
	calls := 0
	load := func() (*textutil.Fingerprint, error) {
		calls++
		return nil, nil
	}

	if got, err := cache.get(load); err != nil || got != nil {
		t.Fatalf("first get = (%v, %v), want (nil, nil)", got, err)
	}
	if got, err := cache.get(load); err != nil || got != nil {
		t.Fatalf("cached get = (%v, %v), want (nil, nil)", got, err)
	}
	if calls != 1 {
		t.Fatalf("load called %d times, want 1", calls)
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

func TestBuildCommentaryUserPrompt_WithTitle(t *testing.T) {
	stream := ffprobe.Stream{
		Tags: map[string]string{"title": "Director Commentary"},
	}
	prompt := buildCommentaryUserPrompt(stream, "Some transcript text here.")

	if !contains(prompt, "Title: Director Commentary") {
		t.Errorf("expected title in prompt, got:\n%s", prompt)
	}
	if !contains(prompt, "Some transcript text here.") {
		t.Errorf("expected transcript in prompt, got:\n%s", prompt)
	}
}

func TestBuildCommentaryUserPrompt_NoTitle(t *testing.T) {
	stream := ffprobe.Stream{
		Tags: map[string]string{},
	}
	prompt := buildCommentaryUserPrompt(stream, "Transcript.")

	if contains(prompt, "Title:") {
		t.Errorf("expected no title line, got:\n%s", prompt)
	}
	if !contains(prompt, "Transcript.") {
		t.Errorf("expected transcript in prompt, got:\n%s", prompt)
	}
}

func TestBuildCommentaryUserPrompt_Truncation(t *testing.T) {
	long := make([]byte, maxTranscriptLen+500)
	for i := range long {
		long[i] = 'a'
	}

	stream := ffprobe.Stream{Tags: map[string]string{}}
	prompt := buildCommentaryUserPrompt(stream, string(long))

	if !contains(prompt, "[truncated]") {
		t.Error("expected truncation marker in prompt")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
