package encoding

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestUpdateDraptoLogPointerSelectsNewestLog(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "drapto_encode_run_20240101_120000.log")
	newer := filepath.Join(dir, "drapto_encode_run_20240101_120100.log")
	if err := os.WriteFile(older, []byte("old"), 0o644); err != nil {
		t.Fatalf("write older log: %v", err)
	}
	if err := os.Chtimes(older, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour)); err != nil && !os.IsPermission(err) {
		t.Fatalf("chtimes older log: %v", err)
	}
	if err := os.WriteFile(newer, []byte("new"), 0o644); err != nil {
		t.Fatalf("write newer log: %v", err)
	}
	pointer := filepath.Join(t.TempDir(), "drapto.log")

	if err := updateDraptoLogPointer(dir, pointer); err != nil {
		t.Fatalf("updateDraptoLogPointer returned error: %v", err)
	}

	data, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatalf("read pointer: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("expected pointer to reference newest log, got %q", string(data))
	}

	if runtime.GOOS != "windows" {
		if linkTarget, err := os.Readlink(pointer); err == nil {
			if linkTarget != newer {
				t.Fatalf("expected symlink to %q, got %q", newer, linkTarget)
			}
		}
	}
}

func TestUpdateDraptoLogPointerNoLogs(t *testing.T) {
	dir := t.TempDir()
	pointer := filepath.Join(t.TempDir(), "drapto.log")
	if err := updateDraptoLogPointer(dir, pointer); err != nil {
		t.Fatalf("expected no error when directory empty, got %v", err)
	}
	if _, err := os.Stat(pointer); !os.IsNotExist(err) {
		t.Fatalf("expected pointer not to be created, got err=%v", err)
	}
}
