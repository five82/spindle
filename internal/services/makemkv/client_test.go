package makemkv_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"spindle/internal/services/makemkv"
)

type stubExecutor struct {
	lines []string
	err   error
	calls int
}

func (s *stubExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	s.calls++
	for _, line := range s.lines {
		onStdout(line)
	}
	return s.err
}

func TestRipCreatesPlaceholderWhenOutputMissing(t *testing.T) {
	tmp := t.TempDir()
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(&stubExecutor{lines: []string{"PRGV:1,10,starting", "PRGV:1,80,ripping"}}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var progress []makemkv.ProgressUpdate
	path, err := client.Rip(context.Background(), "Sample", "", tmp, func(update makemkv.ProgressUpdate) {
		progress = append(progress, update)
	})
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected rip file to exist: %v", err)
	}
	if len(progress) != 2 || progress[0].Percent != 10 || progress[1].Percent != 80 {
		t.Fatalf("unexpected progress: %#v", progress)
	}
}

func TestRipReturnsExecutorError(t *testing.T) {
	client, err := makemkv.New("makemkvcon", 1, makemkv.WithExecutor(&stubExecutor{err: errors.New("boom")}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.Rip(context.Background(), "Sample", "", t.TempDir(), nil); err == nil {
		t.Fatal("expected error from executor")
	}
}

func TestRipCopiesSourceWhenProvided(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.mkv")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(&stubExecutor{}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	destDir := filepath.Join(tmp, "dest")
	path, err := client.Rip(context.Background(), "Movie", src, destDir, nil)
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(contents) != "data" {
		t.Fatalf("expected copied data, got %q", contents)
	}
}
