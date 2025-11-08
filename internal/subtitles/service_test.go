package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"spindle/internal/config"
	"spindle/internal/media/ffprobe"
	"spindle/internal/subtitles/opensubtitles"
)

func TestServiceGenerateProducesStableTSSRT_CPUMode(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 120, false)

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
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
	for _, raw := range strings.Split(string(contents), "\n") {
		if strings.HasPrefix(raw, " ") && strings.TrimSpace(raw) != "" && !strings.Contains(raw, "-->") {
			t.Fatalf("unexpected leading space: %q", raw)
		}
	}
	t.Logf("\n%s", string(contents))
	if result.Duration != 120*time.Second {
		t.Fatalf("unexpected duration: %s", result.Duration)
	}
	if !strings.Contains(string(contents), "General Kenobi") {
		t.Fatalf("expected subtitle content to include segment text")
	}
	if strings.Contains(string(contents), "<i>") || strings.Contains(string(contents), "\u266A") {
		t.Fatalf("unexpected lyric styling in output")
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
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	if _, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
	}
}

func TestServiceGenerateRequiresTokenForPyannote(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 30, false)

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	cfg.WhisperXVADMethod = "pyannote"
	cfg.WhisperXHuggingFaceToken = ""
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	if _, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	}); err == nil || !strings.Contains(err.Error(), "Hugging Face token") {
		t.Fatalf("expected configuration error about Hugging Face token, got %v", err)
	}
}

func TestServiceGeneratePyannoteWithToken(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 45, false)

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	cfg.WhisperXVADMethod = "pyannote"
	cfg.WhisperXHuggingFaceToken = "token"
	validator := func(ctx context.Context, token string) (tokenValidationResult, error) {
		if token != "token" {
			return tokenValidationResult{}, fmt.Errorf("unexpected token: %s", token)
		}
		return tokenValidationResult{Account: "pixar-studios"}, nil
	}
	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithTokenValidator(validator),
		WithoutDependencyCheck(),
	)

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected non-zero segments")
	}
	if stub.lastVAD != whisperXVADMethodPyannote {
		t.Fatalf("expected VAD %q, got %q", whisperXVADMethodPyannote, stub.lastVAD)
	}
	if stub.lastHFToken != "token" {
		t.Fatalf("expected hf token to be passed to whisperx")
	}
}

func TestServiceGeneratePyannoteTokenFallbackToSilero(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 45, false)

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	cfg.WhisperXVADMethod = "pyannote"
	cfg.WhisperXHuggingFaceToken = "bad-token"
	validator := func(ctx context.Context, token string) (tokenValidationResult, error) {
		if token != "bad-token" {
			return tokenValidationResult{}, fmt.Errorf("unexpected token: %s", token)
		}
		return tokenValidationResult{}, fmt.Errorf("%w: test rejection", errPyannoteUnauthorized)
	}
	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithTokenValidator(validator),
		WithoutDependencyCheck(),
	)

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected fallback subtitles to contain segments")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if stub.lastVAD != whisperXVADMethodSilero {
		t.Fatalf("expected fallback VAD %q, got %q", whisperXVADMethodSilero, stub.lastVAD)
	}
	if stub.lastHFToken != "" {
		t.Fatalf("expected no HF token when falling back, got %q", stub.lastHFToken)
	}
}

func TestServiceGenerateFallsBackToWhisperSRT(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, []byte("fake video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 75, false)
	stub.stableTSError = fmt.Errorf("stable-ts boom")

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	service := NewService(&cfg, nil, WithCommandRunner(stub.Runner), WithoutDependencyCheck())

	t.Setenv("SPD_DEBUG_SUBTITLES_KEEP", "1")

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisperx command to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts command to be attempted")
	}
	if !stub.calledFFmpeg {
		t.Fatalf("expected ffmpeg extraction to run")
	}

	whisperSRT := filepath.Join(tmp, "work", "whisperx", "primary_audio.srt")
	raw, err := os.ReadFile(whisperSRT)
	if err != nil {
		t.Fatalf("read whisper srt: %v", err)
	}
	final, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read output srt: %v", err)
	}
	if !bytes.Equal(raw, final) {
		t.Fatalf("expected fallback output to match whisper srt")
	}
}

func TestServiceGenerateUsesOpenSubtitlesWhenAvailable(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 1024), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 120, false)
	osStub := &openSubtitlesStub{
		data: []byte(`1
00:00:01,000 --> 00:00:03,000
www.opensubtitles.org

2
00:00:04,000 --> 00:00:06,000
Aligned text
`),
	}

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	cfg.OpenSubtitlesEnabled = true
	cfg.OpenSubtitlesAPIKey = "k"
	cfg.OpenSubtitlesUserAgent = "Spindle/test"
	cfg.OpenSubtitlesLanguages = []string{"en"}

	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithOpenSubtitlesClient(osStub),
		WithoutDependencyCheck(),
	)

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
		Context: SubtitleContext{
			Title:     "Example Movie",
			MediaType: "movie",
			TMDBID:    123,
			Year:      "2024",
		},
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if stub.calledWhisper {
		t.Fatalf("expected whisper transcription to be skipped")
	}
	if !stub.calledAlignment {
		t.Fatalf("expected alignment pass to run")
	}
	if stub.calledStableTS {
		t.Fatalf("did not expect stable-ts for downloaded subtitles")
	}

	contents, err := os.ReadFile(result.SubtitlePath)
	if err != nil {
		t.Fatalf("read subtitles: %v", err)
	}
	output := string(contents)
	if strings.Contains(strings.ToLower(output), "opensubtitles") {
		t.Fatalf("expected advertisement to be removed, got %q", output)
	}
	if !strings.Contains(output, "Aligned text") {
		t.Fatalf("expected aligned text to remain, got %q", output)
	}
	if result.SegmentCount != 1 {
		t.Fatalf("expected segment count 1, got %d", result.SegmentCount)
	}
}

func TestServiceGenerateForceAISkipsOpenSubtitles(t *testing.T) {
	tmp := t.TempDir()
	source := filepath.Join(tmp, "movie.mkv")
	if err := os.WriteFile(source, bytes.Repeat([]byte{0x05, 0x06, 0x07, 0x08}, 1024), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stub := setupInspectAndStub(t, 110, false)
	osStub := &openSubtitlesStub{
		data: []byte(`1
00:00:01,000 --> 00:00:03,000
Example text
`),
	}

	cfg := config.Default()
	cfg.SubtitlesEnabled = true
	cfg.OpenSubtitlesEnabled = true
	cfg.OpenSubtitlesAPIKey = "k"
	cfg.OpenSubtitlesUserAgent = "Spindle/test"
	cfg.OpenSubtitlesLanguages = []string{"en"}

	service := NewService(&cfg, nil,
		WithCommandRunner(stub.Runner),
		WithOpenSubtitlesClient(osStub),
		WithoutDependencyCheck(),
	)

	result, err := service.Generate(context.Background(), GenerateRequest{
		SourcePath: source,
		WorkDir:    filepath.Join(tmp, "work"),
		OutputDir:  filepath.Join(tmp, "out"),
		BaseName:   "movie",
		ForceAI:    true,
		Context: SubtitleContext{
			Title:     "Example Movie",
			MediaType: "movie",
			TMDBID:    456,
			Year:      "2024",
		},
		Languages: []string{"en"},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !stub.calledWhisper {
		t.Fatalf("expected whisper transcription to run")
	}
	if !stub.calledStableTS {
		t.Fatalf("expected stable-ts formatter to run")
	}
	if stub.calledAlignment {
		t.Fatalf("did not expect alignment when skipping OpenSubtitles")
	}
	if osStub.searchCount != 0 {
		t.Fatalf("expected OpenSubtitles search to be skipped, got %d", osStub.searchCount)
	}
	if osStub.downloadCount != 0 {
		t.Fatalf("expected OpenSubtitles download to be skipped, got %d", osStub.downloadCount)
	}
	if result.SegmentCount == 0 {
		t.Fatalf("expected AI subtitles to contain segments")
	}
}

type whisperXStub struct {
	t               *testing.T
	expectCUDA      bool
	calledWhisper   bool
	calledStableTS  bool
	calledFFmpeg    bool
	calledAlignment bool
	duration        float64
	lastVAD         string
	lastHFToken     string
	stableTSError   error
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

func (s *whisperXStub) Runner(ctx context.Context, name string, args ...string) error {
	if name == ffmpegCommand {
		s.calledFFmpeg = true
		if len(args) == 0 {
			s.t.Fatalf("ffmpeg called without arguments")
		}
		dest := args[len(args)-1]
		if !strings.HasSuffix(dest, ".wav") {
			s.t.Fatalf("expected ffmpeg output to be .wav, got %q", dest)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			s.t.Fatalf("mkdir audio dir: %v", err)
		}
		if err := os.WriteFile(dest, []byte("fake-wav"), 0o644); err != nil {
			s.t.Fatalf("write wav: %v", err)
		}
		return nil
	}

	if name == whisperXCommand && containsArg(args, "--output_dir") {
		if containsArg(args, "--download-models") || containsArg(args, "--model_cache_only") {
			return nil
		}
		s.calledWhisper = true

		var outputDir, sourcePath, indexURL, extraIndexURL, device, computeType, hfToken, vadMethod string
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
			case "--hf_token":
				if i+1 < len(args) {
					hfToken = args[i+1]
				}
			case "--vad_method":
				if i+1 < len(args) {
					vadMethod = args[i+1]
				}
			default:
				if strings.HasSuffix(args[i], ".wav") {
					sourcePath = args[i]
				}
			}
		}

		if outputDir == "" {
			s.t.Fatal("command missing --output_dir")
		}
		if sourcePath == "" || !strings.HasSuffix(sourcePath, ".wav") {
			s.t.Fatalf("command missing audio source path, got %q", sourcePath)
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

		if vadMethod == "" {
			s.t.Fatal("expected --vad_method to be provided")
		}
		switch vadMethod {
		case whisperXVADMethodPyannote:
			if hfToken == "" {
				s.t.Fatal("expected --hf_token to be provided for pyannote VAD")
			}
		default:
			if hfToken != "" {
				s.t.Fatalf("did not expect --hf_token for VAD method %q", vadMethod)
			}
		}
		s.lastVAD = vadMethod
		s.lastHFToken = hfToken

		base := strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
		if err := s.writeWhisperOutputs(outputDir, base); err != nil {
			s.t.Fatalf("write whisper outputs: %v", err)
		}
		return nil
	}

	var pkg string
	for i := 0; i < len(args); i++ {
		if args[i] == "--from" && i+1 < len(args) {
			pkg = args[i+1]
			break
		}
	}
	if pkg == whisperXPackage {
		s.calledAlignment = true
		idxScript := -1
		for i := 0; i < len(args); i++ {
			if args[i] == "-c" {
				idxScript = i
				break
			}
		}
		if idxScript < 0 || idxScript+5 >= len(args) {
			s.t.Fatalf("unexpected whisperx align args: %v", args)
		}
		inputPath := args[idxScript+3]
		outputPath := args[idxScript+4]
		data, err := os.ReadFile(inputPath)
		if err != nil {
			s.t.Fatalf("read align input: %v", err)
		}
		if err := os.WriteFile(outputPath, data, 0o644); err != nil {
			s.t.Fatalf("write align output: %v", err)
		}
		return nil
	}
	if pkg == stableTSPackage {
		s.calledStableTS = true
		if s.stableTSError != nil {
			return s.stableTSError
		}
		idxScript := -1
		for i := 0; i < len(args); i++ {
			if args[i] == "-c" {
				idxScript = i
				break
			}
		}
		if idxScript < 0 || idxScript+3 >= len(args) {
			s.t.Fatalf("unexpected stable-ts formatter args: %v", args)
		}
		jsonPath := args[idxScript+2]
		tmpOutput := args[idxScript+3]
		language := "en"
		for j := idxScript + 4; j < len(args)-1; j++ {
			if args[j] == "--language" && j+1 < len(args) {
				language = args[j+1]
				break
			}
		}
		if err := s.simulateStableTSFormatter(jsonPath, tmpOutput, language); err != nil {
			return err
		}
		return nil
	}

	s.t.Fatalf("unexpected command: %s %v", name, args)
	return nil
}

func (s *whisperXStub) simulateStableTSFormatter(jsonPath, outputPath, language string) error {
	segments, err := loadWhisperSegments(jsonPath)
	if err != nil {
		return fmt.Errorf("load whisper segments: %w", err)
	}
	var builder strings.Builder
	index := 1
	for _, seg := range segments {
		text := buildSentence(seg.Words)
		if text == "" {
			text = strings.TrimSpace(seg.Text)
		}
		if text == "" {
			continue
		}
		start := formatSRTTimestamp(seg.Start)
		end := formatSRTTimestamp(seg.End)
		fmt.Fprintf(&builder, "%d\n%s --> %s\n%s\n\n", index, start, end, text)
		index++
	}
	if index == 1 {
		return fmt.Errorf("no cues generated for stable-ts formatter")
	}
	return os.WriteFile(outputPath, []byte(builder.String()), 0o644)
}

func (s *whisperXStub) writeWhisperOutputs(outputDir, base string) error {
	srtPath := filepath.Join(outputDir, base+".srt")
	content := `1
00:00:00,000 --> 00:00:02,000
Thank you.

2
00:00:02,500 --> 00:00:04,500
General Kenobi, you are a bold one.

3
00:00:05,000 --> 00:00:07,000
Somebody stop that force field.

`
	if err := os.WriteFile(srtPath, []byte(content), 0o644); err != nil {
		return err
	}

	payload := whisperXPayload{
		Segments: []whisperXSegment{
			{
				Text:  "Thank you.",
				Start: 0.0,
				End:   2.0,
				Words: []whisperXWord{
					{Word: "Thank", Start: 0.0, End: 1.0},
					{Word: "you.", Start: 1.0, End: 2.0},
				},
			},
			{
				Text:  "General Kenobi, you are a bold one.",
				Start: 2.5,
				End:   4.5,
				Words: []whisperXWord{
					{Word: "General", Start: 2.5, End: 3.0},
					{Word: "Kenobi,", Start: 3.0, End: 3.3},
					{Word: "you", Start: 3.3, End: 3.7},
					{Word: "are", Start: 3.7, End: 3.9},
					{Word: "a", Start: 3.9, End: 4.0},
					{Word: "bold", Start: 4.0, End: 4.2},
					{Word: "one.", Start: 4.2, End: 4.5},
				},
			},
			{
				Text:  "Somebody stop that force field.",
				Start: 5.0,
				End:   7.0,
				Words: []whisperXWord{
					{Word: "Somebody", Start: 5.0, End: 5.6},
					{Word: "stop", Start: 5.6, End: 6.0},
					{Word: "that", Start: 6.0, End: 6.3},
					{Word: "force", Start: 6.3, End: 6.6},
					{Word: "field.", Start: 6.6, End: 7.0},
				},
			},
		},
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	jsonPath := filepath.Join(outputDir, base+".json")
	return os.WriteFile(jsonPath, jsonData, 0o644)
}

func formatDurationSeconds(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", value), "0"), ".")
}

func buildSentence(words []whisperXWord) string {
	var builder strings.Builder
	for _, word := range words {
		token := strings.TrimSpace(word.Word)
		if token == "" {
			continue
		}
		if builder.Len() > 0 {
			r, _ := utf8.DecodeRuneInString(token)
			if r != 0 && !strings.ContainsRune("',’).,!?:;", r) {
				builder.WriteByte(' ')
			}
		}
		builder.WriteString(token)
	}
	return builder.String()
}

func formatSRTTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	msTotal := int(seconds*1000 + 0.5)
	hours := msTotal / 3_600_000
	msTotal %= 3_600_000
	minutes := msTotal / 60_000
	msTotal %= 60_000
	secs := msTotal / 1_000
	millis := msTotal % 1_000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, secs, millis)
}

func containsArg(args []string, needle string) bool {
	for _, a := range args {
		if a == needle {
			return true
		}
	}
	return false
}

type openSubtitlesStub struct {
	data          []byte
	searchCount   int
	downloadCount int
}

func (s *openSubtitlesStub) Search(ctx context.Context, req opensubtitles.SearchRequest) (opensubtitles.SearchResponse, error) {
	s.searchCount++
	return opensubtitles.SearchResponse{
		Subtitles: []opensubtitles.Subtitle{
			{
				ID:              "candidate-1",
				FileID:          42,
				Language:        "en",
				Downloads:       100,
				FeatureTitle:    req.Query,
				FeatureType:     req.MediaType,
				HearingImpaired: false,
				AITranslated:    false,
			},
		},
		Total: 1,
	}, nil
}

func (s *openSubtitlesStub) Download(ctx context.Context, fileID int64, opts opensubtitles.DownloadOptions) (opensubtitles.DownloadResult, error) {
	if fileID != 42 {
		return opensubtitles.DownloadResult{}, fmt.Errorf("unexpected file id %d", fileID)
	}
	s.downloadCount++
	return opensubtitles.DownloadResult{
		Data:     append([]byte(nil), s.data...),
		FileName: "movie.en.srt",
		Language: "en",
	}, nil
}
