// Package chunk provides types and functions for managing video encoding chunks.
package chunk

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/five82/reel/internal/video"
)

// MergeOutput concatenates all chunk IVF files into a single IVF video file.
func MergeOutput(workDir string, inf *video.Info, numChunks int) error {
	if numChunks <= 0 {
		return fmt.Errorf("no chunks to merge")
	}
	const maxUint32 = int64(^uint32(0))
	if inf.Frames < 0 || int64(inf.Frames) > maxUint32 {
		return fmt.Errorf("invalid video info: frame count %d cannot be stored in IVF header", inf.Frames)
	}

	ivfFiles := make([]string, numChunks)
	for i := range numChunks {
		ivfFiles[i] = IVFPath(workDir, i)
	}

	if err := concatIVF(ivfFiles, GetVideoPath(workDir), uint32(inf.Frames)); err != nil {
		return fmt.Errorf("ivf concat failed: %w", err)
	}
	return nil
}

func concatIVF(files []string, outputPath string, totalFrames uint32) (err error) {
	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create merged IVF: %w", err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close merged IVF: %w", cerr)
		}
	}()

	var frameIdx uint32
	for i, path := range files {
		if err := appendIVF(out, path, i == 0, totalFrames, &frameIdx); err != nil {
			return err
		}
	}
	if frameIdx != totalFrames {
		return fmt.Errorf("merged IVF contains %d frames, expected %d", frameIdx, totalFrames)
	}
	return nil
}

func appendIVF(out io.Writer, path string, includeHeader bool, totalFrames uint32, frameIdx *uint32) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open IVF chunk %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	header := make([]byte, 32)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("failed to read IVF header from %s: %w", path, err)
	}
	if string(header[:4]) != "DKIF" || binary.LittleEndian.Uint16(header[6:8]) != 32 {
		return fmt.Errorf("invalid IVF header in %s", path)
	}

	if includeHeader {
		binary.LittleEndian.PutUint32(header[24:28], totalFrames)
		if _, err := out.Write(header); err != nil {
			return fmt.Errorf("failed to write IVF header: %w", err)
		}
	}
	for {
		frameHeader := make([]byte, 12)
		if _, err := io.ReadFull(file, frameHeader); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("failed to read IVF frame header from %s: %w", path, err)
		}
		frameSize := binary.LittleEndian.Uint32(frameHeader[:4])
		binary.LittleEndian.PutUint64(frameHeader[4:12], uint64(*frameIdx))
		if _, err := out.Write(frameHeader); err != nil {
			return fmt.Errorf("failed to write IVF frame header: %w", err)
		}
		if _, err := io.CopyN(out, file, int64(frameSize)); err != nil {
			return fmt.Errorf("failed to append IVF frame from %s: %w", path, err)
		}
		*frameIdx = *frameIdx + 1
	}
}

// GetVideoPath returns the path to the merged video file.
func GetVideoPath(workDir string) string {
	return filepath.Join(workDir, "video.ivf")
}
