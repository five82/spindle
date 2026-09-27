// Package keyframe provides fixed-length chunk generation for video encoding.
package keyframe

import (
	"fmt"
	"os"
	"path/filepath"
)

// GenerateFixedChunks creates chunk boundaries at fixed time intervals.
// Returns a sorted slice of frame numbers where chunks start.
func GenerateFixedChunks(totalFrames int, fpsNum, fpsDen uint32, chunkDurationSecs float64) []int {
	if fpsDen == 0 || totalFrames <= 0 {
		return []int{0}
	}

	fps := float64(fpsNum) / float64(fpsDen)
	framesPerChunk := int(fps * chunkDurationSecs)
	if framesPerChunk < 1 {
		framesPerChunk = 1
	}

	var keyframes []int
	for frame := 0; frame < totalFrames; frame += framesPerChunk {
		keyframes = append(keyframes, frame)
	}

	// Ensure we have at least frame 0
	if len(keyframes) == 0 {
		keyframes = []int{0}
	}

	return keyframes
}

// ExtractKeyframesIfNeeded generates fixed-length chunks and writes them to chunk-plan.txt if not already present.
// Returns the path to the chunk plan file.
func ExtractKeyframesIfNeeded(videoPath, workDir string, fpsNum, fpsDen uint32, totalFrames int, chunkDuration float64) (string, error) {
	boundaryFile := filepath.Join(workDir, "chunk-plan.txt")

	// Check if chunk plan already exists
	if _, err := os.Stat(boundaryFile); err == nil {
		return boundaryFile, nil
	}

	// Generate fixed-length chunks
	keyframes := GenerateFixedChunks(totalFrames, fpsNum, fpsDen, chunkDuration)

	if err := writeBoundaryFile(boundaryFile, keyframes); err != nil {
		return "", err
	}

	return boundaryFile, nil
}

// dedupe removes duplicate values from a sorted slice.
func dedupe(sorted []int) []int {
	if len(sorted) <= 1 {
		return sorted
	}

	result := make([]int, 0, len(sorted))
	result = append(result, sorted[0])

	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1] {
			result = append(result, sorted[i])
		}
	}

	return result
}

// writeBoundaryFile writes frame numbers to a chunk plan file.
func writeBoundaryFile(path string, frames []int) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create chunk plan file: %w", err)
	}
	defer func() { _ = file.Close() }()

	for _, frame := range frames {
		if _, err := fmt.Fprintf(file, "%d\n", frame); err != nil {
			return fmt.Errorf("failed to write chunk plan file: %w", err)
		}
	}

	return nil
}
