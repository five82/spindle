package encode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileHardLinksOrReplacesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.ivf")
	if err := os.WriteFile(src, []byte("encoded frames"), 0600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "nested", "output.ivf")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := os.Stat(dst)
	if err != nil || !os.SameFile(original, linked) {
		t.Fatalf("expected hard link: %v", err)
	}
	// When a stale destination exists, hard-linking fails and copyFile must
	// overwrite it with current probe bytes rather than retaining old bytes.
	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old probe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "encoded frames" {
		t.Fatalf("copied bytes %q: %v", data, err)
	}
	copied, err := os.Stat(dst)
	if err != nil || os.SameFile(original, copied) {
		t.Fatalf("fallback should copy, not link: %v", err)
	}
}

func TestCopyFileReportsFilesystemFailures(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.ivf")
	if err := os.WriteFile(src, []byte("frames"), 0600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, destination string }{
		{src, filepath.Join(blocker, "output.ivf")},
		{filepath.Join(dir, "missing.ivf"), filepath.Join(dir, "missing-target.ivf")},
		{src, dir}, // Destination is a directory, not a file.
	} {
		if err := copyFile(tc.source, tc.destination); err == nil {
			t.Fatalf("copyFile(%q, %q) unexpectedly succeeded", tc.source, tc.destination)
		}
	}
}
