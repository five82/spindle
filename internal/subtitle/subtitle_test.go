package subtitle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/transcription"
)

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

type fakeDisplayTranscriber struct {
	selected transcription.SelectedAudio
	jsonPath string
}

func (f fakeDisplayTranscriber) SelectPrimaryAudioTrack(context.Context, string, string) (transcription.SelectedAudio, error) {
	return f.selected, nil
}

func (f fakeDisplayTranscriber) Transcribe(_ context.Context, _ transcription.TranscribeRequest, progress ...transcription.ProgressFunc) (*transcription.TranscribeResult, error) {
	if len(progress) > 0 && progress[0] != nil {
		progress[0](transcription.PhaseExtract, 0)
		progress[0](transcription.PhaseExtract, time.Second)
		progress[0](transcription.PhaseTranscribe, 0)
		progress[0](transcription.PhaseTranscribe, 2*time.Second)
	}
	return &transcription.TranscribeResult{
		JSONPath:       f.jsonPath,
		Duration:       90,
		Segments:       1,
		ExtractTime:    time.Second,
		TranscribeTime: 2 * time.Second,
	}, nil
}

func TestGenerateDisplaySubtitle(t *testing.T) {
	origInspect := inspectSubtitleMedia
	origRun := runStableTS
	t.Cleanup(func() {
		inspectSubtitleMedia = origInspect
		runStableTS = origRun
	})

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "audio.json")
	payload := whisperXPayload{
		Language: "en",
		Segments: []map[string]any{{
			"start": 1.0,
			"end":   3.0,
			"text":  "General Kenobi",
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	inspectSubtitleMedia = func(context.Context, string, string) (*ffprobe.Result, error) {
		return &ffprobe.Result{Format: ffprobe.Format{Duration: "123.456"}}, nil
	}
	runStableTS = func(_ context.Context, args []string) ([]byte, error) {
		outputPath := args[6]
		if err := os.WriteFile(outputPath, []byte("1\n00:00:01,000 --> 00:00:03,000\nGeneral Kenobi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}

	var events []string
	result, err := GenerateDisplaySubtitle(context.Background(), GenerateDisplaySubtitleRequest{
		VideoPath:       filepath.Join(dir, "Movie.mkv"),
		DisplayBasePath: filepath.Join(dir, "work", "Movie.mkv"),
		WorkDir:         filepath.Join(dir, "work"),
		Language:        "en",
		Transcriber: fakeDisplayTranscriber{
			selected: transcription.SelectedAudio{Index: 2, Language: "eng", Label: "English"},
			jsonPath: jsonPath,
		},
		Progress: func(phase transcription.Phase, elapsed time.Duration) {
			events = append(events, fmt.Sprintf("progress:%s:%t", phase, elapsed > 0))
		},
		OnAudioSelected: func(transcription.SelectedAudio) {
			events = append(events, "audio")
		},
		OnTranscriptionComplete: func(*transcription.TranscribeResult) {
			events = append(events, "transcription")
		},
		OnDurationSelected: func(float64, string, float64) {
			events = append(events, "duration")
		},
		OnFormattingStart: func() {
			events = append(events, "format-start")
		},
		OnFormattingComplete: func(FormatResult) {
			events = append(events, "format-complete")
		},
	})
	if err != nil {
		t.Fatalf("GenerateDisplaySubtitle() error = %v", err)
	}
	if result.SelectedAudio.Index != 2 || result.VideoSeconds != 123.456 || result.DurationSource != "media_probe" {
		t.Fatalf("result = %+v", result)
	}
	wantPath := filepath.Join(dir, "work", "Movie.en.srt")
	if result.Formatting.DisplayPath != wantPath {
		t.Fatalf("display path = %q, want %q", result.Formatting.DisplayPath, wantPath)
	}
	wantEvents := []string{
		"audio",
		"progress:extract:false",
		"progress:extract:true",
		"progress:transcribe:false",
		"progress:transcribe:true",
		"transcription",
		"duration",
		"format-start",
		"format-complete",
	}
	if fmt.Sprint(events) != fmt.Sprint(wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
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

func TestAuditMediaContext(t *testing.T) {
	tests := []struct {
		name string
		meta ripspec.Metadata
		key  string
		want string
	}{
		{
			name: "movie with year",
			meta: ripspec.Metadata{MediaType: "movie", Title: "Air", Year: "2023"},
			key:  "main",
			want: `the movie "Air" (2023)`,
		},
		{
			name: "movie without year",
			meta: ripspec.Metadata{MediaType: "movie", Title: "Air"},
			key:  "main",
			want: `the movie "Air"`,
		},
		{
			name: "tv with show title",
			meta: ripspec.Metadata{MediaType: "tv", ShowTitle: "Breaking Bad", Title: "Breaking Bad"},
			key:  "s01_001",
			want: "the TV episode Breaking Bad s01_001",
		},
		{
			name: "tv falls back to title when show title empty",
			meta: ripspec.Metadata{MediaType: "tv", Title: "Breaking Bad"},
			key:  "s01_001",
			want: "the TV episode Breaking Bad s01_001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auditMediaContext(tt.meta, tt.key); got != tt.want {
				t.Fatalf("auditMediaContext() = %q, want %q", got, tt.want)
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
