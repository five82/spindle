package subtitles

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
)

func TestValidateMuxedSubtitlesZeroExpected(t *testing.T) {
	// When expectedCount is 0, validation should pass without probing
	err := ValidateMuxedSubtitles(context.Background(), "", "/any/path.mkv", 0, "en", nil)
	if err != nil {
		t.Fatalf("expected no error for zero expected subtitles, got: %v", err)
	}
}

func TestValidateMuxedSubtitlesEmptyPath(t *testing.T) {
	// Empty MKV path should fail validation
	err := ValidateMuxedSubtitles(context.Background(), "", "", 1, "en", nil)
	if err == nil {
		t.Fatal("expected error for empty MKV path")
	}
}

func TestValidateMuxedSubtitlesNonExistentFile(t *testing.T) {
	// Skip if ffprobe is not available
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	tmpDir := t.TempDir()
	nonExistentFile := filepath.Join(tmpDir, "nonexistent.mkv")

	logger := logging.NewNop()
	err := ValidateMuxedSubtitles(context.Background(), "", nonExistentFile, 1, "en", logger)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestAnalyzeSubtitleStreamsEmpty(t *testing.T) {
	// With no streams and no expected language, LanguageMatch should be true
	result := analyzeSubtitleStreams(nil, "")
	if result.SubtitleCount != 0 {
		t.Errorf("expected 0 subtitle count, got %d", result.SubtitleCount)
	}
	if !result.LanguageMatch {
		t.Error("expected language match to be true when no expected lang")
	}

	// With no streams but an expected language, LanguageMatch should be false
	// (no subtitle found to match the expected language)
	result2 := analyzeSubtitleStreams(nil, "en")
	if result2.LanguageMatch {
		t.Error("expected language match to be false when no streams to match against")
	}
}

func TestAnalyzeSubtitleStreamsWithSubtitles(t *testing.T) {
	streams := []ffprobe.Stream{
		{CodecType: "video", Index: 0},
		{CodecType: "audio", Index: 1},
		{
			CodecType:   "subtitle",
			Index:       2,
			Tags:        map[string]string{"language": "eng"},
			Disposition: map[string]int{"default": 1},
		},
		{
			CodecType:   "subtitle",
			Index:       3,
			Tags:        map[string]string{"language": "eng"},
			Disposition: map[string]int{"forced": 1},
		},
	}

	result := analyzeSubtitleStreams(streams, "en")
	if result.SubtitleCount != 2 {
		t.Errorf("expected 2 subtitle count, got %d", result.SubtitleCount)
	}
	if result.DefaultTrack != 0 { // First subtitle stream (index 0 in subtitle-relative indexing)
		t.Errorf("expected default track at index 0, got %d", result.DefaultTrack)
	}
	if result.ForcedTrack != 1 { // Second subtitle stream
		t.Errorf("expected forced track at index 1, got %d", result.ForcedTrack)
	}
	if !result.HasRegularSubs {
		t.Error("expected regular subs to be present")
	}
	if !result.HasForcedSubs {
		t.Error("expected forced subs to be present")
	}
	if !result.LanguageMatch {
		t.Error("expected language match")
	}
}

func TestAnalyzeSubtitleStreamsLanguageMismatch(t *testing.T) {
	streams := []ffprobe.Stream{
		{
			CodecType: "subtitle",
			Index:     0,
			Tags:      map[string]string{"language": "spa"},
		},
	}

	result := analyzeSubtitleStreams(streams, "en")
	if result.LanguageMatch {
		t.Error("expected language mismatch")
	}
}

func TestNormalizeSubtitleLanguage(t *testing.T) {
	tests := []struct {
		tags     map[string]string
		expected string
	}{
		{nil, ""},
		{map[string]string{}, ""},
		{map[string]string{"language": "eng"}, "eng"},
		{map[string]string{"LANGUAGE": "ENG"}, "eng"},
		{map[string]string{"language": "  en  "}, "en"},
	}

	for _, tt := range tests {
		result := normalizeSubtitleLanguage(tt.tags)
		if result != tt.expected {
			t.Errorf("normalizeSubtitleLanguage(%v) = %q, want %q", tt.tags, result, tt.expected)
		}
	}
}

// Integration test - requires a real MKV file with subtitles
func TestValidateMuxedSubtitlesIntegration(t *testing.T) {
	if os.Getenv("TEST_SUBTITLE_MKV") == "" {
		t.Skip("skipping integration test - set TEST_SUBTITLE_MKV to a path with muxed subtitles")
	}

	mkvPath := os.Getenv("TEST_SUBTITLE_MKV")
	expectedCount := 1 // Adjust based on test file

	logger := logging.NewNop()
	err := ValidateMuxedSubtitles(context.Background(), "", mkvPath, expectedCount, "en", logger)
	if err != nil {
		t.Errorf("subtitle mux validation failed: %v", err)
	}
}
