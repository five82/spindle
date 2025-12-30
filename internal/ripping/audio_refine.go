package ripping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/commentary"
	"spindle/internal/media/ffprobe"
)

// RefineAudioTargets applies primary + commentary selection across a set of rip paths.
func RefineAudioTargets(ctx context.Context, cfg *config.Config, logger *slog.Logger, paths []string) error {
	unique := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		clean := strings.TrimSpace(path)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		unique = append(unique, clean)
	}
	for _, path := range unique {
		if err := refineAudioTracks(ctx, cfg, logger, path); err != nil {
			return err
		}
	}
	return nil
}

func refineAudioTracks(ctx context.Context, cfg *config.Config, logger *slog.Logger, path string) error {
	logger = logging.WithContext(ctx, logging.NewComponentLogger(logger, "audio-refiner"))
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("refine audio: empty path")
	}
	ffprobeBinary := "ffprobe"
	if cfg != nil {
		ffprobeBinary = deps.ResolveFFprobePath(cfg.FFprobeBinary())
	}
	probe, err := probeVideo(ctx, ffprobeBinary, path)
	if err != nil {
		return fmt.Errorf("inspect ripped audio: %w", err)
	}
	totalAudio := countAudioStreams(probe.Streams)
	if totalAudio <= 1 {
		if logger != nil {
			candidates, _ := summarizeAudioCandidates(probe.Streams)
			logger.Info("audio selection decision",
				logging.String(logging.FieldDecisionType, "audio_selection"),
				logging.String("decision_result", "skipped"),
				logging.String("decision_reason", "single_audio_stream"),
				logging.String("decision_options", "select, skip"),
				logging.Any("decision_candidates", candidates),
			)
		}
		return nil
	}
	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return fmt.Errorf("refine audio: primary selection missing")
	}
	if logger != nil {
		candidates, hasEnglish := summarizeAudioCandidates(probe.Streams)
		reason := "english_preferred"
		if !hasEnglish {
			reason = "fallback_first_audio"
		}
		logger.Info("audio selection decision",
			logging.String(logging.FieldDecisionType, "audio_selection"),
			logging.String("decision_result", "selected"),
			logging.String("decision_reason", reason),
			logging.String("decision_options", "select, skip"),
			logging.String("decision_selected", selection.PrimaryLabel()),
			logging.Any("decision_candidates", candidates),
		)
	}

	commentaryIndices := []int{}
	if cfg != nil && cfg.CommentaryDetection.Enabled {
		result, detectErr := commentary.Detect(ctx, cfg, path, probe, selection.PrimaryIndex, logger)
		if detectErr != nil {
			logger.Warn("commentary detection failed", logging.Error(detectErr))
		} else {
			commentaryIndices = append(commentaryIndices, result.Indices...)
		}
	}
	commentaryTitles := collectCommentaryTitles(probe.Streams, commentaryIndices)

	keep := buildKeepOrder(selection.PrimaryIndex, commentaryIndices)
	if len(keep) == 0 {
		return fmt.Errorf("refine audio: selection produced no audio streams")
	}
	selection.KeepIndices = keep
	selection.RemovedIndices = removedIndices(probe.Streams, keep)

	needsRemux := selection.Changed(totalAudio) || needsDispositionFix(probe.Streams, keep) || needsCommentaryDispositionFix(probe.Streams, commentaryTitles)
	if !needsRemux {
		return nil
	}

	tmpPath := deriveTempAudioPath(path)
	ffmpegBinary := "ffmpeg"
	if cfg != nil {
		ffmpegBinary = deps.ResolveFFmpegPath(cfg.DraptoBinary())
	}
	if err := remuxAudioSelection(ctx, ffmpegBinary, path, tmpPath, selection, commentaryTitles); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("refine audio: remove original rip: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("refine audio: finalize remux: %w", err)
	}

	fields := []any{
		logging.String("primary_audio", selection.PrimaryLabel()),
		logging.Int("kept_audio_streams", len(selection.KeepIndices)),
	}
	if len(commentaryIndices) > 0 {
		sort.Ints(commentaryIndices)
		fields = append(fields, logging.Any("commentary_indices", commentaryIndices))
	}
	if len(selection.RemovedIndices) > 0 {
		fields = append(fields, logging.Any("removed_audio_indices", selection.RemovedIndices))
	}
	logger.Info("refined ripped audio tracks", fields...)
	return nil
}

func remuxAudioSelection(ctx context.Context, ffmpegBinary, src, dst string, selection audio.Selection, commentaryTitles map[int]string) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return fmt.Errorf("remux audio: invalid path")
	}
	if strings.TrimSpace(ffmpegBinary) == "" {
		ffmpegBinary = "ffmpeg"
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", src, "-map", "0:v?", "-map", "0:s?", "-map", "0:d?", "-map", "0:t?"}
	for _, idx := range selection.KeepIndices {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}
	args = append(args, "-c", "copy")
	if len(selection.KeepIndices) > 0 {
		for outIdx, srcIdx := range selection.KeepIndices {
			if outIdx == 0 {
				args = append(args, "-disposition:a:0", "default")
				continue
			}
			title, isCommentary := commentaryTitles[srcIdx]
			if isCommentary {
				args = append(args, "-disposition:a:"+strconv.Itoa(outIdx), "comment")
				label := commentaryLabel(title)
				if label != "" {
					args = append(args, "-metadata:s:a:"+strconv.Itoa(outIdx), "title="+label)
				}
				continue
			}
			args = append(args, "-disposition:a:"+strconv.Itoa(outIdx), "none")
		}
	}
	if format := outputFormatForPath(dst); format != "" {
		args = append(args, "-f", format)
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

func countAudioStreams(streams []ffprobe.Stream) int {
	count := 0
	for _, stream := range streams {
		if strings.EqualFold(stream.CodecType, "audio") {
			count++
		}
	}
	return count
}

func buildKeepOrder(primaryIndex int, commentaryIndices []int) []int {
	if primaryIndex < 0 {
		return nil
	}
	keep := []int{primaryIndex}
	seen := map[int]struct{}{primaryIndex: {}}
	for _, idx := range commentaryIndices {
		if idx < 0 {
			continue
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		keep = append(keep, idx)
	}
	return keep
}

func removedIndices(streams []ffprobe.Stream, keep []int) []int {
	kept := make(map[int]struct{}, len(keep))
	for _, idx := range keep {
		kept[idx] = struct{}{}
	}
	removed := make([]int, 0)
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		if _, ok := kept[stream.Index]; ok {
			continue
		}
		removed = append(removed, stream.Index)
	}
	sort.Ints(removed)
	return removed
}

func needsDispositionFix(streams []ffprobe.Stream, keep []int) bool {
	if len(keep) == 0 {
		return false
	}
	primaryIndex := keep[0]
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		if stream.Index == primaryIndex {
			continue
		}
		if stream.Disposition != nil && stream.Disposition["default"] == 1 {
			return true
		}
	}
	return false
}

func needsCommentaryDispositionFix(streams []ffprobe.Stream, commentaryTitles map[int]string) bool {
	if len(commentaryTitles) == 0 {
		return false
	}
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		title, ok := commentaryTitles[stream.Index]
		if !ok {
			continue
		}
		if stream.Disposition == nil || stream.Disposition["comment"] != 1 {
			return true
		}
		if title == "" || !strings.Contains(strings.ToLower(title), "commentary") {
			return true
		}
	}
	return false
}

func summarizeAudioCandidates(streams []ffprobe.Stream) ([]string, bool) {
	candidates := make([]string, 0)
	hasEnglish := false
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		lang := audioLanguage(stream.Tags)
		if strings.HasPrefix(lang, "en") {
			hasEnglish = true
		}
		langLabel := lang
		if langLabel == "" {
			langLabel = "und"
		}
		codec := strings.TrimSpace(stream.CodecLong)
		if codec == "" {
			codec = strings.TrimSpace(stream.CodecName)
		}
		if codec == "" {
			codec = "unknown"
		}
		title := audioTitle(stream.Tags)
		titleLabel := ""
		if title != "" {
			titleLabel = " | " + title
		}
		defaultLabel := ""
		if stream.Disposition != nil && stream.Disposition["default"] == 1 {
			defaultLabel = " default"
		}
		channelLabel := "unknown"
		if stream.Channels > 0 {
			channelLabel = fmt.Sprintf("%dch", stream.Channels)
		}
		candidates = append(candidates, fmt.Sprintf("#%d (%s): %s %s%s%s",
			stream.Index,
			langLabel,
			codec,
			channelLabel,
			defaultLabel,
			titleLabel,
		))
	}
	return candidates, hasEnglish
}

func audioLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"language", "LANGUAGE", "Language", "language_ietf", "LANG"} {
		if value, ok := tags[key]; ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func audioTitle(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"title", "TITLE", "handler_name", "HANDLER_NAME"} {
		if value, ok := tags[key]; ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func collectCommentaryTitles(streams []ffprobe.Stream, commentaryIndices []int) map[int]string {
	if len(commentaryIndices) == 0 {
		return nil
	}
	indices := make(map[int]struct{}, len(commentaryIndices))
	for _, idx := range commentaryIndices {
		if idx < 0 {
			continue
		}
		indices[idx] = struct{}{}
	}
	titles := make(map[int]string, len(indices))
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		if _, ok := indices[stream.Index]; !ok {
			continue
		}
		titles[stream.Index] = audioTitle(stream.Tags)
	}
	return titles
}

func commentaryLabel(original string) string {
	title := strings.TrimSpace(original)
	if title == "" {
		return "Commentary"
	}
	lower := strings.ToLower(title)
	if strings.Contains(lower, "commentary") {
		return title
	}
	return title + " (Commentary)"
}

func deriveTempAudioPath(path string) string {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return path + ".spindle-audio"
	}
	ext := filepath.Ext(clean)
	base := strings.TrimSuffix(clean, ext)
	if ext == "" {
		ext = ".mkv"
	}
	return fmt.Sprintf("%s.spindle-audio%s", base, ext)
}

func outputFormatForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mk3d":
		return "matroska"
	case ".mp4", ".m4v":
		return "mp4"
	case ".mov":
		return "mov"
	case ".ts", ".m2ts":
		return "mpegts"
	case ".mka":
		return "matroska"
	default:
		return ""
	}
}
