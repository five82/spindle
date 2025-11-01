package subtitles

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/config"
	"spindle/internal/media/ffprobe"
)

func TestBuildNetflixCuesRespectsLimits(t *testing.T) {
	words := []word{
		{Text: "Hello", Start: 0.0, End: 0.6},
		{Text: "there", Start: 0.6, End: 1.1},
		{Text: "general", Start: 1.1, End: 1.6},
		{Text: "Kenobi.", Start: 1.6, End: 2.2},
		{Text: "You", Start: 3.0, End: 3.5},
		{Text: "are", Start: 3.5, End: 4.0},
		{Text: "a", Start: 4.0, End: 4.2},
		{Text: "bold", Start: 4.2, End: 4.7},
		{Text: "one.", Start: 4.7, End: 5.2},
	}
	segment := whisperSegment{Start: words[0].Start, End: words[len(words)-1].End}
	segment.Words = make([]whisperWordJSON, len(words))
	for i, w := range words {
		segment.Words[i] = whisperWordJSON{Word: w.Text, Start: w.Start, End: w.End}
	}

	groups := buildCueWordGroups([]whisperSegment{segment})
	if len(groups) == 0 {
		t.Fatal("expected cue groups to be produced")
	}
	cues := convertCueWordsToCues(groups)
	cues = enforceCueDurations(cues)
	if len(cues) == 0 {
		t.Fatal("expected cues to be produced")
	}
	for _, cue := range cues {
		lines := strings.Split(cue.Text, "\n")
		if len(lines) > netflixMaxLines {
			t.Fatalf("expected <=%d lines, got %d (%q)", netflixMaxLines, len(lines), cue.Text)
		}
		for _, line := range lines {
			if len([]rune(line)) > netflixMaxLineChars {
				t.Fatalf("line exceeds %d chars: %q", netflixMaxLineChars, line)
			}
		}
		duration := cue.End - cue.Start
		if duration > netflixMaxCueDuration.Seconds()+0.01 {
			t.Fatalf("duration exceeds max: %.2f", duration)
		}
		if duration < netflixMinCueDuration.Seconds()-0.02 {
			t.Fatalf("duration below min: %.2f", duration)
		}
		cps := float64(countCharacters(strings.ReplaceAll(cue.Text, "\n", ""))) / duration
		if cps > netflixMaxCharsPerSec+0.1 {
			t.Fatalf("CPS exceeds limit: %.2f", cps)
		}
	}
}

func TestServiceGenerateProducesNetflixSRT_CPUMode(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 120, false)

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner))

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.called {
		t.Fatalf("expected command runner to be called")
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected non-zero segments")
	}
	if _, err := os.Stat(result.SubtitlePath); err != nil {
		t.Fatalf("expected subtitle file to exist: %v", err)
	}
	contents, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read srt: %v", err)
	}
	t.Logf("\n%s", string(contents))
	lines := strings.Split(string(contents), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "-->") || strings.HasPrefix(line, "1") || strings.HasPrefix(line, "2") {
			continue
		}
		if len([]rune(line)) > netflixMaxLineChars {
			t.Fatalf("line exceeds limit: %q", line)
		}
	}
	if result.Duration != 120*time.Second {
		t.Fatalf("unexpected duration: %s", result.Duration)
	}
	if !strings.Contains(string(contents), "<i>") {
		t.Fatalf("expected italicized lyric cue in output")
	}
	if !strings.Contains(string(contents), "\u266A") {
		t.Fatalf("expected lyric cue to include music note")
	}
	if strings.Contains(string(contents), "\nThank\n") {
		t.Fatalf("expected lyric fragments to merge, found isolated 'Thank'")
	}
	if strings.Contains(string(contents), "Somebody do\n") {
		t.Fatalf("expected 'Somebody do something' to stay in a single cue")
	}
}

func TestServiceGenerateUsesCUDAArgsWhenEnabled(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 60, true)

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	cfg.WhisperXCUDAEnabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner))

	if _, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.called {
		t.Fatalf("expected command runner to be called")
	}
}

func TestMergeShortCueWordsCombinesAdjacentWords(t *testing.T) {
	groups := []cueWordGroup{
		{Words: []word{{Text: "Stop", Start: 0.0, End: 0.1}}},
		{Words: []word{{Text: "it,", Start: 0.12, End: 0.22}}},
		{Words: []word{{Text: "stop", Start: 0.23, End: 0.33}}},
		{Words: []word{{Text: "it,", Start: 0.34, End: 0.44}}},
		{Words: []word{{Text: "you", Start: 0.45, End: 0.55}}},
		{Words: []word{{Text: "mean", Start: 0.56, End: 0.66}}},
		{Words: []word{{Text: "old", Start: 0.67, End: 0.77}}},
		{Words: []word{{Text: "potato.", Start: 0.78, End: 1.20}}},
	}
	merged := mergeShortCueWords(groups)
	if len(merged) > 2 {
		t.Fatalf("expected merged cues to be compact, got %d", len(merged))
	}
	first := strings.Join(wrapText(joinWords(merged[0].Words)), " ")
	if len(strings.Fields(first)) < 3 {
		t.Fatalf("expected first cue to contain multiple words, got %q", first)
	}
}

type whisperXStub struct {
	t          *testing.T
	expectCUDA bool
	called     bool
	duration   float64
}

func setupInspectAndStub(t *testing.T, durationSeconds float64, expectCUDA bool) *whisperXStub {
	origInspect := inspectMedia
	t.Cleanup(func() {
		inspectMedia = origInspect
	})
	inspectMedia = func(ctx context.Context, binary, path string) (ffprobe.Result, error) {
		return ffprobe.Result{
			Streams: []ffprobe.Stream{
				{Index: 0, CodecType: "audio", CodecName: "aac", Tags: map[string]string{"language": "eng"}},
			},
			Format: ffprobe.Format{Duration: formatDurationSeconds(durationSeconds)},
		}, nil
	}
	return &whisperXStub{
		t:          t,
		expectCUDA: expectCUDA,
		duration:   durationSeconds,
	}
}

func (s *whisperXStub) Runner(_ context.Context, _ string, args ...string) error {
	s.called = true

	var outputDir, sourcePath, indexURL, extraIndexURL, device, computeType string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output_dir":
			if i+1 < len(args) {
				outputDir = args[i+1]
			}
		case "--index-url":
			if i+1 < len(args) {
				indexURL = args[i+1]
			}
		case "--extra-index-url":
			if i+1 < len(args) {
				extraIndexURL = args[i+1]
			}
		case "--device":
			if i+1 < len(args) {
				device = args[i+1]
			}
		case "--compute_type":
			if i+1 < len(args) {
				computeType = args[i+1]
			}
		default:
			if strings.HasSuffix(args[i], ".mkv") {
				sourcePath = args[i]
			}
		}
	}

	if outputDir == "" {
		s.t.Fatal("command missing --output_dir")
	}
	if sourcePath == "" {
		s.t.Fatal("command missing source path")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		s.t.Fatalf("mkdir output: %v", err)
	}

	switch {
	case s.expectCUDA:
		if device != whisperXCUDADevice {
			s.t.Fatalf("expected cuda device, got %q", device)
		}
		if indexURL != whisperXCUDAIndexURL {
			s.t.Fatalf("expected cuda index url %q, got %q", whisperXCUDAIndexURL, indexURL)
		}
		if extraIndexURL != whisperXPypiIndexURL {
			s.t.Fatalf("expected extra index url %q, got %q", whisperXPypiIndexURL, extraIndexURL)
		}
		if computeType != "" {
			s.t.Fatalf("unexpected compute type in CUDA mode: %q", computeType)
		}
	default:
		if device != whisperXCPUDevice {
			s.t.Fatalf("expected cpu device, got %q", device)
		}
		if computeType != whisperXCPUComputeType {
			s.t.Fatalf("expected compute type %q, got %q", whisperXCPUComputeType, computeType)
		}
		if indexURL != whisperXPypiIndexURL {
			s.t.Fatalf("expected cpu index url %q, got %q", whisperXPypiIndexURL, indexURL)
		}
		if extraIndexURL != "" {
			s.t.Fatalf("unexpected extra index url in CPU mode: %q", extraIndexURL)
		}
	}

	base := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	payload := whisperJSON{
		Segments: []whisperSegment{
			{
				Start: 0.0,
				End:   0.7,
				Text:  "Thank",
				Words: []whisperWordJSON{{Word: "Thank", Start: 0.0, End: 0.7}},
			},
			{
				Start: 0.8,
				End:   1.4,
				Text:  "you",
				Words: []whisperWordJSON{{Word: "you", Start: 0.8, End: 1.4}},
			},
			{
				Start: 1.5,
				End:   2.2,
				Text:  "Thank",
				Words: []whisperWordJSON{{Word: "Thank", Start: 1.5, End: 2.2}},
			},
			{
				Start: 2.3,
				End:   3.0,
				Text:  "you",
				Words: []whisperWordJSON{{Word: "you", Start: 2.3, End: 3.0}},
			},
			{
				Start: 3.5,
				End:   6.0,
				Text:  "Hello there.",
				Words: []whisperWordJSON{
					{Word: "Hello", Start: 3.5, End: 4.1},
					{Word: "there.", Start: 4.1, End: 6.0},
				},
			},
			{
				Start: 7.0,
				End:   9.5,
				Text:  "General Kenobi, you are a bold one.",
				Words: []whisperWordJSON{
					{Word: "General", Start: 7.0, End: 7.6},
					{Word: "Kenobi,", Start: 7.6, End: 8.2},
					{Word: "you", Start: 8.2, End: 8.6},
					{Word: "are", Start: 8.6, End: 9.0},
					{Word: "a", Start: 9.0, End: 9.2},
					{Word: "bold", Start: 9.2, End: 9.4},
					{Word: "one.", Start: 9.4, End: 9.5},
				},
			},
			{
				Start: 10.0,
				End:   13.0,
				Text:  "You got a friend in me",
				Words: []whisperWordJSON{
					{Word: "You", Start: 10.0, End: 10.8},
					{Word: "got", Start: 10.8, End: 11.3},
					{Word: "a", Start: 11.3, End: 11.5},
					{Word: "friend", Start: 11.5, End: 12.1},
					{Word: "in", Start: 12.1, End: 12.4},
					{Word: "me", Start: 12.4, End: 13.0},
				},
			},
			{
				Start: 13.5,
				End:   14.8,
				Text:  "Somebody do",
				Words: []whisperWordJSON{
					{Word: "Somebody", Start: 13.5, End: 14.2},
					{Word: "do", Start: 14.2, End: 14.8},
				},
			},
			{
				Start: 14.8,
				End:   15.6,
				Text:  "something.",
				Words: []whisperWordJSON{{Word: "something.", Start: 14.8, End: 15.6}},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		s.t.Fatalf("marshal payload: %v", err)
	}
	jsonPath := filepath.Join(outputDir, base+".json")
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		s.t.Fatalf("write json: %v", err)
	}
	return nil
}

func formatDurationSeconds(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", value), "0"), ".")
}
