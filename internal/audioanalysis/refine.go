package audioanalysis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	langpkg "spindle/internal/language"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
)

// AudioRefinementResult captures audio selection outcomes for reporting.
type AudioRefinementResult struct {
	PrimaryAudioDescription string
	// KeptIndices contains the original stream indices that were preserved,
	// in the order they appear in the output file. Used to map old indices
	// to new output positions after remux.
	KeptIndices []int
}

// RefineAudioTargets applies primary audio selection across a set of rip paths.
// additionalKeep specifies extra stream indices to preserve (e.g., commentary tracks).
// Returns aggregated audio info from the first successfully processed path.
func RefineAudioTargets(ctx context.Context, cfg *config.Config, logger *slog.Logger, paths []string, additionalKeep []int) (AudioRefinementResult, error) {
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
		info, err := refineAudioTracks(ctx, cfg, logger, path, additionalKeep)
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

func refineAudioTracks(ctx context.Context, cfg *config.Config, logger *slog.Logger, path string, additionalKeep []int) (AudioRefinementResult, error) {
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
	totalAudio := probe.AudioStreamCount()
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
		// Collect the single audio stream index for KeptIndices
		var keptIndices []int
		for _, stream := range probe.Streams {
			if stream.CodecType == "audio" {
				keptIndices = append(keptIndices, stream.Index)
				break
			}
		}
		return AudioRefinementResult{
			PrimaryAudioDescription: findAudioDescription(probe.Streams, -1),
			KeptIndices:             keptIndices,
		}, nil
	}
	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return AudioRefinementResult{}, fmt.Errorf("refine audio: primary selection missing")
	}

	// Merge additional indices to keep (e.g., commentary tracks detected before refinement)
	if len(additionalKeep) > 0 {
		keepSet := make(map[int]struct{}, len(selection.KeepIndices)+len(additionalKeep))
		for _, idx := range selection.KeepIndices {
			keepSet[idx] = struct{}{}
		}
		for _, idx := range additionalKeep {
			keepSet[idx] = struct{}{}
		}

		// Rebuild KeepIndices in stream order
		selection.KeepIndices = selection.KeepIndices[:0]
		for _, stream := range probe.Streams {
			if stream.CodecType != "audio" {
				continue
			}
			if _, ok := keepSet[stream.Index]; ok {
				selection.KeepIndices = append(selection.KeepIndices, stream.Index)
			}
		}

		// Rebuild RemovedIndices excluding the additional kept tracks
		newRemoved := make([]int, 0, len(selection.RemovedIndices))
		for _, idx := range selection.RemovedIndices {
			if _, ok := keepSet[idx]; !ok {
				newRemoved = append(newRemoved, idx)
			}
		}
		selection.RemovedIndices = newRemoved

		if logger != nil && len(additionalKeep) > 0 {
			logger.Debug("merged additional audio tracks",
				logging.Int("additional_count", len(additionalKeep)),
				logging.Int("total_kept", len(selection.KeepIndices)),
			)
		}
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

	result := AudioRefinementResult{
		PrimaryAudioDescription: findAudioDescription(probe.Streams, selection.PrimaryIndex),
		KeptIndices:             selection.KeepIndices,
	}

	needsRemux := selection.Changed(totalAudio) || needsDispositionFix(probe.Streams, selection.KeepIndices)
	if !needsRemux {
		return result, nil
	}

	tmpPath := deriveTempAudioPath(path)
	ffmpegBinary := "ffmpeg"
	if cfg != nil {
		ffmpegBinary = deps.ResolveFFmpegPath()
	}
	if err := remuxAudioSelection(ctx, ffmpegBinary, path, tmpPath, selection); err != nil {
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
		logging.Int("removed_count", len(selection.RemovedIndices)),
	}
	for _, idx := range selection.RemovedIndices {
		fields = append(fields, logging.String(fmt.Sprintf("removed_%d", idx), fmt.Sprintf("%d", idx)))
	}
	logger.Info("refined ripped audio tracks", logging.Args(fields...)...)
	return result, nil
}

func remuxAudioSelection(ctx context.Context, ffmpegBinary, src, dst string, selection audio.Selection) error {
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
		// Set first audio stream as default
		args = append(args, "-disposition:a:0", "default")
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
	lang := langpkg.ExtractFromTags(stream.Tags)
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

// normalizeCodecName returns a human-friendly codec name for display.
func normalizeCodecName(s ffprobe.Stream) string {
	switch strings.ToLower(s.CodecName) {
	case "truehd":
		return "TrueHD"
	case "dts":
		profile := strings.ToLower(s.Profile)
		if strings.Contains(profile, "ma") {
			return "DTS-HD MA"
		}
		if strings.Contains(profile, "hd") {
			return "DTS-HD"
		}
		return "DTS"
	case "eac3":
		return "E-AC3"
	case "ac3":
		return "AC3"
	case "flac":
		return "FLAC"
	case "pcm_s16le", "pcm_s24le", "pcm_s32le":
		return "PCM"
	case "aac":
		return "AAC"
	case "opus":
		return "Opus"
	default:
		return strings.ToUpper(s.CodecName)
	}
}

// formatChannelLayout returns a human-friendly channel layout string.
func formatChannelLayout(s ffprobe.Stream) string {
	layout := strings.TrimSpace(s.ChannelLayout)
	if layout == "" {
		switch s.Channels {
		case 1:
			return "mono"
		case 2:
			return "stereo"
		case 6:
			return "5.1"
		case 8:
			return "7.1"
		case 0:
			return ""
		default:
			return fmt.Sprintf("%dch", s.Channels)
		}
	}
	// Normalize common layout variants
	switch strings.ToLower(layout) {
	case "5.1(side)", "5.1":
		return "5.1"
	case "7.1(wide)", "7.1":
		return "7.1"
	default:
		return layout
	}
}

// formatAudioDescription builds a human-readable audio description from stream metadata.
// Example output: "TrueHD 7.1 Atmos" or "DTS-HD MA 5.1"
func formatAudioDescription(s ffprobe.Stream) string {
	if s.CodecType != "audio" {
		return ""
	}

	parts := []string{normalizeCodecName(s)}

	if layout := formatChannelLayout(s); layout != "" {
		parts = append(parts, layout)
	}

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

	actualCount := probe.AudioStreamCount()
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

// probeVideo runs ffprobe on a video file and returns the parsed result.
func probeVideo(ctx context.Context, ffprobeBinary, path string) (*ffprobe.Result, error) {
	result, err := ffprobe.Inspect(ctx, ffprobeBinary, path)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// findAudioDescription returns the formatted audio description for the specified stream index.
// If index is negative, returns the description of the first audio stream found.
func findAudioDescription(streams []ffprobe.Stream, index int) string {
	for _, s := range streams {
		if s.CodecType != "audio" {
			continue
		}
		if index < 0 || s.Index == index {
			return formatAudioDescription(s)
		}
	}
	return ""
}
