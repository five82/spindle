package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteFile fills the target path with the requested number of bytes using a
// simple repeating pattern. A size <= 0 writes a single byte.
func WriteFile(t testing.TB, path string, size int64) {
	t.Helper()

	if size <= 0 {
		size = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	const chunkSize = 32 * 1024
	buf := make([]byte, chunkSize)
	for i := range buf {
		buf[i] = 0x42
	}

	remaining := size
	for remaining > 0 {
		toWrite := int64(chunkSize)
		if remaining < toWrite {
			toWrite = remaining
		}
		if _, err := f.Write(buf[:toWrite]); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		remaining -= toWrite
	}
}
