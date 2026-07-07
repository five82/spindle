package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestClearQueueDBFilesRemovesOnlyQueueFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "queue.db")
	paths := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	other := filepath.Join(dir, "staging-output.mkv")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write other: %v", err)
	}

	if err := clearQueueDBFiles(dbPath); err != nil {
		t.Fatalf("clearQueueDBFiles: %v", err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed with non-missing error: %v", path, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("non-queue file was removed or became unreadable: %v", err)
	}
}

func TestClearQueueDBFilesMissingFilesOK(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	if err := clearQueueDBFiles(dbPath); err != nil {
		t.Fatalf("clearQueueDBFiles missing files: %v", err)
	}
}

func TestPrintTaskLines(t *testing.T) {
	tasks := []httpapi.TaskResponse{
		{
			Type:  "ripping",
			State: string(queue.TaskRunning),
			Progress: httpapi.ProgressResponse{
				Percent:     42,
				Message:     "Ripping title 3",
				BytesCopied: 1024,
				TotalBytes:  4096,
			},
			ActiveAssetKey: "s01e01",
		},
		{
			Type:  "encoding",
			State: string(queue.TaskFailed),
			Error: "encoder crashed",
		},
		{
			Type:  "subtitling",
			State: string(queue.TaskPending),
		},
	}

	out := captureStdout(t, func() {
		printTaskLines("  ", tasks, true)
	})

	if !strings.Contains(out, "Progress (ripping):") || !strings.Contains(out, "Ripping title 3") || !strings.Contains(out, "42%") {
		t.Errorf("missing running task progress line: %q", out)
	}
	if !strings.Contains(out, "s01e01") {
		t.Errorf("missing active asset key in verbose mode: %q", out)
	}
	if !strings.Contains(out, "1024 B / 4096 B") {
		t.Errorf("missing bytes line: %q", out)
	}
	if !strings.Contains(out, "Failed:") || !strings.Contains(out, "encoding") || !strings.Contains(out, "encoder crashed") {
		t.Errorf("missing failed task line: %q", out)
	}
	if strings.Contains(out, "subtitling") {
		t.Errorf("pending task should not be rendered: %q", out)
	}
}

func TestPrintTaskLinesNonVerboseOmitsAssetKey(t *testing.T) {
	tasks := []httpapi.TaskResponse{
		{
			Type:  "encoding",
			State: string(queue.TaskRunning),
			Progress: httpapi.ProgressResponse{
				Percent: 10,
				Message: "Encoding",
			},
			ActiveAssetKey: "s01e02",
		},
	}

	out := captureStdout(t, func() {
		printTaskLines("", tasks, false)
	})

	if strings.Contains(out, "s01e02") {
		t.Errorf("non-verbose output should omit active asset key: %q", out)
	}
	if !strings.Contains(out, "Progress (encoding):") {
		t.Errorf("missing progress line: %q", out)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		os.Stdout = orig
		t.Fatalf("close writer: %v", err)
	}
	os.Stdout = orig

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(data)
}
