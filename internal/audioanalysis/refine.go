package audioanalysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/audio"
	"github.com/five82/spindle/internal/media/ffprobe"
)

// AudioRefinementResult holds the result of audio track refinement.
type AudioRefinementResult struct {
	PrimaryAudioDescription string
	KeptIndices             []int
}

// RefineAudioTargets selects and keeps only the desired audio tracks in MKV
// files. Each unique path is probed and, when needed, remuxed so the selected
// primary track becomes the first audio stream and the only default audio
// stream. Additional keep indices (e.g. commentary) are preserved when valid
// for a given file.
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

	var out AudioRefinementResult
	for i, path := range unique {
		result, err := ffprobe.Inspect(ctx, "", path)
		if err != nil {
			return nil, fmt.Errorf("ffprobe %s: %w", path, err)
		}

		audioCount := result.AudioStreamCount()
		sel := audio.Select(result.Streams, logger)
		if audioCount == 0 {
			logger.Info("audio refinement: no audio streams",
				"decision_type", logs.DecisionAudioRefinement,
				"decision_result", "skipped",
				"decision_reason", "no audio streams",
				"path", path,
			)
			if i == 0 {
				out = AudioRefinementResult{}
			}
			continue
		}

		keptIndices := buildKeptIndices(audioCount, sel.PrimaryIndex, additionalKeep)
		needsRemux := len(keptIndices) != audioCount || needsDispositionFix(result, sel.PrimaryIndex)
		if !needsRemux {
			logger.Info("audio refinement: no remux needed",
				"decision_type", logs.DecisionAudioRefinement,
				"decision_result", "skipped",
				"decision_reason", "audio tracks and default disposition already correct",
				"path", path,
			)
		} else {
			if err := remuxAudioTracks(ctx, logger, path, keptIndices); err != nil {
				return nil, fmt.Errorf("remux %s: %w", path, err)
			}
			if err := validateRemuxedAudio(ctx, path, len(keptIndices)); err != nil {
				return nil, err
			}
			logger.Info("audio refinement complete",
				"decision_type", logs.DecisionAudioRefinement,
				"decision_result", "remuxed",
				"decision_reason", fmt.Sprintf("kept %d of %d audio tracks", len(keptIndices), audioCount),
				"path", path,
			)
		}

		if i == 0 {
			out = AudioRefinementResult{
				PrimaryAudioDescription: sel.PrimaryLabel(),
				KeptIndices:             keptIndices,
			}
		}
	}

	return &out, nil
}

func buildKeptIndices(audioCount, primaryIndex int, additionalKeep []int) []int {
	if audioCount <= 0 {
		return nil
	}

	keepSet := map[int]bool{primaryIndex: true}
	for _, idx := range additionalKeep {
		if idx >= 0 && idx < audioCount {
			keepSet[idx] = true
		}
	}

	keptIndices := []int{primaryIndex}
	for i := 0; i < audioCount; i++ {
		if i == primaryIndex {
			continue
		}
		if keepSet[i] {
			keptIndices = append(keptIndices, i)
		}
	}
	return keptIndices
}

func needsDispositionFix(result *ffprobe.Result, primaryIndex int) bool {
	audioStreams := result.AudioStreams()
	if len(audioStreams) == 0 {
		return false
	}
	if primaryIndex != 0 {
		return true
	}
	for i, st := range audioStreams {
		isDefault := st.Disposition["default"] == 1
		if i == 0 && !isDefault {
			return true
		}
		if i > 0 && isDefault {
			return true
		}
	}
	return false
}

func validateRemuxedAudio(ctx context.Context, path string, expectedAudio int) error {
	postResult, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return fmt.Errorf("post-remux ffprobe %s: %w", path, err)
	}
	postAudio := postResult.AudioStreamCount()
	if postAudio != expectedAudio {
		return fmt.Errorf("post-remux audio count %d != expected %d for %s", postAudio, expectedAudio, path)
	}
	audioStreams := postResult.AudioStreams()
	for i, st := range audioStreams {
		isDefault := st.Disposition["default"] == 1
		if i == 0 && !isDefault {
			return fmt.Errorf("post-remux first audio stream is not default for %s", path)
		}
		if i > 0 && isDefault {
			return fmt.Errorf("post-remux non-primary audio stream %d is still default for %s", i, path)
		}
	}
	return nil
}

// remuxAudioTracks creates a new MKV with only the selected audio tracks,
// copying all video, subtitle, and data streams.
func remuxAudioTracks(ctx context.Context, logger *slog.Logger, path string, keptAudioIndices []int) error {
	logger.Info("audio remux started",
		"decision_type", logs.DecisionAudioRemux,
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("keeping %d audio tracks", len(keptAudioIndices)),
		"path", path,
	)

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
	// Copy codecs, clear inherited audio defaults, then set first mapped audio as default.
	args = append(args, "-c", "copy", "-disposition:a", "0", "-disposition:a:0", "default")
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
		"decision_type", logs.DecisionAudioRemux,
		"decision_result", "completed",
		"decision_reason", fmt.Sprintf("kept %d audio tracks", len(keptAudioIndices)),
		"path", path,
	)

	return nil
}
