package subtitles

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"spindle/internal/config"
	"spindle/internal/media/ffprobe"
)

func TestBuildChunksRespectsOverlap(t *testing.T) {
	svc := &Service{chunkDuration: 15 * time.Minute, chunkOverlap: 2 * time.Second}
	chunks, err := svc.buildChunks(3600) // one hour
	if err != nil {
		t.Fatalf("buildChunks returned error: %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks for 60m duration, got %d", len(chunks))
	}
	if chunks[1].Start >= chunks[1].End {
		t.Fatalf("chunk start >= end: %+v", chunks[1])
	}
	if chunks[1].Start >= chunks[0].End {
		t.Fatalf("expected overlap between chunks: %+v %+v", chunks[0], chunks[1])
	}
	if diff := math.Abs(chunks[1].Base - chunks[0].End); diff > 0.5 {
		t.Fatalf("expected chunk1 base to align with chunk0 end: base=%f end=%f", chunks[1].Base, chunks[0].End)
	}
}

func TestAppendCueMergesDuplicates(t *testing.T) {
	cues := []Cue{}
	cues = appendCue(cues, Cue{Start: 0, End: 1, Text: "Hello"})
	cues = appendCue(cues, Cue{Start: 0.05, End: 1.2, Text: "hello"})
	if len(cues) != 1 {
		t.Fatalf("expected merged cues, got %d", len(cues))
	}
	if cues[0].End <= 1.1 {
		t.Fatalf("expected extended cue end, got %f", cues[0].End)
	}

	prev := Cue{Start: 9.8, End: 10.2, Text: "Hello"}
	next := Cue{Start: 10.25, End: 10.6, Text: "hello"}
	if _, ok := mergeCue(prev, next); !ok {
		t.Fatalf("expected cues to merge by end gap")
	}

	prev = Cue{Start: 59.0, End: 60.0, Text: "Mrs. Potato Head."}
	next = Cue{Start: 60.0, End: 61.5, Text: "Mrs. Potato Head, Mrs. Potato Head, Mrs. Potato Head."}
	merged, ok := mergeCue(prev, next)
	if !ok {
		t.Fatalf("expected cues to merge when next text extends previous")
	}
	if !strings.Contains(merged.Text, ",") {
		t.Fatalf("expected merged text to prefer extended phrase, got %q", merged.Text)
	}
}

func TestWriteSRT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.srt")
	cues := []Cue{{Start: 0, End: 1.5, Text: "Hello world"}}
	if err := writeSRT(path, cues); err != nil {
		t.Fatalf("writeSRT returned error: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read srt: %v", err)
	}
	if len(contents) == 0 || string(contents[:1]) != "1" {
		t.Fatalf("unexpected srt contents: %q", string(contents))
	}
}

func TestChooseDemuxPlan(t *testing.T) {
	expect := demuxPlan{Extension: ".opus", Format: "opus", AudioCodec: "libopus", ExtraArgs: []string{"-ac", "1", "-ar", "16000", "-b:a", "64000"}}
	plan := chooseDemuxPlan()
	if plan.Extension != expect.Extension || plan.Format != expect.Format || plan.AudioCodec != expect.AudioCodec || !reflect.DeepEqual(plan.ExtraArgs, expect.ExtraArgs) {
		t.Fatalf("unexpected plan %+v", plan)
	}
}

func TestGenerateUsesChunkStartForOffsets(t *testing.T) {
	t.Helper()

	tmp := t.TempDir()
	source := filepath.Join(tmp, "source.mkv")
	if err := os.WriteFile(source, []byte("fake-video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	origInspect := inspectMedia
	t.Cleanup(func() {
		inspectMedia = origInspect
	})
	inspectMedia = func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return ffprobe.Result{
			Streams: []ffprobe.Stream{
				{Index: 0, CodecType: "audio", CodecName: "aac", Tags: map[string]string{"language": "eng"}},
			},
			Format: ffprobe.Format{Duration: "40"},
		}, nil
	}

	cfg := config.Default()
	cfg.SubtitlesEnabled = true

	var responses []TranscriptionResponse
	client := &stubClient{}
	runner := func(_ context.Context, _ string, args ...string) error {
		target := args[len(args)-1]
		return os.WriteFile(target, []byte("audio"), 0o644)
	}

	service := NewService(&cfg, client, nil,
		WithChunkDuration(10*time.Second),
		WithChunkOverlap(2*time.Second),
		WithCommandRunner(runner),
	)

	chunks, err := service.buildChunks(40)
	if err != nil {
		t.Fatalf("buildChunks: %v", err)
	}
	responses = make([]TranscriptionResponse, len(chunks))
	for i, chunk := range chunks {
		span := chunk.End - chunk.Start
		overlap := math.Min(2, span)
		mainEnd := span
		segments := []TranscribedSpan{
			{Start: 0, End: overlap, Text: fmt.Sprintf("chunk%d-overlap", i)},
		}
		if mainEnd > overlap+0.05 {
			segments = append(segments, TranscribedSpan{
				Start: overlap,
				End:   mainEnd,
				Text:  fmt.Sprintf("chunk%d-main", i),
			})
		}
		responses[i] = TranscriptionResponse{Segments: segments}
	}
	client.responses = responses

	workDir := filepath.Join(tmp, "work")
	outputDir := filepath.Join(tmp, "out")
	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    workDir,
		OutputDir:  outputDir,
		BaseName:   "example",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	data, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read subtitle: %v", err)
	}
	starts := parseCueStarts(string(data))
	if _, ok := starts["chunk1-overlap"]; ok {
		t.Fatalf("expected chunk1-overlap cue to be trimmed, subtitles:\n%s", string(data))
	}

	got, ok := starts["chunk1-main"]
	if !ok {
		t.Fatalf("expected chunk1-main cue, subtitles:\n%s", string(data))
	}
	if math.Abs(got-10.0) > 0.05 {
		t.Fatalf("expected chunk1-main at ~10s, got %.3f", got)
	}

	got, ok = starts["chunk2-main"]
	if !ok {
		t.Fatalf("expected chunk2-main cue, subtitles:\n%s", string(data))
	}
	if math.Abs(got-20.0) > 0.05 {
		t.Fatalf("expected chunk2-main at ~20s, got %.3f", got)
	}
}

type stubClient struct {
	responses []TranscriptionResponse
	calls     int
}

func (s *stubClient) Transcribe(ctx context.Context, req TranscriptionRequest) (TranscriptionResponse, error) {
	if s.calls >= len(s.responses) {
		return TranscriptionResponse{}, fmt.Errorf("unexpected transcription call %d", s.calls)
	}
	resp := s.responses[s.calls]
	s.calls++
	return resp, nil
}

func parseCueStarts(contents string) map[string]float64 {
	result := make(map[string]float64)
	blocks := strings.Split(strings.TrimSpace(contents), "\n\n")
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 3 {
			continue
		}
		timeParts := strings.Split(lines[1], " --> ")
		if len(timeParts) != 2 {
			continue
		}
		start := parseTimestampToSeconds(timeParts[0])
		text := strings.TrimSpace(strings.Join(lines[2:], " "))
		if text != "" {
			result[text] = start
		}
	}
	return result
}

func parseTimestampToSeconds(value string) float64 {
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return 0
	}
	main := parts[0]
	msPart := parts[1]
	timePieces := strings.Split(main, ":")
	if len(timePieces) != 3 {
		return 0
	}
	hours, _ := strconv.Atoi(timePieces[0])
	minutes, _ := strconv.Atoi(timePieces[1])
	seconds, _ := strconv.Atoi(timePieces[2])
	millis, _ := strconv.Atoi(msPart)
	total := hours*3600 + minutes*60 + seconds
	return float64(total) + float64(millis)/1000.0
}
