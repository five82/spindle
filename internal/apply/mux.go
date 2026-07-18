package apply

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/internal/language"
)

// MuxTrack describes one SRT track to mux into an MKV.
type MuxTrack struct {
	Path, Language string
}

// MuxRequest describes an MKV subtitle mux operation.
type MuxRequest struct {
	VideoPath, OutputPath string
	Track                 MuxTrack
	ReplaceExisting       bool
}

// DisplaySubtitlePath returns the standard sidecar path for a video.
func DisplaySubtitlePath(videoPath, subtitleLanguage string) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	lang := language.ToISO2(subtitleLanguage)
	if lang == "" {
		lang = "en"
	}
	return base + "." + lang + ".srt"
}

func buildSubtitleMuxArgs(outputPath, videoPath string, track MuxTrack, replaceExisting bool) []string {
	args := []string{"-o", outputPath}
	if replaceExisting {
		args = append(args, "--no-subtitles")
	}
	args = append(args, videoPath)

	lang := language.ToISO3(track.Language)
	if lang == "" || lang == "und" {
		lang = "eng"
	}
	name := language.DisplayName(track.Language)
	if strings.TrimSpace(name) == "" {
		name = "English"
	}
	args = append(args, "--language", "0:"+lang, "--track-name", "0:"+name,
		"--default-track-flag", "0:no", "--forced-track", "0:no", track.Path)
	return args
}

// MuxSubtitleTrack runs mkvmerge and atomically replaces OutputPath.
func MuxSubtitleTrack(ctx context.Context, req MuxRequest) (string, error) {
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
	cmd := exec.CommandContext(ctx, "mkvmerge", buildSubtitleMuxArgs(tmpPath, req.VideoPath, req.Track, req.ReplaceExisting)...)
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

// muxDisplaySubtitle writes a separate output before the caller records it as
// the subtitled asset; the encoded source remains available if muxing fails.
func muxDisplaySubtitle(
	ctx context.Context,
	logger *slog.Logger,
	videoPath string,
	srtPath string,
	key string,
	subtitleLanguage string,
) (string, error) {
	dir := filepath.Dir(videoPath)
	ext := filepath.Ext(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), ext)
	outPath := filepath.Join(dir, base+".subtitled"+ext)

	logger.Info("subtitle mux started",
		"event_type", "mux_start",
		"episode_key", key,
		"video_path", videoPath,
		"subtitle_path", srtPath,
		"output_path", outPath,
	)
	muxStart := time.Now()
	muxedPath, err := MuxSubtitleTrack(ctx, MuxRequest{
		VideoPath:  videoPath,
		OutputPath: outPath,
		Track:      MuxTrack{Path: srtPath, Language: subtitleLanguage},
	})
	if err != nil {
		return "", fmt.Errorf("mux subtitles %s: %w", key, err)
	}

	logger.Info("subtitles muxed into MKV",
		"event_type", "mux_complete",
		"episode_key", key,
		"output_path", muxedPath,
		"duration_ms", time.Since(muxStart).Milliseconds(),
	)
	return muxedPath, nil
}
