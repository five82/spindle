// Package chunk provides types and functions for managing video encoding chunks.
package chunk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	nativeaudio "github.com/five82/reel/internal/audio"
	"github.com/five82/reel/internal/media"
)

// ExtractAudio encodes source audio streams to Opus using native libav/libopusenc.
func ExtractAudio(ctx context.Context, inputPath, workDir string, audioStreams []media.AudioStreamInfo, videoDurationSecs float64) ([]nativeaudio.EncodedStream, error) {
	return nativeaudio.EncodeStreams(ctx, inputPath, workDir, audioStreams, videoDurationSecs)
}

// CleanupWorkDir removes the work directory and all its contents.
func CleanupWorkDir(workDir string) error {
	return os.RemoveAll(workDir)
}

// CreateWorkDir creates the work directory structure.
func CreateWorkDir(workDir string) error {
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create work directory: %w", err)
	}
	return EnsureEncodeDir(workDir)
}

// WorkDirExists checks if the work directory exists.
func WorkDirExists(workDir string) bool {
	_, err := os.Stat(workDir)
	return err == nil
}

// GetWorkDirPath returns the full path to the work directory for a given input file.
func GetWorkDirPath(inputPath, tempDir string) string {
	dirName := WorkDirName(inputPath)
	return filepath.Join(tempDir, dirName)
}
