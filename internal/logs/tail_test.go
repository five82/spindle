package logs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"spindle/internal/logs"
)

func TestTailLastLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spindle.log")
	content := "a\nb\nc\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	result, err := logs.Tail(context.Background(), path, logs.TailOptions{Offset: -1, Limit: 2})
	if err != nil {
		t.Fatalf("tail returned error: %v", err)
	}
	if len(result.Lines) != 2 || result.Lines[0] != "b" || result.Lines[1] != "c" {
		t.Fatalf("unexpected lines: %#v", result.Lines)
	}
	if result.Offset == 0 {
		t.Fatal("expected offset to advance")
	}
}

func TestTailFollowWaits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spindle.log")
	if err := os.WriteFile(path, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	opts := logs.TailOptions{Offset: -1, Limit: 1}
	result, err := logs.Tail(ctx, path, opts)
	if err != nil {
		t.Fatalf("initial tail: %v", err)
	}
	if len(result.Lines) != 1 {
		t.Fatalf("expected initial line, got %#v", result.Lines)
	}

	done := make(chan struct{})
	go func(offset int64) {
		res, err := logs.Tail(ctx, path, logs.TailOptions{Offset: offset, Follow: true, Wait: 5 * time.Second})
		if err != nil {
			t.Errorf("follow tail error: %v", err)
		}
		if len(res.Lines) != 1 || res.Lines[0] != "later" {
			t.Errorf("unexpected follow lines: %#v", res.Lines)
		}
		close(done)
	}(result.Offset)

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat log: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString("later\n"); err != nil {
		t.Fatalf("append log: %v", err)
	}
	_ = f.Close()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("tail follow did not return")
	}
}
