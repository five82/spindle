package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}
}

func TestCopyFileMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyFileMode(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	// Check executable bits are set (umask may clear some bits).
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("expected executable bits, got %o", info.Mode().Perm())
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Fatalf("content mismatch: got %q", got)
	}
}

func TestCopyFileVerified(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	content := []byte("verified copy content")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyFileVerified(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}
}

func TestCopyFileVerified_MissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nonexistent")
	dst := filepath.Join(dir, "dst.bin")

	err := CopyFileVerified(src, dst)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestCopyFile_MissingSource(t *testing.T) {
	dir := t.TempDir()
	err := CopyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "dst"))
	if err == nil {
		t.Fatal("expected error for missing source")
	}
}
