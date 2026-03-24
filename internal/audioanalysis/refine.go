package audioanalysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/five82/spindle/internal/media/audio"
	"github.com/five82/spindle/internal/media/ffprobe"
)

// AudioRefinementResult holds the result of audio track refinement.
type AudioRefinementResult struct {
	PrimaryAudioDescription string
	KeptIndices             []int
}

// RefineAudioTargets selects and keeps only the desired audio tracks in MKV
// files. For files with <= 1 audio stream, no remux is needed and the single
// stream info is returned. For multi-audio files, the primary track is
// selected via audio.Select(), merged with additionalKeep indices, and a
// copy-mode remux is performed.
func RefineAudioTargets(
	ctx context.Context,
	logger *slog.Logger,
	paths []string,
	additionalKeep []int,
) (*AudioRefinementResult, error) {
	if len(paths) == 0 {
		return &AudioRefinementResult{}, nil
	}

	// Deduplicate paths.
	seen := make(map[string]bool)
	var unique []string
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}

	// Process the first path as the representative sample for audio selection.
	path := unique[0]
	result, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}

	audioCount := result.AudioStreamCount()
	if audioCount <= 1 {
		sel := audio.Select(result.Streams)
		logger.Info("audio refinement: single track, no remux needed",
			"decision_type", "audio_refinement",
			"decision_result", "skipped",
			"decision_reason", "single audio stream",
			"path", path,
		)
		return &AudioRefinementResult{
			PrimaryAudioDescription: sel.PrimaryLabel(),
			KeptIndices:             []int{0},
		}, nil
	}

	sel := audio.Select(result.Streams)

	// Merge additionalKeep into kept indices.
	keepSet := make(map[int]bool)
	for _, idx := range sel.KeepIndices {
		keepSet[idx] = true
	}
	for _, idx := range additionalKeep {
		keepSet[idx] = true
	}

	// Build final kept indices sorted.
	var keptIndices []int
	for i := 0; i < audioCount; i++ {
		if keepSet[i] {
			keptIndices = append(keptIndices, i)
		}
	}

	// Check if remux is needed.
	needsRemux := len(keptIndices) != audioCount
	if !needsRemux {
		logger.Info("audio refinement: all tracks kept, no remux needed",
			"decision_type", "audio_refinement",
			"decision_result", "skipped",
			"decision_reason", "all audio tracks selected",
			"path", path,
		)
		return &AudioRefinementResult{
			PrimaryAudioDescription: sel.PrimaryLabel(),
			KeptIndices:             keptIndices,
		}, nil
	}

	// Remux with only selected tracks.
	if err := remuxAudioTracks(ctx, logger, path, keptIndices); err != nil {
		return nil, fmt.Errorf("remux %s: %w", path, err)
	}

	// Validate.
	postResult, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return nil, fmt.Errorf("post-remux ffprobe %s: %w", path, err)
	}
	postAudio := postResult.AudioStreamCount()
	if postAudio != len(keptIndices) {
		return nil, fmt.Errorf("post-remux audio count %d != expected %d", postAudio, len(keptIndices))
	}

	logger.Info("audio refinement complete",
		"decision_type", "audio_refinement",
		"decision_result", "remuxed",
		"decision_reason", fmt.Sprintf("kept %d of %d audio tracks", len(keptIndices), audioCount),
		"path", path,
	)

	return &AudioRefinementResult{
		PrimaryAudioDescription: sel.PrimaryLabel(),
		KeptIndices:             keptIndices,
	}, nil
}

// remuxAudioTracks creates a new MKV with only the selected audio tracks,
// copying all video, subtitle, and data streams.
func remuxAudioTracks(ctx context.Context, logger *slog.Logger, path string, keptAudioIndices []int) error {
	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, ".refine-"+filepath.Base(path))

	args := []string{"-y", "-i", path}
	// Map all video streams.
	args = append(args, "-map", "0:v")
	// Map selected audio streams.
	for _, idx := range keptAudioIndices {
		args = append(args, "-map", "0:a:"+strconv.Itoa(idx))
	}
	// Map subtitles and data if present.
	args = append(args, "-map", "0:s?", "-map", "0:d?")
	// Copy codecs, set first audio as default.
	args = append(args, "-c", "copy", "-disposition:a:0", "default")
	args = append(args, tmpPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg remux: %w: %s", err, output)
	}

	// Replace original with remuxed file.
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename remuxed file: %w", err)
	}

	logger.Info("audio remux complete",
		"event_type", "audio_remux",
		"decision_type", "audio_remux",
		"decision_result", "completed",
		"decision_reason", fmt.Sprintf("kept %d audio tracks", len(keptAudioIndices)),
		"path", path,
	)

	return nil
}
