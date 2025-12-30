package commentary

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
)

const (
	whisperXCommand        = "uvx"
	whisperXPackage        = "whisperx"
	whisperXCUDAIndexURL   = "https://download.pytorch.org/whl/cu128"
	whisperXPypiIndexURL   = "https://pypi.org/simple"
	whisperXModel          = "large-v3"
	whisperXAlignModel     = "WAV2VEC2_ASR_LARGE_LV60K_960H"
	whisperXBatchSize      = "4"
	whisperXChunkSize      = "15"
	whisperXVADOnset       = "0.08"
	whisperXVADOffset      = "0.07"
	whisperXBeamSize       = "10"
	whisperXBestOf         = "10"
	whisperXTemperature    = "0.0"
	whisperXPatience       = "1.0"
	whisperXSegmentRes     = "sentence"
	whisperXOutputFormat   = "srt"
	whisperXCPUDevice      = "cpu"
	whisperXCUDADevice     = "cuda"
	whisperXCPUComputeType = "float32"
)

const (
	whisperXVADMethodPyannote = "pyannote"
	whisperXVADMethodSilero   = "silero"
)

type transcriptScores struct {
	Commentary       int
	AudioDescription int
	Tokens           int
}

func whisperxAvailable(cfg *config.Config) bool {
	if cfg == nil || !cfg.Subtitles.Enabled {
		return false
	}
	if _, err := exec.LookPath(whisperXCommand); err != nil {
		return false
	}
	vad := strings.ToLower(strings.TrimSpace(cfg.Subtitles.WhisperXVADMethod))
	if vad == whisperXVADMethodPyannote && strings.TrimSpace(cfg.Subtitles.WhisperXHuggingFace) == "" {
		return false
	}
	return true
}

func applyWhisperXClassification(ctx context.Context, cfg *config.Config, ffmpegBinary, path string, windows []window, workDir string, decisions []Decision, logger *slog.Logger) []Decision {
	candidates := make([]int, 0)
	for idx, decision := range decisions {
		if decision.Include || decision.Metadata.Negative != "" {
			continue
		}
		switch decision.Reason {
		case "ambiguous", "audio_description", "audio_description_outlier":
			candidates = append(candidates, idx)
		}
	}
	if len(candidates) == 0 {
		return decisions
	}

	for _, idx := range candidates {
		decision := decisions[idx]
		label, scores, err := classifyWithWhisperX(ctx, cfg, ffmpegBinary, path, decision.Metadata.Language, decision.Index, windows, workDir)
		if err != nil {
			if logger != nil {
				logger.Warn("commentary whisperx classification failed",
					logging.Int("stream_index", decision.Index),
					logging.Error(err),
					logging.String(logging.FieldEventType, "commentary_whisperx_failed"),
					logging.String(logging.FieldErrorHint, "verify whisperx availability and try again"),
				)
			}
			continue
		}

		if logger != nil {
			logger.Debug("commentary whisperx classification",
				logging.Int("stream_index", decision.Index),
				logging.String("label", label),
				logging.Int("commentary_score", scores.Commentary),
				logging.Int("audio_description_score", scores.AudioDescription),
				logging.Int("token_count", scores.Tokens),
			)
		}

		switch label {
		case "commentary":
			decisions[idx].Include = true
			decisions[idx].Reason = "whisperx_commentary"
		case "audio_description":
			decisions[idx].Include = false
			decisions[idx].Reason = "whisperx_audio_description"
		}
	}

	return decisions
}

func classifyWithWhisperX(ctx context.Context, cfg *config.Config, ffmpegBinary, path, language string, streamIndex int, windows []window, workDir string) (string, transcriptScores, error) {
	runDir := filepath.Join(workDir, fmt.Sprintf("whisperx-%d", streamIndex))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", transcriptScores{}, err
	}

	samplePath := filepath.Join(runDir, fmt.Sprintf("commentary-%d.wav", streamIndex))
	if err := extractWhisperXSample(ctx, ffmpegBinary, path, streamIndex, windows, samplePath); err != nil {
		return "", transcriptScores{}, err
	}

	args := buildWhisperXArgs(cfg, samplePath, runDir, language)
	if err := runWhisperX(ctx, args); err != nil {
		return "", transcriptScores{}, err
	}

	srtPath := whisperXSRTPath(runDir, samplePath)
	transcript, err := readTranscript(srtPath)
	if err != nil {
		return "", transcriptScores{}, err
	}
	label, scores := classifyTranscript(transcript)
	return label, scores, nil
}

func extractWhisperXSample(ctx context.Context, ffmpegBinary, path string, streamIndex int, windows []window, outputPath string) error {
	if strings.TrimSpace(ffmpegBinary) == "" {
		ffmpegBinary = "ffmpeg"
	}
	filter, label := buildFilter(streamIndex, windows)
	args := []string{"-hide_banner", "-loglevel", "error", "-i", path}
	if filter != "" {
		args = append(args, "-filter_complex", filter, "-map", label)
	} else {
		args = append(args, "-map", fmt.Sprintf("0:%d", streamIndex))
	}
	args = append(args,
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		outputPath,
	)
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg whisperx extract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func buildWhisperXArgs(cfg *config.Config, source, outputDir, language string) []string {
	cudaEnabled := cfg != nil && cfg.Subtitles.WhisperXCUDAEnabled

	args := make([]string, 0, 32)
	if cudaEnabled {
		args = append(args,
			"--index-url", whisperXCUDAIndexURL,
			"--extra-index-url", whisperXPypiIndexURL,
		)
	} else {
		args = append(args,
			"--index-url", whisperXPypiIndexURL,
		)
	}

	args = append(args,
		whisperXPackage,
		source,
		"--model", whisperXModel,
		"--align_model", whisperXAlignModel,
		"--batch_size", whisperXBatchSize,
		"--output_dir", outputDir,
		"--output_format", whisperXOutputFormat,
		"--segment_resolution", whisperXSegmentRes,
		"--chunk_size", whisperXChunkSize,
		"--vad_onset", whisperXVADOnset,
		"--vad_offset", whisperXVADOffset,
		"--beam_size", whisperXBeamSize,
		"--best_of", whisperXBestOf,
		"--temperature", whisperXTemperature,
		"--patience", whisperXPatience,
	)

	vadMethod := strings.ToLower(strings.TrimSpace(cfg.Subtitles.WhisperXVADMethod))
	if vadMethod != whisperXVADMethodPyannote {
		vadMethod = whisperXVADMethodSilero
	}
	args = append(args, "--vad_method", vadMethod)
	if vadMethod == whisperXVADMethodPyannote {
		token := strings.TrimSpace(cfg.Subtitles.WhisperXHuggingFace)
		if token != "" {
			args = append(args, "--hf_token", token)
		}
	}

	if lang := normalizeWhisperXLanguage(language); lang != "" {
		args = append(args, "--language", lang)
	}
	if cudaEnabled {
		args = append(args, "--device", whisperXCUDADevice)
	} else {
		args = append(args, "--device", whisperXCPUDevice, "--compute_type", whisperXCPUComputeType)
	}
	return args
}

func runWhisperX(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, whisperXCommand, args...) //nolint:gosec
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if os.Getenv("TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD") == "" {
		cmd.Env = append(os.Environ(), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("whisperx: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func whisperXSRTPath(outputDir, source string) string {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	if base == "" {
		base = "whisperx"
	}
	return filepath.Join(outputDir, base+".srt")
}

func readTranscript(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "-->") {
			continue
		}
		if isNumeric(line) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	return b.String(), nil
}

func isNumeric(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func classifyTranscript(text string) (string, transcriptScores) {
	tokens := tokenizeTranscript(text)
	if len(tokens) == 0 {
		return "ambiguous", transcriptScores{}
	}

	commentaryTokens := map[string]struct{}{
		"commentary": {}, "director": {}, "producer": {}, "writer": {}, "screenwriter": {}, "camera": {}, "shot": {},
		"scene": {}, "take": {}, "cut": {}, "editing": {}, "edited": {}, "edit": {}, "actor": {}, "actress": {},
		"cast": {}, "crew": {}, "set": {}, "location": {}, "studio": {}, "filmed": {}, "filming": {},
		"production": {}, "behind": {}, "track": {}, "dvd": {}, "blu": {}, "bluray": {}, "mix": {}, "sound": {},
		"score": {}, "music": {}, "script": {}, "costume": {}, "makeup": {}, "effect": {}, "effects": {},
		"visual": {}, "vfx": {}, "cgi": {}, "stunt": {}, "stunts": {}, "we": {}, "i": {}, "our": {}, "us": {},
		"me": {}, "my": {},
	}
	adTokens := map[string]struct{}{
		"he": {}, "she": {}, "they": {}, "his": {}, "her": {}, "their": {}, "man": {}, "woman": {}, "boy": {},
		"girl": {}, "walk": {}, "walks": {}, "enter": {}, "enters": {}, "look": {}, "looks": {}, "smile": {},
		"smiles": {}, "stand": {}, "stands": {}, "sit": {}, "sits": {}, "turn": {}, "turns": {}, "run": {},
		"runs": {}, "open": {}, "opens": {}, "close": {}, "closes": {}, "door": {}, "room": {}, "kitchen": {},
		"street": {}, "outside": {}, "inside": {}, "window": {}, "table": {}, "chair": {}, "bed": {}, "car": {},
		"house": {}, "building": {}, "toward": {}, "across": {}, "down": {}, "up": {}, "into": {}, "out": {},
		"away": {},
	}

	scores := transcriptScores{Tokens: len(tokens)}
	for _, token := range tokens {
		if _, ok := commentaryTokens[token]; ok {
			scores.Commentary++
		}
		if _, ok := adTokens[token]; ok {
			scores.AudioDescription++
		}
	}

	if scores.Commentary >= 3 && scores.Commentary >= scores.AudioDescription+2 {
		return "commentary", scores
	}
	if scores.AudioDescription >= 3 && scores.AudioDescription >= scores.Commentary+2 {
		return "audio_description", scores
	}
	return "ambiguous", scores
}

func tokenizeTranscript(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r < 'a' || r > 'z'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		if len(field) > 3 && strings.HasSuffix(field, "s") && !strings.HasSuffix(field, "ss") {
			field = strings.TrimSuffix(field, "s")
		}
		out = append(out, field)
	}
	return out
}

func normalizeWhisperXLanguage(language string) string {
	lang := strings.TrimSpace(strings.ToLower(language))
	if len(lang) == 2 {
		return lang
	}
	if len(lang) == 3 {
		switch lang {
		case "eng":
			return "en"
		case "spa":
			return "es"
		case "fra":
			return "fr"
		case "ger", "deu":
			return "de"
		case "ita":
			return "it"
		case "por":
			return "pt"
		case "dut", "nld":
			return "nl"
		case "rus":
			return "ru"
		case "jpn":
			return "ja"
		case "kor":
			return "ko"
		}
	}
	return ""
}
