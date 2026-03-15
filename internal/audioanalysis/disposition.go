package audioanalysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

// ApplyCommentaryDisposition sets the "comment" disposition on the specified
// audio tracks in an MKV file using FFmpeg copy-mode remux.
func ApplyCommentaryDisposition(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	commentaryAudioIndices []int,
) error {
	if len(commentaryAudioIndices) == 0 {
		return nil
	}

	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, ".disposition-"+filepath.Base(path))

	args := []string{"-y", "-i", path, "-map", "0", "-c", "copy"}
	for _, idx := range commentaryAudioIndices {
		args = append(args, "-disposition:a:"+strconv.Itoa(idx), "comment")
	}
	args = append(args, tmpPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg disposition: %w: %s", err, output)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename disposition file: %w", err)
	}

	logger.Info("commentary disposition applied",
		"event_type", "commentary_disposition",
		"path", path,
		"tracks", commentaryAudioIndices,
	)

	return nil
}

// ValidateCommentaryLabeling verifies that the specified audio tracks have
// the "comment" disposition set.
func ValidateCommentaryLabeling(
	ctx context.Context,
	path string,
	expectedIndices []int,
) error {
	if len(expectedIndices) == 0 {
		return nil
	}

	result, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return fmt.Errorf("ffprobe validate: %w", err)
	}

	expected := make(map[int]bool)
	for _, idx := range expectedIndices {
		expected[idx] = true
	}

	audioIdx := 0
	for _, s := range result.Streams {
		if s.CodecType != "audio" {
			continue
		}
		if expected[audioIdx] {
			disp, ok := s.Disposition["comment"]
			if !ok || disp != 1 {
				return fmt.Errorf("audio track %d missing comment disposition", audioIdx)
			}
		}
		audioIdx++
	}

	return nil
}

// RemapCommentaryIndices maps original commentary track indices to their new
// positions within the kept indices after audio refinement.
func RemapCommentaryIndices(
	original []ripspec.CommentaryTrackRef,
	keptIndices []int,
) []ripspec.CommentaryTrackRef {
	if len(original) == 0 || len(keptIndices) == 0 {
		return nil
	}

	// Build old -> new index mapping.
	indexMap := make(map[int]int)
	for newIdx, oldIdx := range keptIndices {
		indexMap[oldIdx] = newIdx
	}

	var remapped []ripspec.CommentaryTrackRef
	for _, ref := range original {
		if newIdx, ok := indexMap[ref.Index]; ok {
			remapped = append(remapped, ripspec.CommentaryTrackRef{
				Index:      newIdx,
				Confidence: ref.Confidence,
				Reason:     ref.Reason,
			})
		}
	}

	return remapped
}
