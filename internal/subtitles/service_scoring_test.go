package subtitles

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/subtitles/opensubtitles"
)

func TestRankSubtitleCandidatesPrefersBluRayOverWeb(t *testing.T) {
	subs := []opensubtitles.Subtitle{
		{FileID: 1, Language: "en", Release: "Michael.Clayton.2007.1080p.BluRay.x264", Downloads: 600, FeatureYear: 2007},
		{FileID: 2, Language: "en", Release: "Michael.Clayton.2007.WEB-DL.x264", Downloads: 6000, FeatureYear: 2007},
	}
	ordered := rankSubtitleCandidates(subs, []string{"en"}, SubtitleContext{MediaType: "movie", Year: "2007"})
	if len(ordered) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(ordered))
	}
	if ordered[0].subtitle.FileID != 1 {
		t.Fatalf("expected BluRay release to rank first, got file id %d", ordered[0].subtitle.FileID)
	}
}

func TestRankSubtitleCandidatesRespectsLanguageBuckets(t *testing.T) {
	subs := []opensubtitles.Subtitle{
		{FileID: 1, Language: "es", Release: "Movie.1080p.BluRay", Downloads: 200},
		{FileID: 2, Language: "en", Release: "Movie.1080p.BluRay", Downloads: 50},
		{FileID: 3, Language: "en", Release: "Movie.WEB-DL", Downloads: 150, AITranslated: true},
	}
	ordered := rankSubtitleCandidates(subs, []string{"en"}, SubtitleContext{MediaType: "movie"})
	if len(ordered) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(ordered))
	}
	if ordered[0].subtitle.FileID != 2 {
		t.Fatalf("expected human preferred language first, got %d", ordered[0].subtitle.FileID)
	}
	if ordered[1].subtitle.FileID != 3 {
		t.Fatalf("expected AI preferred language second, got %d", ordered[1].subtitle.FileID)
	}
	if ordered[2].subtitle.FileID != 1 {
		t.Fatalf("expected fallback language last, got %d", ordered[2].subtitle.FileID)
	}
}

func TestCheckSubtitleDurationRejectsLargeDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.srt")
	contents := "1\n00:00:00,000 --> 00:00:30,000\nHello\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	delta, mismatch, err := checkSubtitleDuration(path, 60)
	if err != nil {
		t.Fatalf("checkSubtitleDuration returned error: %v", err)
	}
	if !mismatch {
		t.Fatalf("expected mismatch, got none")
	}
	if math.Abs(delta-30) > 0.1 {
		t.Fatalf("expected delta about 30, got %.2f", delta)
	}
}

func TestCheckSubtitleDurationAllowsSmallDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.srt")
	contents := "1\n00:00:00,000 --> 01:00:05,000\nHello\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	delta, mismatch, err := checkSubtitleDuration(path, 3600)
	if err != nil {
		t.Fatalf("checkSubtitleDuration returned error: %v", err)
	}
	if mismatch {
		t.Fatalf("expected no mismatch, got delta %.2f", delta)
	}
}
