package deps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckFFmpegForDrapto reports the FFmpeg binary Drapto will execute.
//
// Drapto uses the ffmpeg-sidecar crate, whose lookup order prefers an ffmpeg
// binary that sits next to the Drapto executable and falls back to resolving
// "ffmpeg" from PATH. This helper mirrors that logic so Spindle status output
// matches Drapto's behaviour.
func CheckFFmpegForDrapto(draptoCommand string) Status {
	result := Status{
		Name:        "FFmpeg",
		Description: "Used by Drapto for encoding",
	}

	if candidate := resolveEnvFFmpeg(); candidate != "" {
		if info, statErr := os.Stat(candidate); statErr == nil && isExecutable(info) {
			result.Command = candidate
			result.Available = true
			return result
		}
		result.Command = candidate
		result.Available = false
		result.Detail = fmt.Sprintf("binary %q not found", candidate)
		return result
	}

	draptoBinary := strings.TrimSpace(draptoCommand)
	if draptoBinary != "" {
		if resolved, err := exec.LookPath(draptoBinary); err == nil {
			if candidate, ok := ffmpegSidecarCandidate(resolved); ok {
				if info, statErr := os.Stat(candidate); statErr == nil && isExecutable(info) {
					result.Command = candidate
					result.Available = true
					return result
				}
			}
		}
	}

	ffmpegName := "ffmpeg"
	if ffmpegPath, err := exec.LookPath(ffmpegName); err == nil {
		result.Command = ffmpegPath
		result.Available = true
		return result
	}

	result.Command = ffmpegName
	result.Available = false
	result.Detail = fmt.Sprintf("binary %q not found", ffmpegName)
	return result
}

// ResolveFFmpegPath returns the ffmpeg path Drapto would use, or "ffmpeg" when unresolved.
func ResolveFFmpegPath(draptoCommand string) string {
	if candidate := resolveEnvFFmpeg(); candidate != "" {
		if info, statErr := os.Stat(candidate); statErr == nil && isExecutable(info) {
			return candidate
		}
	}
	draptoBinary := strings.TrimSpace(draptoCommand)
	if draptoBinary != "" {
		if resolved, err := exec.LookPath(draptoBinary); err == nil {
			if candidate, ok := ffmpegSidecarCandidate(resolved); ok {
				if info, statErr := os.Stat(candidate); statErr == nil && isExecutable(info) {
					return candidate
				}
			}
		}
	}
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		return ffmpegPath
	}
	return "ffmpeg"
}

func ffmpegSidecarCandidate(draptoPath string) (string, bool) {
	if draptoPath == "" {
		return "", false
	}
	dir := filepath.Dir(draptoPath)
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name), true
}

func isExecutable(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func resolveEnvFFmpeg() string {
	for _, key := range []string{"SPINDLE_FFMPEG_PATH", "FFMPEG_PATH"} {
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ResolveFFprobePath returns the ffprobe binary path if overridden or on PATH.
func ResolveFFprobePath(defaultBinary string) string {
	if candidate := resolveEnvFFprobe(); candidate != "" {
		if info, statErr := os.Stat(candidate); statErr == nil && isExecutable(info) {
			return candidate
		}
		return candidate
	}
	if strings.TrimSpace(defaultBinary) != "" {
		if resolved, err := exec.LookPath(strings.TrimSpace(defaultBinary)); err == nil {
			return resolved
		}
	}
	if ffprobePath, err := exec.LookPath("ffprobe"); err == nil {
		return ffprobePath
	}
	if strings.TrimSpace(defaultBinary) != "" {
		return strings.TrimSpace(defaultBinary)
	}
	return "ffprobe"
}

func resolveEnvFFprobe() string {
	for _, key := range []string{"SPINDLE_FFPROBE_PATH", "FFPROBE_PATH"} {
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
