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

func TestDiscNumberText(t *testing.T) {
	if got := discNumberText(2); got != "2" {
		t.Fatalf("discNumberText(2) = %q, want 2", got)
	}
	if got := discNumberText(0); got != "-" {
		t.Fatalf("discNumberText(0) = %q, want -", got)
	}
}

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

func TestDirectQueueReadsMissingDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")

	items, err := directQueueItems(dbPath)
	if err != nil || items != nil {
		t.Fatalf("directQueueItems on missing db = (%v, %v), want (nil, nil)", items, err)
	}
	item, err := directQueueItem(dbPath, 1)
	if err != nil || item != nil {
		t.Fatalf("directQueueItem on missing db = (%v, %v), want (nil, nil)", item, err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("direct read must not create the database file: %v", err)
	}
}

func TestDirectQueueReads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "queue.db")
	store, err := queue.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	alpha, err := store.NewDisc("Alpha", "fp-alpha")
	if err != nil {
		t.Fatalf("insert alpha: %v", err)
	}
	beta, err := store.NewDisc("Beta", "fp-beta")
	if err != nil {
		t.Fatalf("insert beta: %v", err)
	}
	if err := store.MoveToStage(beta, queue.StageEncoding); err != nil {
		t.Fatalf("move beta: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	items, err := directQueueItems(dbPath)
	if err != nil {
		t.Fatalf("directQueueItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("directQueueItems returned %d items, want 2", len(items))
	}

	filtered, err := directQueueItems(dbPath, queue.StageEncoding)
	if err != nil {
		t.Fatalf("directQueueItems filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].DiscTitle != "Beta" {
		t.Fatalf("stage filter returned %+v, want single Beta item", filtered)
	}

	got, err := directQueueItem(dbPath, alpha.ID)
	if err != nil {
		t.Fatalf("directQueueItem: %v", err)
	}
	if got == nil || got.DiscTitle != "Alpha" || got.DiscFingerprint != "fp-alpha" {
		t.Fatalf("directQueueItem returned %+v, want Alpha", got)
	}

	missing, err := directQueueItem(dbPath, 9999)
	if err != nil || missing != nil {
		t.Fatalf("directQueueItem missing id = (%v, %v), want (nil, nil)", missing, err)
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
