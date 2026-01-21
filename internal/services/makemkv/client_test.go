package makemkv_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"spindle/internal/services/makemkv"
)

type stubExecutor struct {
	lines []string
	err   error
	calls int
	args  [][]string
}

func (s *stubExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	s.calls++
	cloned := append([]string(nil), args...)
	s.args = append(s.args, cloned)
	for _, line := range s.lines {
		onStdout(line)
	}
	return s.err
}

func TestRipErrorsWhenNoOutputProduced(t *testing.T) {
	tmp := t.TempDir()
	exec := &stubExecutor{lines: []string{"PRGV:0,10,100", "PRGV:0,80,100"}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Rip(context.Background(), "Sample", tmp, nil, nil)
	if err == nil {
		t.Fatal("expected error when MakeMKV produces no output")
	}
	if !strings.Contains(err.Error(), "no output file") {
		t.Fatalf("expected 'no output file' error, got: %v", err)
	}
}

func TestRipReturnsExecutorError(t *testing.T) {
	client, err := makemkv.New("makemkvcon", 1, makemkv.WithExecutor(&stubExecutor{err: errors.New("boom")}))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := client.Rip(context.Background(), "Sample", t.TempDir(), nil, nil); err == nil {
		t.Fatal("expected error from executor")
	}
}

func TestRipSelectsSpecificTitleAndRenamesOutput(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	exec := &fileCreatingExecutor{}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	path, err := client.Rip(context.Background(), "Sample Movie", destDir, []int{0}, nil)
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	if filepath.Base(path) != "Sample Movie.mkv" {
		t.Fatalf("expected sanitized filename, got %q", filepath.Base(path))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected renamed output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "title_t00.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected intermediate title removal, got err=%v", err)
	}
	if len(exec.args) != 1 {
		t.Fatalf("expected executor invocation recorded")
	}
	gotArgs := exec.args[0]
	expectedArgs := []string{"--robot", "mkv", "disc:0", "0", destDir}
	if !equalStrings(gotArgs, expectedArgs) {
		t.Fatalf("unexpected makemkv args: got %v want %v", gotArgs, expectedArgs)
	}
}

func TestRipSequentialTitles(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	exec := &fileCreatingExecutor{}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ids := []int{0, 3, 7}
	path, err := client.Rip(context.Background(), "Sample Show", destDir, ids, nil)
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	for _, id := range ids {
		name := fmt.Sprintf("title_t%02d.mkv", id)
		if _, statErr := os.Stat(filepath.Join(destDir, name)); statErr != nil {
			t.Fatalf("expected rip output %s: %v", name, statErr)
		}
	}
	if filepath.Base(path) != "title_t07.mkv" {
		t.Fatalf("expected last rip path to be title_t07.mkv, got %q", filepath.Base(path))
	}
	if exec.calls != len(ids) {
		t.Fatalf("expected %d rip invocations, got %d", len(ids), exec.calls)
	}
}

type fileCreatingExecutor struct {
	args  [][]string
	calls int
}

func (f *fileCreatingExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	clone := append([]string(nil), args...)
	f.args = append(f.args, clone)
	f.calls++
	destDir := args[len(args)-1]
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	titleArg := args[len(args)-2]
	id, _ := strconv.Atoi(titleArg)
	filePath := filepath.Join(destDir, fmt.Sprintf("title_t%02d.mkv", id))
	return os.WriteFile(filePath, []byte("rip"), 0o644)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
