package deps

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ResolveFFmpegPath returns the ffmpeg binary path, checking environment
// variables first and then falling back to PATH.
func ResolveFFmpegPath() string {
	if candidate := resolveEnvFFmpeg(); candidate != "" {
		if info, statErr := os.Stat(candidate); statErr == nil && isExecutable(info) {
			return candidate
		}
	}
	if ffmpegPath, err := exec.LookPath("ffmpeg"); err == nil {
		return ffmpegPath
	}
	return "ffmpeg"
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
