package audioanalysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

// commentaryLabel formats a stream title for a commentary track.
// Empty titles become "Commentary". Titles already containing "commentary"
// (case-insensitive) are unchanged. Otherwise " (Commentary)" is appended.
func commentaryLabel(original string) string {
	title := strings.TrimSpace(original)
	if title == "" {
		return "Commentary"
	}
	if strings.Contains(strings.ToLower(title), "commentary") {
		return title
	}
	return title + " (Commentary)"
}

// ApplyCommentaryDisposition sets the "comment" disposition and updates the
// title metadata on the specified audio tracks in an MKV file using FFmpeg
// copy-mode remux. audioTitles maps audio-relative indices to their current
// titles (from the caller's existing probe result).
func ApplyCommentaryDisposition(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	commentaryAudioIndices []int,
	audioTitles map[int]string,
) error {
	if len(commentaryAudioIndices) == 0 {
		return nil
	}

	logger.Info("applying commentary disposition",
		"event_type", "commentary_disposition_start",
		"path", path,
		"tracks", commentaryAudioIndices,
	)

	dir := filepath.Dir(path)
	tmpPath := filepath.Join(dir, ".disposition-"+filepath.Base(path))

	args := []string{"-y", "-i", path, "-map", "0", "-c", "copy"}
	for _, idx := range commentaryAudioIndices {
		idxStr := strconv.Itoa(idx)
		args = append(args, "-disposition:a:"+idxStr, "comment")
		label := commentaryLabel(audioTitles[idx])
		args = append(args, "-metadata:s:a:"+idxStr, "title="+label)
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
		"decision_type", logs.DecisionCommentaryDisposition,
		"decision_result", "applied",
		"decision_reason", fmt.Sprintf("marked %d tracks as commentary", len(commentaryAudioIndices)),
		"path", path,
		"tracks", commentaryAudioIndices,
	)

	return nil
}

// ValidateCommentaryLabeling verifies that the specified audio tracks have
// both the "comment" disposition set and a title containing "Commentary".
func ValidateCommentaryLabeling(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	expectedIndices []int,
) error {
	logger = logs.Default(logger)
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

	var issues []string
	audioIdx := 0
	for _, s := range result.Streams {
		if s.CodecType != "audio" {
			continue
		}
		if expected[audioIdx] {
			disp, ok := s.Disposition["comment"]
			if !ok || disp != 1 {
				issues = append(issues, fmt.Sprintf("audio track %d missing comment disposition", audioIdx))
			}
			title := s.Tags["title"]
			if !strings.Contains(strings.ToLower(title), "commentary") {
				issues = append(issues, fmt.Sprintf("audio track %d title %q lacks Commentary label", audioIdx, title))
			}
		}
		audioIdx++
	}

	if len(issues) > 0 {
		return fmt.Errorf("commentary labeling validation failed: %s", strings.Join(issues, "; "))
	}

	logger.Info("commentary labeling validated",
		"decision_type", logs.DecisionCommentaryDisposition,
		"decision_result", "valid",
		"decision_reason", fmt.Sprintf("verified %d tracks", len(expectedIndices)),
		"path", path,
	)
	return nil
}

// RemapCommentaryIndices maps original commentary track indices to their new
// positions within the kept indices after audio refinement.
func RemapCommentaryIndices(
	logger *slog.Logger,
	original []ripspec.CommentaryTrackRef,
	keptIndices []int,
) []ripspec.CommentaryTrackRef {
	logger = logs.Default(logger)
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

	logger.Info("commentary indices remapped",
		"decision_type", logs.DecisionCommentaryRemapping,
		"decision_result", fmt.Sprintf("remapped_%d", len(remapped)),
		"decision_reason", fmt.Sprintf("original=%d kept=%d", len(original), len(keptIndices)),
	)
	return remapped
}
