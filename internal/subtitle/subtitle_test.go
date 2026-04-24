package subtitle

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/transcription"
)

func TestOverallSubtitlePercent(t *testing.T) {
	tests := []struct {
		name       string
		completed  int
		total      int
		currentPct float64
		want       float64
	}{
		{name: "first item half done", completed: 0, total: 4, currentPct: 50, want: 12.5},
		{name: "three complete", completed: 3, total: 4, currentPct: 0, want: 75},
		{name: "all complete", completed: 4, total: 4, currentPct: 0, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := overallSubtitlePercent(tt.completed, tt.total, tt.currentPct)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("overallSubtitlePercent() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestSubtitlePhasePercent(t *testing.T) {
	if got := subtitlePhasePercent(transcription.PhaseExtract, 0); got != 10 {
		t.Fatalf("extract start = %f, want 10", got)
	}
	if got := subtitlePhasePercent(transcription.PhaseExtract, time.Second); got != 25 {
		t.Fatalf("extract done = %f, want 25", got)
	}
	if got := subtitlePhasePercent(transcription.PhaseTranscribe, 0); got != 35 {
		t.Fatalf("transcribe start = %f, want 35", got)
	}
	if got := subtitlePhasePercent(transcription.PhaseTranscribe, time.Second); got != 90 {
		t.Fatalf("transcribe done = %f, want 90", got)
	}
}

func TestAssetKeys_Movie(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}
	keys := env.AssetKeys()
	if len(keys) != 1 || keys[0] != "main" {
		t.Fatalf("expected [main], got %v", keys)
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
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d]: expected %s, got %s", i, expected[i], k)
		}
	}
}

func TestAssetKeys_TVSkipsEmptyKeys(t *testing.T) {
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
		t.Fatalf("expected 2 keys (skipping empty), got %d: %v", len(keys), keys)
	}
	if keys[0] != "s01e01" || keys[1] != "s01e03" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestAssetKeys_TVNoEpisodes(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
	}
	keys := env.AssetKeys()
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys for TV with no episodes, got %d", len(keys))
	}
}

func TestUpsertSubtitleGenRecordReplacesExisting(t *testing.T) {
	records := []ripspec.SubtitleGenRecord{
		{EpisodeKey: "S01E01", SubtitlePath: "old.srt", Language: "en"},
		{EpisodeKey: "S01E02", SubtitlePath: "keep.srt", Language: "en"},
	}

	upsertSubtitleGenRecord(&records, ripspec.SubtitleGenRecord{EpisodeKey: "s01e01", SubtitlePath: "new.srt", Language: "en"})

	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if records[0].SubtitlePath != "new.srt" {
		t.Fatalf("record was not replaced: %+v", records[0])
	}
	if records[1].SubtitlePath != "keep.srt" {
		t.Fatalf("unrelated record changed: %+v", records[1])
	}
}

func TestRankForcedSubtitleCandidates(t *testing.T) {
	mkResult := func(id, lang, release string, downloads, fileID int) opensubtitles.SubtitleResult {
		return opensubtitles.SubtitleResult{
			ID: id,
			Attributes: opensubtitles.SubtitleAttributes{
				Language:         lang,
				Release:          release,
				DownloadCount:    downloads,
				ForeignPartsOnly: true,
				Files:            []opensubtitles.SubtitleFile{{FileID: fileID}},
			},
		}
	}

	t.Run("prefers configured language before downloads", func(t *testing.T) {
		results := []opensubtitles.SubtitleResult{
			mkResult("spanish", "es", "BluRay", 500, 20),
			mkResult("english", "en", "BluRay", 50, 30),
		}
		idx, ok := rankForcedSubtitleCandidates(results, []string{"en", "es"})
		if !ok || idx != 1 {
			t.Fatalf("rankForcedSubtitleCandidates() = %d, %v; want 1, true", idx, ok)
		}
	})

	t.Run("filters garbage release", func(t *testing.T) {
		results := []opensubtitles.SubtitleResult{
			mkResult("cam", "en", "CAM", 500, 20),
			mkResult("bluray", "en", "BluRay", 50, 30),
		}
		idx, ok := rankForcedSubtitleCandidates(results, []string{"en"})
		if !ok || idx != 1 {
			t.Fatalf("rankForcedSubtitleCandidates() = %d, %v; want 1, true", idx, ok)
		}
	})

	t.Run("uses file id tiebreaker", func(t *testing.T) {
		results := []opensubtitles.SubtitleResult{
			mkResult("later", "en", "BluRay", 50, 30),
			mkResult("earlier", "en", "BluRay", 50, 20),
		}
		idx, ok := rankForcedSubtitleCandidates(results, []string{"en"})
		if !ok || idx != 1 {
			t.Fatalf("rankForcedSubtitleCandidates() = %d, %v; want 1, true", idx, ok)
		}
	})
}

func TestResolveSubtitleVideoDuration(t *testing.T) {
	origInspect := inspectSubtitleMedia
	t.Cleanup(func() { inspectSubtitleMedia = origInspect })

	inspectSubtitleMedia = func(ctx context.Context, binary, path string) (*ffprobe.Result, error) {
		if path == "/tmp/fail.mkv" {
			return nil, fmt.Errorf("probe failed")
		}
		return &ffprobe.Result{Format: ffprobe.Format{Duration: "123.456"}}, nil
	}

	if got, source := resolveSubtitleVideoDuration(context.Background(), "/tmp/video.mkv", 90); got != 123.456 || source != "media_probe" {
		t.Fatalf("resolveSubtitleVideoDuration() = %v, %q; want 123.456, media_probe", got, source)
	}
	if got, source := resolveSubtitleVideoDuration(context.Background(), "/tmp/fail.mkv", 90); got != 90 || source != "transcript_fallback" {
		t.Fatalf("fallback = %v, %q; want 90, transcript_fallback", got, source)
	}
}
