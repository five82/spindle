package encoding

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
)

func (e *Encoder) refineCommentaryTracks(ctx context.Context, item *queue.Item, sourcePath, stagingRoot string, logger *slog.Logger) error {
	if e == nil || e.cfg == nil || e.commentary == nil || !e.cfg.CommentaryDetectionEnabled {
		return nil
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil
	}
	ffprobeBinary := e.cfg.FFprobeBinary()
	probe, err := encodeProbe(ctx, ffprobeBinary, sourcePath)
	if err != nil {
		return services.Wrap(services.ErrExternalTool, "encoding", "ffprobe commentary detection", "Failed to inspect source file audio streams", err)
	}
	if probe.AudioStreamCount() <= 1 {
		return nil
	}

	workDir := strings.TrimSpace(stagingRoot)
	if workDir == "" {
		workDir = filepath.Dir(sourcePath)
	}
	workDir = filepath.Join(workDir, "commentary")

	if item != nil {
		item.ProgressMessage = "Detecting commentary tracks"
		if e.store != nil {
			_ = e.store.UpdateProgress(ctx, item)
		}
	}

	ref, err := e.commentary.Refine(ctx, sourcePath, workDir)
	if err != nil {
		return err
	}
	if ref.PrimaryIndex < 0 || len(ref.KeepIndices) == 0 {
		return nil
	}

	keepSet := make(map[int]struct{}, len(ref.KeepIndices))
	for _, idx := range ref.KeepIndices {
		keepSet[idx] = struct{}{}
	}
	needsRemux := false
	for _, stream := range probe.Streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		if _, ok := keepSet[stream.Index]; ok {
			continue
		}
		needsRemux = true
		break
	}
	if !needsRemux {
		return nil
	}

	tmpPath := deriveTempCommentaryPath(sourcePath)
	if err := remuxKeepAudioIndices(ctx, "ffmpeg", sourcePath, tmpPath, ref.KeepIndices); err != nil {
		if logger != nil {
			logger.Warn("commentary remux failed; keeping original audio streams", logging.Error(err))
		}
		_ = os.Remove(tmpPath)
		return nil
	}
	if err := os.Rename(tmpPath, sourcePath); err != nil {
		_ = os.Remove(tmpPath)
		return services.Wrap(services.ErrTransient, "encoding", "finalize commentary remux", "Failed to finalize remuxed source file", err)
	}

	if logger != nil {
		fields := []any{
			logging.String("source", sourcePath),
			logging.Int("kept_audio_streams", len(ref.KeepIndices)),
		}
		if len(ref.Dropped) > 0 {
			fields = append(fields, logging.Int("dropped_audio_streams", len(ref.Dropped)))
		}
		logger.Info("refined source audio tracks before encoding", fields...)
	}

	return nil
}

func deriveTempCommentaryPath(src string) string {
	ext := filepath.Ext(src)
	base := strings.TrimSuffix(src, ext)
	if ext == "" {
		ext = ".mkv"
	}
	return base + ".commentary-tmp" + ext
}

func remuxKeepAudioIndices(ctx context.Context, ffmpegBinary, src, dst string, keepIndices []int) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return services.Wrap(services.ErrValidation, "encoding", "remux audio", "Invalid remux path", nil)
	}
	if len(keepIndices) == 0 {
		return services.Wrap(services.ErrValidation, "encoding", "remux audio", "No audio streams selected for remux", nil)
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", src, "-map", "0:v?", "-map", "0:s?", "-map", "0:d?", "-map", "0:t?"}
	for _, idx := range keepIndices {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}
	args = append(args, "-c", "copy")
	args = append(args, "-disposition:a:0", "default")
	for i := 1; i < len(keepIndices); i++ {
		args = append(args, "-disposition:a:"+strconv.Itoa(i), "none")
	}
	args = append(args, dst)
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg remux: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
