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

	"spindle/internal/commentaryid"
	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
)

func refineCommentaryTracks(ctx context.Context, cfg *config.Config, store *queue.Store, detector *commentaryid.Detector, item *queue.Item, sourcePath, stagingRoot, label string, episodeIndex, episodeCount int, logger *slog.Logger) (string, error) {
	if cfg == nil || detector == nil || !cfg.CommentaryDetection.Enabled {
		return sourcePath, nil
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return sourcePath, nil
	}

	decorate := func(step string) string {
		step = strings.TrimSpace(step)
		if step == "" {
			return ""
		}
		lbl := strings.TrimSpace(label)
		if lbl != "" && episodeIndex > 0 && episodeCount > 0 {
			return fmt.Sprintf("%s (%d/%d) — %s", lbl, episodeIndex, episodeCount, step)
		}
		if lbl != "" {
			return fmt.Sprintf("%s — %s", lbl, step)
		}
		return step
	}
	ffprobeBinary := cfg.FFprobeBinary()
	probe, err := encodeProbe(ctx, ffprobeBinary, sourcePath)
	if err != nil {
		return sourcePath, services.Wrap(services.ErrExternalTool, "encoding", "ffprobe commentary detection", "Failed to inspect source file audio streams", err)
	}
	if probe.AudioStreamCount() <= 1 {
		return sourcePath, nil
	}

	baseDir := strings.TrimSpace(stagingRoot)
	if baseDir == "" {
		baseDir = filepath.Dir(sourcePath)
	}
	workDir := filepath.Join(baseDir, "commentary")

	workingSource := sourcePath
	if isPathWithin(strings.TrimSpace(cfg.RipCache.Dir), sourcePath) {
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return sourcePath, services.Wrap(services.ErrConfiguration, "encoding", "commentary working copy", "Failed to create commentary working directory", err)
		}
		copyPath := filepath.Join(workDir, "source-"+filepath.Base(sourcePath))
		if err := copyFile(sourcePath, copyPath); err != nil {
			return sourcePath, services.Wrap(services.ErrTransient, "encoding", "commentary working copy", "Failed to copy rip cache source for commentary detection", err)
		}
		if logger != nil {
			logger.Info("created commentary working copy from rip cache",
				logging.String("source", sourcePath),
				logging.String("working_copy", copyPath),
			)
		}
		workingSource = copyPath
	}

	if item != nil {
		item.ProgressMessage = decorate("Commentary scan (WhisperX)")
		if store != nil {
			_ = store.UpdateProgress(ctx, item)
		}
	}

	ref, err := detector.Refine(ctx, workingSource, workDir)
	if err != nil {
		return workingSource, err
	}
	detector.DebugLog(ref)
	if ref.PrimaryIndex < 0 || len(ref.KeepIndices) == 0 {
		if item != nil {
			item.ProgressMessage = decorate("Commentary scan complete")
			if store != nil {
				_ = store.UpdateProgress(ctx, item)
			}
		}
		return workingSource, nil
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
		if item != nil {
			item.ProgressMessage = decorate("Commentary scan complete (no changes)")
			if store != nil {
				_ = store.UpdateProgress(ctx, item)
			}
		}
		return workingSource, nil
	}

	tmpPath := deriveTempCommentaryPath(workingSource)
	if item != nil {
		item.ProgressMessage = decorate("Commentary remux (ffmpeg)")
		if store != nil {
			_ = store.UpdateProgress(ctx, item)
		}
	}
	if err := remuxKeepAudioIndices(ctx, "ffmpeg", workingSource, tmpPath, ref.KeepIndices); err != nil {
		if logger != nil {
			logger.Warn("commentary remux failed; keeping original audio streams", logging.Error(err))
		}
		if item != nil {
			item.ProgressMessage = decorate("Commentary remux failed; keeping original audio")
			if store != nil {
				_ = store.UpdateProgress(ctx, item)
			}
		}
		_ = os.Remove(tmpPath)
		return workingSource, nil
	}
	if err := os.Rename(tmpPath, workingSource); err != nil {
		_ = os.Remove(tmpPath)
		return workingSource, services.Wrap(services.ErrTransient, "encoding", "finalize commentary remux", "Failed to finalize remuxed source file", err)
	}

	if logger != nil {
		fields := []any{
			logging.String("source", workingSource),
			logging.Int("kept_audio_streams", len(ref.KeepIndices)),
			logging.Any("kept_audio_indices", ref.KeepIndices),
		}
		if len(ref.Dropped) > 0 {
			fields = append(fields,
				logging.Int("dropped_audio_streams", len(ref.Dropped)),
				logging.Any("dropped_decisions", ref.Dropped),
			)
		}
		logger.Info("refined source audio tracks before encoding", fields...)
	}
	if item != nil {
		dropped := 0
		if len(ref.Dropped) > 0 {
			dropped = len(ref.Dropped)
		}
		item.ProgressMessage = decorate(fmt.Sprintf("Commentary remux complete (kept %d, dropped %d)", len(ref.KeepIndices), dropped))
		if store != nil {
			_ = store.UpdateProgress(ctx, item)
		}
	}

	return workingSource, nil
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

func isPathWithin(baseDir, targetPath string) bool {
	baseDir = strings.TrimSpace(baseDir)
	targetPath = strings.TrimSpace(targetPath)
	if baseDir == "" || targetPath == "" {
		return false
	}
	baseDir = filepath.Clean(baseDir)
	targetPath = filepath.Clean(targetPath)
	rel, err := filepath.Rel(baseDir, targetPath)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}
