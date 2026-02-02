package jellyfin_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/services/jellyfin"
)

type staticMetadata struct {
	filename string
	movie    bool
}

func (m staticMetadata) GetLibraryPath(root, moviesDir, tvDir string) string {
	if m.movie {
		return filepath.Join(root, moviesDir)
	}
	return filepath.Join(root, tvDir)
}

func (m staticMetadata) GetFilename() string { return m.filename }
func (m staticMetadata) IsMovie() bool       { return m.movie }
func (m staticMetadata) Title() string       { return m.filename }
func (m staticMetadata) GetEdition() string  { return "" }

func TestSimpleServiceOrganizeAddsSuffixWhenOverwriteDisabled(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	libraryDir := filepath.Join(base, "library")
	moviesDir := "movies"
	if err := os.MkdirAll(filepath.Join(libraryDir, moviesDir), 0o755); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}

	existing := filepath.Join(libraryDir, moviesDir, "Demo.mkv")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	stagingDir := filepath.Join(base, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	source := filepath.Join(stagingDir, "demo.mkv")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	svc := jellyfin.NewSimpleService(libraryDir, moviesDir, "tv", false)
	meta := staticMetadata{filename: "Demo", movie: true}

	finalPath, err := svc.Organize(context.Background(), source, meta)
	if err != nil {
		t.Fatalf("Organize: %v", err)
	}

	expected := filepath.Join(libraryDir, moviesDir, "Demo (1).mkv")
	if finalPath != expected {
		t.Fatalf("expected %s, got %s", expected, finalPath)
	}
	if _, err := os.Stat(existing); err != nil {
		t.Fatalf("expected original file to remain: %v", err)
	}
	data, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("expected new file content, got %q", string(data))
	}
}

func TestSimpleServiceOrganizeOverwritesWhenEnabled(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	libraryDir := filepath.Join(base, "library")
	moviesDir := "movies"
	targetDir := filepath.Join(libraryDir, moviesDir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}

	target := filepath.Join(targetDir, "Demo.mkv")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	stagingDir := filepath.Join(base, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	source := filepath.Join(stagingDir, "demo.mkv")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	svc := jellyfin.NewSimpleService(libraryDir, moviesDir, "tv", true)
	meta := staticMetadata{filename: "Demo", movie: true}

	finalPath, err := svc.Organize(context.Background(), source, meta)
	if err != nil {
		t.Fatalf("Organize: %v", err)
	}

	if finalPath != target {
		t.Fatalf("expected overwrite to keep target path, got %s", finalPath)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read overwritten file: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("expected overwritten content, got %q", string(data))
	}
}
