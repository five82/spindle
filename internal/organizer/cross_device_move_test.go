//go:build linux

package organizer

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/five82/spindle/internal/fileutil"
)

func TestMoveOrCopyCrossDeviceAndFailedCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(src, []byte("movie bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir, err := os.MkdirTemp("/dev/shm", "spindle-organizer-test-")
	if err != nil {
		t.Skipf("no shared memory mount: %v", err)
	}
	defer func() { _ = os.RemoveAll(dstDir) }()
	srcInfo, err := os.Stat(filepath.Dir(src))
	if err != nil {
		t.Fatal(err)
	}
	dstInfo, err := os.Stat(dstDir)
	if err != nil {
		t.Fatal(err)
	}
	if srcInfo.Sys().(*syscall.Stat_t).Dev == dstInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("shared memory is not a separate filesystem")
	}
	var updates []fileutil.CopyProgress
	dst := filepath.Join(dstDir, "movie.mkv")
	if err := moveOrCopyWithProgress(src, dst, func(p fileutil.CopyProgress) { updates = append(updates, p) }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "movie bytes" || len(updates) == 0 {
		t.Fatalf("copied data %q, progress %v, error %v", data, updates, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source left after copy: %v", err)
	}
	src = t.TempDir() // renaming a directory across devices reaches the copy fallback, which rejects non-files.
	err = moveOrCopyWithProgress(src, filepath.Join(dstDir, "invalid.mkv"), nil)
	if err == nil || !strings.Contains(err.Error(), "copy data") {
		t.Fatalf("copy failure: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source lost on failed copy: %v", err)
	}
}
