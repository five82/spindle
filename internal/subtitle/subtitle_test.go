package subtitle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

func TestStartSubtitleJobLogsRippedInput(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	startSubtitleJob(&stage.Session{Logger: logger}, stage.AssetJob{
		Key:           "s01_001",
		Input:         ripspec.Asset{Path: "/staging/ripped/episode.mkv"},
		ProgressTotal: 1,
	})

	var entry map[string]any
	if err := json.Unmarshal(bytes.Split(output.Bytes(), []byte("\n"))[0], &entry); err != nil {
		t.Fatal(err)
	}
	if entry["msg"] != "ripped asset selected as subtitle input" || entry["decision_result"] != ripspec.AssetKindRipped {
		t.Fatalf("source decision = %#v", entry)
	}
	if entry["path"] != "/staging/ripped/episode.mkv" {
		t.Fatalf("source path = %v", entry["path"])
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

func TestSubtitleValidationResult(t *testing.T) {
	tests := []struct {
		name       string
		validation validationResult
		want       string
	}{
		{name: "observations only pass", validation: validationResult{Issues: []string{"high_reading_speed"}}, want: "passed"},
		{name: "review issues need review", validation: validationResult{ReviewIssues: []string{"high_reading_speed"}}, want: "needs_review"},
		{name: "severe issues fail", validation: validationResult{ReviewIssues: []string{"overlapping_cues"}, SevereIssues: []string{"overlapping_cues"}}, want: "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subtitleValidationResult(tt.validation); got != tt.want {
				t.Fatalf("subtitleValidationResult() = %q, want %q", got, tt.want)
			}
		})
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

func TestResolveSubtitleVideoDuration(t *testing.T) {
	origInspect := inspectSubtitleMedia
	t.Cleanup(func() { inspectSubtitleMedia = origInspect })

	inspectSubtitleMedia = func(ctx context.Context, binary, path string) (*ffprobe.Result, error) {
		if path == "/tmp/fail.mkv" {
			return nil, fmt.Errorf("probe failed")
		}
		return &ffprobe.Result{Format: ffprobe.Format{Duration: "123.456"}}, nil
	}

	if got, source := resolveSubtitleVideoDuration(context.Background(), slog.Default(), "/tmp/video.mkv", 90); got != 123.456 || source != "media_probe" {
		t.Fatalf("resolveSubtitleVideoDuration() = %v, %q; want 123.456, media_probe", got, source)
	}
	if got, source := resolveSubtitleVideoDuration(context.Background(), slog.Default(), "/tmp/fail.mkv", 90); got != 90 || source != "transcript_fallback" {
		t.Fatalf("fallback = %v, %q; want 90, transcript_fallback", got, source)
	}
}
