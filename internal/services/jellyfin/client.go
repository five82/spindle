package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// MediaMetadata represents the subset of media info needed for organization.
type MediaMetadata interface {
	GetLibraryPath(root, moviesDir, tvDir string) string
	GetFilename() string
	IsMovie() bool
	Title() string
}

// Service defines Jellyfin/library operations used by the organizer.
type Service interface {
	Organize(ctx context.Context, sourcePath string, meta MediaMetadata) (string, error)
	Refresh(ctx context.Context, meta MediaMetadata) error
}

// FileMover handles moving files into the library directory.
func FileMover(sourcePath, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		var linkErr *os.LinkError
		if errors.As(err, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV) {
			if err := copyFileContents(sourcePath, targetPath); err != nil {
				return fmt.Errorf("copy file across devices: %w", err)
			}
			if err := os.Remove(sourcePath); err != nil {
				return fmt.Errorf("remove source after copy: %w", err)
			}
			return nil
		}
		return fmt.Errorf("move file: %w", err)
	}
	return nil
}

func copyFileContents(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer source.Close()

	info, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	dest, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	if _, err := io.Copy(dest, source); err != nil {
		dest.Close()
		return fmt.Errorf("copy data: %w", err)
	}
	if err := dest.Sync(); err != nil {
		dest.Close()
		return fmt.Errorf("sync destination: %w", err)
	}
	if err := dest.Close(); err != nil {
		return fmt.Errorf("close destination: %w", err)
	}
	return nil
}

func removeExistingTarget(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat existing target: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("existing library path %q is a directory", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove existing target %q: %w", path, err)
	}
	return nil
}

// SimpleService moves files into the library directory tree.
type SimpleService struct {
	LibraryDir        string
	MoviesDir         string
	TVDir             string
	MoveFunc          func(string, string) error
	OverwriteExisting bool
}

// NewSimpleService constructs a simple Jellyfin organizer.
func NewSimpleService(libraryDir, moviesDir, tvDir string, overwriteExisting bool) *SimpleService {
	return &SimpleService{
		LibraryDir:        libraryDir,
		MoviesDir:         moviesDir,
		TVDir:             tvDir,
		MoveFunc:          FileMover,
		OverwriteExisting: overwriteExisting,
	}
}

func (s *SimpleService) Organize(ctx context.Context, sourcePath string, meta MediaMetadata) (string, error) {
	targetDir := meta.GetLibraryPath(s.LibraryDir, s.MoviesDir, s.TVDir)
	filename := fmt.Sprintf("%s%s", meta.GetFilename(), filepath.Ext(sourcePath))
	targetPath := filepath.Join(targetDir, filename)

	finalPath := targetPath
	if s.OverwriteExisting {
		if err := removeExistingTarget(finalPath); err != nil {
			return "", err
		}
	} else {
		counter := 1
		for {
			info, err := os.Stat(finalPath)
			if err != nil {
				if os.IsNotExist(err) {
					break
				}
				return "", fmt.Errorf("stat candidate path: %w", err)
			}
			if info.IsDir() {
				return "", fmt.Errorf("library target %q already exists as directory", finalPath)
			}
			finalPath = filepath.Join(targetDir, fmt.Sprintf("%s (%d)%s", meta.GetFilename(), counter, filepath.Ext(sourcePath)))
			counter++
		}
	}

	if err := s.MoveFunc(sourcePath, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}

func (s *SimpleService) Refresh(ctx context.Context, meta MediaMetadata) error {
	// Placeholder: real implementation uses Jellyfin HTTP API.
	return nil
}
