package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string) string
		wantErr bool
	}{
		{
			name: "basic copy",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				src := filepath.Join(dir, "src.txt")
				if err := os.WriteFile(src, []byte("hello world"), 0o644); err != nil {
					t.Fatal(err)
				}
				return src
			},
		},
		{
			name: "permissions are 0644",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				src := filepath.Join(dir, "src.txt")
				if err := os.WriteFile(src, []byte("check perms"), 0o755); err != nil {
					t.Fatal(err)
				}
				return src
			},
		},
		{
			name: "missing source",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "nonexistent.txt")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := tt.setup(t, dir)
			dst := filepath.Join(dir, "dst.txt")

			err := CopyFile(src, dst)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			srcData, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			dstData, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			if string(srcData) != string(dstData) {
				t.Fatalf("content mismatch: got %q, want %q", dstData, srcData)
			}

			info, err := os.Stat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o644 {
				t.Fatalf("permissions: got %o, want 0644", perm)
			}
		})
	}
}

func TestCopyFileMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
	}{
		{name: "executable", mode: 0o755},
		{name: "read only", mode: 0o444},
		{name: "owner only", mode: 0o600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src.txt")
			dst := filepath.Join(dir, "dst.txt")

			if err := os.WriteFile(src, []byte("mode test"), 0o644); err != nil {
				t.Fatal(err)
			}

			if err := CopyFileMode(src, dst, tt.mode); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			info, err := os.Stat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != tt.mode {
				t.Fatalf("permissions: got %o, want %o", perm, tt.mode)
			}

			srcData, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			dstData, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			if string(srcData) != string(dstData) {
				t.Fatalf("content mismatch: got %q, want %q", dstData, srcData)
			}
		})
	}
}

func TestCopyFileVerified(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "small file", content: []byte("verified copy")},
		{name: "empty file", content: []byte{}},
		{name: "larger file", content: make([]byte, 1<<16)}, // 64 KiB of zeros
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src.bin")
			dst := filepath.Join(dir, "dst.bin")

			if err := os.WriteFile(src, tt.content, 0o644); err != nil {
				t.Fatal(err)
			}

			if err := CopyFileVerified(src, dst); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			dstData, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			if len(dstData) != len(tt.content) {
				t.Fatalf("size mismatch: got %d, want %d", len(dstData), len(tt.content))
			}

			info, err := os.Stat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o644 {
				t.Fatalf("permissions: got %o, want 0644", perm)
			}
		})
	}

	t.Run("missing source", func(t *testing.T) {
		dir := t.TempDir()
		err := CopyFileVerified(filepath.Join(dir, "missing"), filepath.Join(dir, "dst"))
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestCopyFileVerifiedWithProgress(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	content := make([]byte, 1<<16)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	var calls int
	var last CopyProgress
	if err := CopyFileVerifiedWithProgress(src, dst, func(p CopyProgress) {
		calls++
		last = p
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls == 0 {
		t.Fatal("expected progress callbacks")
	}
	if last.BytesCopied != int64(len(content)) {
		t.Fatalf("BytesCopied = %d, want %d", last.BytesCopied, len(content))
	}
	if last.TotalBytes != int64(len(content)) {
		t.Fatalf("TotalBytes = %d, want %d", last.TotalBytes, len(content))
	}
}
