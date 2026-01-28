package subtitles

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/services"
)

func (s *Service) buildWhisperXArgs(source, outputDir, language string) []string {
	cudaEnabled := s != nil && s.config != nil && s.config.Subtitles.WhisperXCUDAEnabled

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
		"whisperx",
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

	vadMethod := s.activeVADMethod()
	args = append(args, "--vad_method", vadMethod)
	if vadMethod == whisperXVADMethodPyannote && s != nil {
		token := strings.TrimSpace(s.hfToken)
		if token != "" {
			args = append(args, "--hf_token", token)
		}
	}

	if lang := normalizeWhisperLanguage(language); lang != "" {
		args = append(args, "--language", lang)
	}
	if cudaEnabled {
		args = append(args, "--device", whisperXCUDADevice)
	} else {
		args = append(args, "--device", whisperXCPUDevice, "--compute_type", whisperXCPUComputeType)
	}
	// Ensure highlight_words is disabled (default false) without passing CLI flag.
	return args
}

func (s *Service) extractPrimaryAudio(ctx context.Context, source string, audioIndex int, destination string) error {
	if audioIndex < 0 {
		return services.Wrap(services.ErrValidation, "subtitles", "extract audio", "Invalid audio track index", nil)
	}
	start := time.Now()
	if s.logger != nil {
		s.logger.Debug("extracting primary audio",
			logging.String("source_file", source),
			logging.Int("audio_index", audioIndex),
			logging.String("destination", destination),
		)
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", source,
		"-map", fmt.Sprintf("0:%d", audioIndex),
		"-vn",
		"-sn",
		"-dn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		destination,
	}
	if err := s.run(ctx, ffmpegCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "extract audio", "Failed to extract primary audio track with ffmpeg", err)
	}
	if s.logger != nil {
		attrs := []logging.Attr{
			logging.String("destination", destination),
			logging.Duration("elapsed", time.Since(start)),
		}
		if info, err := os.Stat(destination); err == nil {
			attrs = append(attrs, logging.Float64("size_mb", float64(info.Size())/1_048_576))
		}
		s.logger.Debug("primary audio extracted", logging.Args(attrs...)...)
	}
	return nil
}

func (s *Service) formatWithStableTS(ctx context.Context, whisperJSON, outputPath, language string) error {
	if strings.TrimSpace(whisperJSON) == "" {
		return os.ErrNotExist
	}

	tmpPath := outputPath + ".tmp"
	defer os.Remove(tmpPath)

	args := []string{
		"--from", stableTSPackage,
		"python", "-c", stableTSFormatterScript,
		whisperJSON,
		tmpPath,
	}
	if trimmed := strings.TrimSpace(language); trimmed != "" {
		args = append(args, "--language", trimmed)
	}
	if err := s.run(ctx, stableTSCommand, args...); err != nil {
		return services.Wrap(services.ErrExternalTool, "subtitles", "stable_ts_formatter", "StableTS formatter failed", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return services.Wrap(services.ErrTransient, "subtitles", "finalize stablets formatter", "Failed to finalize formatted subtitles", err)
	}
	return nil
}

func (s *Service) reshapeSubtitles(ctx context.Context, whisperSRT, whisperJSON, outputPath, language string, _ float64) error {
	if err := s.formatWithStableTS(ctx, whisperJSON, outputPath, normalizeWhisperLanguage(language)); err != nil {
		if s.logger != nil {
			s.logger.Warn("stable-ts formatter failed, delivering raw whisper subtitles",
				logging.Error(err),
				logging.String(logging.FieldEventType, "stablets_formatter_failed"),
				logging.String(logging.FieldErrorHint, "check stable-ts installation and WhisperX outputs"),
			)
		}
		if strings.TrimSpace(whisperSRT) == "" {
			return err
		}
		data, readErr := os.ReadFile(whisperSRT)
		if readErr != nil {
			return services.Wrap(services.ErrTransient, "subtitles", "fallback copy", "Failed to read WhisperX subtitles after Stable-TS failure", readErr)
		}
		if writeErr := os.WriteFile(outputPath, data, 0o644); writeErr != nil {
			return services.Wrap(services.ErrTransient, "subtitles", "fallback copy", "Failed to write WhisperX subtitles after Stable-TS failure", writeErr)
		}
	}
	return nil
}

func (s *Service) defaultCommandRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	// Torch 2.6 changed torch.load default to weights_only=true, breaking WhisperX/pyannote.
	// Force legacy behavior so bundled WhisperX binaries can load checkpoints safely.
	if os.Getenv("TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD") == "" {
		cmd.Env = append(os.Environ(), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	}

	if err := cmd.Run(); err != nil {
		raw := strings.TrimSpace(stderr.String())
		detailPath := s.writeToolLog(name, args, raw)
		base := fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		return &services.ServiceError{
			Marker:     services.ErrExternalTool,
			Kind:       services.ErrorKindExternal,
			Operation:  "command",
			Message:    "External command failed",
			DetailPath: detailPath,
			Cause:      base,
		}
	}
	return nil
}

func (s *Service) writeToolLog(name string, args []string, stderr string) string {
	if s == nil || s.config == nil {
		return ""
	}
	logDir := strings.TrimSpace(s.config.Paths.LogDir)
	if logDir == "" {
		return ""
	}
	toolDir := filepath.Join(logDir, "tool")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to create tool log directory; tool stderr not captured",
				logging.Error(err),
				logging.String(logging.FieldEventType, "tool_log_dir_failed"),
				logging.String(logging.FieldErrorHint, "check log_dir permissions"),
			)
		}
		return ""
	}
	timestamp := time.Now().UTC().Format("20060102T150405.000Z")
	toolName := sanitizeToolName(name)
	if toolName == "" {
		toolName = "tool"
	}
	path := filepath.Join(toolDir, fmt.Sprintf("%s-%s.log", timestamp, toolName))

	command := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
	payload := strings.Builder{}
	payload.Grow(len(command) + len(stderr) + 64)
	payload.WriteString("command: ")
	payload.WriteString(command)
	payload.WriteByte('\n')
	payload.WriteString("stderr:\n")
	payload.WriteString(stderr)
	payload.WriteByte('\n')

	if err := os.WriteFile(path, []byte(payload.String()), 0o644); err != nil {
		if s.logger != nil {
			s.logger.Warn("failed to write tool log; stderr detail lost",
				logging.Error(err),
				logging.String(logging.FieldEventType, "tool_log_write_failed"),
				logging.String(logging.FieldErrorHint, "check log_dir permissions"),
			)
		}
		return ""
	}
	return path
}

func sanitizeToolName(value string) string {
	value = strings.TrimSpace(filepath.Base(value))
	if value == "" {
		return ""
	}
	value = strings.ToLower(value)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return strings.Trim(replacer.Replace(value), "-")
}
