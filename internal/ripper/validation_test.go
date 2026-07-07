package ripper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRippedArtifact_EmptyPath(t *testing.T) {
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestValidateRippedArtifact_NonExistent(t *testing.T) {
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), "/nonexistent/file.mkv"); err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestValidateRippedArtifact_Directory(t *testing.T) {
	h := &Handler{}
	if err := h.validateRippedArtifact(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestValidateRippedArtifact_TooSmall(t *testing.T) {
	h := &Handler{}
	f := filepath.Join(t.TempDir(), "small.mkv")
	if err := os.WriteFile(f, []byte("too small"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.validateRippedArtifact(context.Background(), f); err == nil {
		t.Fatal("expected error for file under 10 MB")
	}
}
