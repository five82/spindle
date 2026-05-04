package subtitle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/language"
)

// MuxTrack describes one SRT track to mux into an MKV.
type MuxTrack struct {
	Path, Language string
	Forced         bool
}

// MuxRequest describes an MKV subtitle mux operation.
type MuxRequest struct {
	VideoPath, OutputPath string
	Tracks                []MuxTrack
	ReplaceExisting       bool
}

// BuildSubtitleMuxArgs builds mkvmerge arguments for adding subtitle tracks.
func BuildSubtitleMuxArgs(outputPath, videoPath string, tracks []MuxTrack, replaceExisting bool) []string {
	args := []string{"-o", outputPath}
	if replaceExisting {
		args = append(args, "--no-subtitles")
	}
	args = append(args, videoPath)
	for _, track := range tracks {
		lang := language.ToISO3(track.Language)
		if lang == "" || lang == "und" {
			lang = "eng"
		}
		name := language.DisplayName(track.Language)
		if strings.TrimSpace(name) == "" {
			name = "English"
		}
		defaultFlag, forcedFlag := "0:no", "0:no"
		if track.Forced {
			name += " (Forced)"
			defaultFlag, forcedFlag = "0:yes", "0:yes"
		}
		args = append(args, "--language", "0:"+lang, "--track-name", "0:"+name,
			"--default-track-flag", defaultFlag, "--forced-track", forcedFlag, track.Path)
	}
	return args
}

// MuxSubtitleTracks runs mkvmerge and atomically replaces OutputPath.
func MuxSubtitleTracks(ctx context.Context, req MuxRequest) (string, error) {
	outputPath := req.OutputPath
	if strings.TrimSpace(outputPath) == "" {
		outputPath = req.VideoPath
	}
	if strings.TrimSpace(req.VideoPath) == "" {
		return "", fmt.Errorf("subtitle mux missing video path")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("create mux output dir: %w", err)
	}

	ext := filepath.Ext(outputPath)
	tmpPath := strings.TrimSuffix(outputPath, ext) + ".tmp" + ext
	cmd := exec.CommandContext(ctx, "mkvmerge", BuildSubtitleMuxArgs(tmpPath, req.VideoPath, req.Tracks, req.ReplaceExisting)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("mkvmerge: %w: %s", err, output)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename muxed file: %w", err)
	}
	return outputPath, nil
}

// MKVHasSubtitleTrack reports whether mkvmerge sees any subtitle tracks.
func MKVHasSubtitleTrack(ctx context.Context, path string) bool {
	out, err := exec.CommandContext(ctx, "mkvmerge", "--identify", path).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Track ID") && strings.Contains(line, "subtitles") {
			return true
		}
	}
	return false
}
