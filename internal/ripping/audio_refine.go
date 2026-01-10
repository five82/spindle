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

// AudioRefinementResult captures audio selection outcomes for reporting.
type AudioRefinementResult struct {
	PrimaryAudioDescription string
	CommentaryCount         int
}

// RefineAudioTargets applies primary + commentary selection across a set of rip paths.
// Returns aggregated audio info from the first successfully processed path.
func RefineAudioTargets(ctx context.Context, cfg *config.Config, logger *slog.Logger, paths []string) (AudioRefinementResult, error) {
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
	var result AudioRefinementResult
	for i, path := range unique {
		info, err := refineAudioTracks(ctx, cfg, logger, path)
		if err != nil {
			return AudioRefinementResult{}, err
		}
		// Capture audio info from the first path (consistent across episodes)
		if i == 0 {
			result = info
		}
	}
	return result, nil
}

func refineAudioTracks(ctx context.Context, cfg *config.Config, logger *slog.Logger, path string) (AudioRefinementResult, error) {
	logger = logging.WithContext(ctx, logging.NewComponentLogger(logger, "audio-refiner"))
	if strings.TrimSpace(path) == "" {
		return AudioRefinementResult{}, fmt.Errorf("refine audio: empty path")
	}
	ffprobeBinary := "ffprobe"
	if cfg != nil {
		ffprobeBinary = deps.ResolveFFprobePath(cfg.FFprobeBinary())
	}
	probe, err := probeVideo(ctx, ffprobeBinary, path)
	if err != nil {
		return AudioRefinementResult{}, fmt.Errorf("inspect ripped audio: %w", err)
	}
	totalAudio := countAudioStreams(probe.Streams)
	if totalAudio <= 1 {
		if logger != nil {
			candidates, _ := summarizeAudioCandidates(probe.Streams)
			attrs := []logging.Attr{
				logging.String(logging.FieldDecisionType, "audio_selection"),
				logging.String("decision_result", "skipped"),
				logging.String("decision_reason", "single_audio_stream"),
				logging.String("decision_options", "select, skip"),
				logging.Int("candidate_count", len(candidates)),
			}
			for _, candidate := range candidates {
				attrs = append(attrs, logging.String(fmt.Sprintf("candidate_%d", candidate.Index), candidate.Value))
			}
			logger.Info("audio selection decision",
				logging.Args(attrs...)...,
			)
		}
		// Single audio stream - return info from that stream
		result := AudioRefinementResult{CommentaryCount: 0}
		if len(probe.Streams) > 0 {
			for _, s := range probe.Streams {
				if s.CodecType == "audio" {
					result.PrimaryAudioDescription = formatAudioDescription(s)
					break
				}
			}
		}
		return result, nil
	}
	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return AudioRefinementResult{}, fmt.Errorf("refine audio: primary selection missing")
	}
	if logger != nil {
		candidates, hasEnglish := summarizeAudioCandidates(probe.Streams)
		candidateByIndex := make(map[int]string, len(candidates))
		for _, candidate := range candidates {
			candidateByIndex[candidate.Index] = candidate.Value
		}
		reason := "english_preferred"
		if !hasEnglish {
			reason = "fallback_first_audio"
		}
		attrs := []logging.Attr{
			logging.String(logging.FieldDecisionType, "audio_selection"),
			logging.String("decision_result", "selected"),
			logging.String("decision_reason", reason),
			logging.String("decision_options", "select, skip"),
			logging.String("decision_selected", selection.PrimaryLabel()),
			logging.Int("candidate_count", len(candidates)),
		}
		for _, candidate := range candidates {
			attrs = append(attrs, logging.String(fmt.Sprintf("candidate_%d", candidate.Index), candidate.Value))
		}
		if selectedValue, ok := candidateByIndex[selection.PrimaryIndex]; ok {
			attrs = append(attrs, logging.String(fmt.Sprintf("selected_%d", selection.PrimaryIndex), selectedValue+" | primary"))
		}
		logger.Info("audio selection decision",
			logging.Args(attrs...)...,
		)
	}

	commentaryIndices := []int{}
	if cfg != nil && cfg.CommentaryDetection.Enabled {
		result, detectErr := commentary.Detect(ctx, cfg, path, probe, selection.PrimaryIndex, logger)
		if detectErr != nil {
			logger.Warn("commentary detection failed; continuing without commentary tracks",
				logging.Error(detectErr),
				logging.String(logging.FieldEventType, "commentary_detection_failed"),
				logging.String(logging.FieldErrorHint, "check ffmpeg/fpcalc availability or disable commentary_detection"),
			)
		} else {
			commentaryIndices = append(commentaryIndices, result.Indices...)
		}
	}
	commentaryTitles := collectCommentaryTitles(probe.Streams, commentaryIndices)

	keep := buildKeepOrder(selection.PrimaryIndex, commentaryIndices)
	if len(keep) == 0 {
		return AudioRefinementResult{}, fmt.Errorf("refine audio: selection produced no audio streams")
	}
	selection.KeepIndices = keep
	selection.RemovedIndices = removedIndices(probe.Streams, keep)

	// Build result with audio info
	result := AudioRefinementResult{
		PrimaryAudioDescription: selection.PrimaryLabel(),
		CommentaryCount:         len(commentaryIndices),
	}
	// Get full audio description from the primary stream
	for _, s := range probe.Streams {
		if s.Index == selection.PrimaryIndex {
			result.PrimaryAudioDescription = formatAudioDescription(s)
			break
		}
	}

	needsRemux := selection.Changed(totalAudio) || needsDispositionFix(probe.Streams, keep) || needsCommentaryDispositionFix(probe.Streams, commentaryTitles)
	if !needsRemux {
		return result, nil
	}

	tmpPath := deriveTempAudioPath(path)
	ffmpegBinary := "ffmpeg"
	if cfg != nil {
		ffmpegBinary = deps.ResolveFFmpegPath(cfg.DraptoBinary())
	}
	if err := remuxAudioSelection(ctx, ffmpegBinary, path, tmpPath, selection, commentaryTitles); err != nil {
		return AudioRefinementResult{}, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmpPath)
		return AudioRefinementResult{}, fmt.Errorf("refine audio: remove original rip: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return AudioRefinementResult{}, fmt.Errorf("refine audio: finalize remux: %w", err)
	}

	// Validate remuxed audio streams match expectations
	if err := validateRemuxedAudio(ctx, ffprobeBinary, path, selection.KeepIndices, logger); err != nil {
		return AudioRefinementResult{}, fmt.Errorf("audio validation failed: %w", err)
	}

	fields := []logging.Attr{
		logging.String("primary_audio", selection.PrimaryLabel()),
		logging.Int("kept_audio_streams", len(selection.KeepIndices)),
		logging.Int("commentary_count", len(commentaryIndices)),
		logging.Int("removed_count", len(selection.RemovedIndices)),
	}
	if len(commentaryIndices) > 0 {
		sort.Ints(commentaryIndices)
		for _, idx := range commentaryIndices {
			fields = append(fields, logging.String(fmt.Sprintf("commentary_%d", idx), fmt.Sprintf("%d", idx)))
		}
	}
	if len(selection.RemovedIndices) > 0 {
		removed := append([]int(nil), selection.RemovedIndices...)
		sort.Ints(removed)
		for _, idx := range removed {
			fields = append(fields, logging.String(fmt.Sprintf("removed_%d", idx), fmt.Sprintf("%d", idx)))
		}
	}
	logger.Info("refined ripped audio tracks", logging.Args(fields...)...)
	return result, nil
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
	var count int
	for _, s := range streams {
		if strings.EqualFold(s.CodecType, "audio") {
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
	var removed []int
	for _, s := range streams {
		if !strings.EqualFold(s.CodecType, "audio") {
			continue
		}
		if _, ok := kept[s.Index]; !ok {
			removed = append(removed, s.Index)
		}
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

type audioCandidate struct {
	Index int
	Value string
}

func summarizeAudioCandidates(streams []ffprobe.Stream) ([]audioCandidate, bool) {
	var candidates []audioCandidate
	var hasEnglish bool
	for _, s := range streams {
		if !strings.EqualFold(s.CodecType, "audio") {
			continue
		}
		value, isEnglish := formatAudioCandidateValue(s)
		hasEnglish = hasEnglish || isEnglish
		candidates = append(candidates, audioCandidate{Index: s.Index, Value: value})
	}
	return candidates, hasEnglish
}

func formatAudioCandidateValue(stream ffprobe.Stream) (string, bool) {
	lang := audioLanguage(stream.Tags)
	isEnglish := strings.HasPrefix(lang, "en")
	if lang == "" {
		lang = "und"
	}
	codec := strings.TrimSpace(stream.CodecLong)
	if codec == "" {
		codec = strings.TrimSpace(stream.CodecName)
	}
	if codec == "" {
		codec = "unknown"
	}
	channelLabel := "unknown"
	if stream.Channels > 0 {
		channelLabel = fmt.Sprintf("%dch", stream.Channels)
	}
	parts := []string{lang, codec, channelLabel}
	if stream.Disposition != nil && stream.Disposition["default"] == 1 {
		parts = append(parts, "default")
	}
	title := strings.TrimSpace(audioTitle(stream.Tags))
	if title != "" {
		parts = append(parts, title)
	}
	return strings.Join(parts, " | "), isEnglish
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
		if idx >= 0 {
			indices[idx] = struct{}{}
		}
	}
	titles := make(map[int]string, len(indices))
	for _, s := range streams {
		if !strings.EqualFold(s.CodecType, "audio") {
			continue
		}
		if _, ok := indices[s.Index]; ok {
			titles[s.Index] = audioTitle(s.Tags)
		}
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

// formatAudioDescription builds a human-readable audio description from stream metadata.
// Example output: "TrueHD 7.1 Atmos" or "DTS-HD MA 5.1"
func formatAudioDescription(s ffprobe.Stream) string {
	if s.CodecType != "audio" {
		return ""
	}

	parts := []string{}

	// Codec name (normalize common ones)
	codec := strings.ToUpper(s.CodecName)
	switch strings.ToLower(s.CodecName) {
	case "truehd":
		codec = "TrueHD"
	case "dts":
		// Check profile for HD variants
		profile := strings.ToLower(s.Profile)
		if strings.Contains(profile, "ma") {
			codec = "DTS-HD MA"
		} else if strings.Contains(profile, "hd") {
			codec = "DTS-HD"
		} else {
			codec = "DTS"
		}
	case "eac3":
		codec = "E-AC3"
	case "ac3":
		codec = "AC3"
	case "flac":
		codec = "FLAC"
	case "pcm_s16le", "pcm_s24le", "pcm_s32le":
		codec = "PCM"
	case "aac":
		codec = "AAC"
	case "opus":
		codec = "Opus"
	}
	parts = append(parts, codec)

	// Channel layout (e.g., "7.1", "5.1", "stereo")
	layout := strings.TrimSpace(s.ChannelLayout)
	if layout == "" && s.Channels > 0 {
		switch s.Channels {
		case 1:
			layout = "mono"
		case 2:
			layout = "stereo"
		case 6:
			layout = "5.1"
		case 8:
			layout = "7.1"
		default:
			layout = fmt.Sprintf("%dch", s.Channels)
		}
	}
	if layout != "" {
		// Normalize common layouts
		switch strings.ToLower(layout) {
		case "5.1(side)", "5.1":
			layout = "5.1"
		case "7.1(wide)", "7.1":
			layout = "7.1"
		}
		parts = append(parts, layout)
	}

	// Check for Atmos (usually in profile or side data)
	if strings.Contains(strings.ToLower(s.Profile), "atmos") {
		parts = append(parts, "Atmos")
	}

	return strings.Join(parts, " ")
}

// validateRemuxedAudio probes the remuxed file and validates that the expected
// audio streams are present. Returns an error if validation fails.
func validateRemuxedAudio(ctx context.Context, ffprobeBinary, path string, expectedIndices []int, logger *slog.Logger) error {
	if len(expectedIndices) == 0 {
		return nil // Nothing to validate
	}

	probe, err := probeVideo(ctx, ffprobeBinary, path)
	if err != nil {
		return fmt.Errorf("probe remuxed file: %w", err)
	}

	actualCount := countAudioStreams(probe.Streams)
	expectedCount := len(expectedIndices)

	if actualCount != expectedCount {
		if logger != nil {
			logger.Error("audio stream count mismatch after remux",
				logging.Int("expected_count", expectedCount),
				logging.Int("actual_count", actualCount),
				logging.String("path", path),
				logging.String(logging.FieldEventType, "audio_validation_failed"),
			)
		}
		return fmt.Errorf("audio stream count mismatch: expected %d, got %d", expectedCount, actualCount)
	}

	// Validate primary audio stream exists (first in keep list)
	if actualCount == 0 {
		return fmt.Errorf("no audio streams in remuxed file")
	}

	if logger != nil {
		logger.Debug("audio validation passed",
			logging.Int("audio_stream_count", actualCount),
			logging.String("path", path),
		)
	}

	return nil
}
