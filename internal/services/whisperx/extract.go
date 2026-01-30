package whisperx

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// buildFFmpegExtractArgs builds the ffmpeg command arguments for audio extraction.
// If startSec < 0, extracts the full audio. Otherwise extracts from startSec for durationSec.
func buildFFmpegExtractArgs(source string, audioIndex int, startSec, durationSec int, dest string) []string {
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
	}
	if startSec >= 0 && durationSec > 0 {
		args = append(args,
			"-ss", fmt.Sprintf("%d", startSec),
			"-t", fmt.Sprintf("%d", durationSec),
		)
	}
	args = append(args,
		"-i", source,
		"-map", fmt.Sprintf("0:%d", audioIndex),
		"-vn",
		"-sn",
		"-dn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		dest,
	)
	return args
}

// ExtractFullAudio extracts the entire audio stream from a source file.
// The output is a mono 16kHz WAV file suitable for WhisperX.
func ExtractFullAudio(ctx context.Context, ffmpegBinary, source string, audioIndex int, dest string) error {
	if audioIndex < 0 {
		return fmt.Errorf("extract audio: invalid audio track index %d", audioIndex)
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
		dest,
	}
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg extract: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// ExtractSegment extracts a time-range segment of audio from a source file.
// startSec is the start time in seconds, durationSec is the segment length.
// The output is a mono 16kHz WAV file suitable for WhisperX.
func ExtractSegment(ctx context.Context, ffmpegBinary, source string, audioIndex int, startSec, durationSec int, dest string) error {
	if audioIndex < 0 {
		return fmt.Errorf("extract segment: invalid audio track index %d", audioIndex)
	}
	if durationSec <= 0 {
		return fmt.Errorf("extract segment: invalid duration %d", durationSec)
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-ss", fmt.Sprintf("%d", startSec),
		"-t", fmt.Sprintf("%d", durationSec),
		"-i", source,
		"-map", fmt.Sprintf("0:%d", audioIndex),
		"-vn",
		"-sn",
		"-dn",
		"-ac", "1",
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		dest,
	}
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg extract segment: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
