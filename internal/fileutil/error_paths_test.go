package fileutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyRejectsMissingSourceDirectoryAndExistingDestinationDirectory(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{"missing source", func() error { return CopyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dest")) }, "open source"},
		{"destination directory", func() error { return CopyFile(source, dir) }, "create destination"},
		{"source directory", func() error { return CopyFileVerifiedWithProgress(dir, filepath.Join(dir, "dest"), nil) }, "copy data"},
		{"verified destination directory", func() error { return CopyFileVerifiedWithProgress(source, dir, nil) }, "create destination"},
		{"hardlink destination directory", func() error { return LinkOrCopyFileVerified(source, dir, nil) }, "remove existing destination"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error: %v", err)
			}
		})
	}
	path := filepath.Join(dir, "removed")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	removeBestEffort(path)
	removeBestEffort(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup: %v", err)
	}
}
