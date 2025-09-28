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
