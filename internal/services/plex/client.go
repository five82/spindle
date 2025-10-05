package plex

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

// Service defines Plex/library operations used by the organizer.
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

// SimpleService is a placeholder organiser; a real implementation would call Plex APIs.
type SimpleService struct {
	LibraryDir    string
	MoviesDir     string
	TVDir         string
	MoviesLibrary string
	TVLibrary     string
	MoveFunc      func(string, string) error
}

// NewSimpleService constructs a simple Plex organizer.
func NewSimpleService(libraryDir, moviesDir, tvDir, moviesLibrary, tvLibrary string) *SimpleService {
	return &SimpleService{
		LibraryDir:    libraryDir,
		MoviesDir:     moviesDir,
		TVDir:         tvDir,
		MoviesLibrary: moviesLibrary,
		TVLibrary:     tvLibrary,
		MoveFunc:      FileMover,
	}
}

func (s *SimpleService) Organize(ctx context.Context, sourcePath string, meta MediaMetadata) (string, error) {
	targetDir := meta.GetLibraryPath(s.LibraryDir, s.MoviesDir, s.TVDir)
	filename := fmt.Sprintf("%s%s", meta.GetFilename(), filepath.Ext(sourcePath))
	targetPath := filepath.Join(targetDir, filename)

	finalPath := targetPath
	counter := 1
	for {
		if _, err := os.Stat(finalPath); os.IsNotExist(err) {
			break
		}
		finalPath = filepath.Join(targetDir, fmt.Sprintf("%s (%d)%s", meta.GetFilename(), counter, filepath.Ext(sourcePath)))
		counter++
	}

	if err := s.MoveFunc(sourcePath, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}

func (s *SimpleService) Refresh(ctx context.Context, meta MediaMetadata) error {
	// Placeholder: real implementation would call Plex HTTP API.
	return nil
}
