package util

import (
	"os"
	"path/filepath"
	"strings"
)

// VideoExtensions is the list of supported video file extensions.
var VideoExtensions = map[string]bool{
	".mkv":  true,
	".wmv":  true,
	".ts":   true,
	".avi":  true,
	".mp4":  true,
	".m4v":  true,
	".mpg":  true,
	".mpeg": true,
	".mov":  true,
	".webm": true,
	".flv":  true,
	".m2ts": true,
	".ogv":  true,
	".vob":  true,
}

// IsVideoFile checks if the given path is a valid video file.
func IsVideoFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	ext := strings.ToLower(filepath.Ext(path))
	return VideoExtensions[ext]
}

// GetFilename returns the filename from a path.
func GetFilename(path string) string {
	return filepath.Base(path)
}

// GetFileStem returns the filename without extension.
func GetFileStem(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// GetFileSize returns the size of a file in bytes.
func GetFileSize(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return uint64(info.Size()), nil
}

// EnsureDirectory creates a directory if it doesn't exist.
func EnsureDirectory(path string) error {
	return os.MkdirAll(path, 0755)
}

// DirectoryExists checks if a directory exists.
func DirectoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// FileExists checks if a file exists.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ResolveOutputPath determines the output path for an encoded file.
func ResolveOutputPath(inputPath, outputDir string, targetOverride string) string {
	if targetOverride != "" {
		return filepath.Join(outputDir, targetOverride)
	}
	stem := GetFileStem(inputPath)
	return filepath.Join(outputDir, stem+".mkv")
}

// OutputPathInfo contains resolved output path information.
type OutputPathInfo struct {
	// OutputDir is the directory where output files should be written.
	OutputDir string
	// FilenameOverride is set when user specifies output.mkv instead of a directory.
	FilenameOverride string
}

// ResolveOutputArg resolves the output argument into a directory and optional filename.
// When the input is a single file AND the output has a .mkv extension,
// the output is treated as a filename. Otherwise, it's treated as a directory.
func ResolveOutputArg(inputPath, outputPath string) (OutputPathInfo, error) {
	inputInfo, err := os.Stat(inputPath)
	if err != nil {
		return OutputPathInfo{}, err
	}

	ext := strings.ToLower(filepath.Ext(outputPath))

	// Single file input with extension on output - treat as filename
	if !inputInfo.IsDir() && ext != "" {
		if ext != ".mkv" {
			return OutputPathInfo{}, os.ErrInvalid
		}

		parentDir := filepath.Dir(outputPath)
		if parentDir == "" {
			parentDir = "."
		}
		filename := filepath.Base(outputPath)

		return OutputPathInfo{
			OutputDir:        parentDir,
			FilenameOverride: filename,
		}, nil
	}

	// Directory input OR no extension - treat output as directory
	return OutputPathInfo{
		OutputDir:        outputPath,
		FilenameOverride: "",
	}, nil
}
