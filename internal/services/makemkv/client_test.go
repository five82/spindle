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

	_, err = client.Rip(context.Background(), "/dev/sr0", "Sample", tmp, nil, nil)
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
	if _, err := client.Rip(context.Background(), "/dev/sr0", "Sample", t.TempDir(), nil, nil); err == nil {
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

	path, err := client.Rip(context.Background(), "/dev/sr0", "Sample Movie", destDir, []int{0}, nil)
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
	expectedArgs := []string{"--robot", "mkv", "dev:/dev/sr0", "0", destDir}
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
	path, err := client.Rip(context.Background(), "/dev/sr0", "Sample Show", destDir, ids, nil)
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

// progressCapturingExecutor emits the given lines AND creates a file so Rip
// succeeds. This lets us capture progress updates through the public API.
type progressCapturingExecutor struct {
	lines []string
}

func (p *progressCapturingExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	destDir := args[len(args)-1]
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	titleArg := args[len(args)-2]
	id, _ := strconv.Atoi(titleArg)
	filePath := filepath.Join(destDir, fmt.Sprintf("title_t%02d.mkv", id))
	if err := os.WriteFile(filePath, []byte("rip"), 0o644); err != nil {
		return err
	}
	for _, line := range p.lines {
		if onStdout != nil {
			onStdout(line)
		}
	}
	return nil
}

func TestRipProgressPhaseAttribution(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")

	// Simulate MakeMKV output: analysis phase then ripping phase.
	exec := &progressCapturingExecutor{lines: []string{
		`PRGT:0,0,"Analyzing"`,
		"PRGV:0,66,100",
		"PRGV:0,100,100",
		`PRGT:0,0,"Saving to MKV file"`,
		"PRGV:0,0,100",
		"PRGV:0,50,100",
		"PRGV:0,100,100",
	}}

	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var updates []makemkv.ProgressUpdate
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", destDir, []int{0}, func(u makemkv.ProgressUpdate) {
		updates = append(updates, u)
	})
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("expected progress updates")
	}

	// Analysis-phase updates should have Stage="Analyzing"
	for _, u := range updates[:2] {
		if u.Stage != "Analyzing" {
			t.Errorf("expected Stage=Analyzing during analysis phase, got %q (percent=%.0f)", u.Stage, u.Percent)
		}
	}
	// Ripping-phase updates should have Stage="Ripping"
	for _, u := range updates[2:] {
		if u.Stage != "Ripping" {
			t.Errorf("expected Stage=Ripping during rip phase, got %q (percent=%.0f)", u.Stage, u.Percent)
		}
	}
}

func TestRipProgressDefaultsToAnalyzingBeforePRGT(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")

	// PRGV lines arrive before any PRGT line (no phase context yet).
	exec := &progressCapturingExecutor{lines: []string{
		"PRGV:0,30,100",
	}}

	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	var updates []makemkv.ProgressUpdate
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", destDir, []int{0}, func(u makemkv.ProgressUpdate) {
		updates = append(updates, u)
	})
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Stage != "Analyzing" {
		t.Errorf("expected Stage=Analyzing before any PRGT, got %q", updates[0].Stage)
	}
}

// --- MSG code handling tests ---

// contextAwareExecutor respects context cancellation (simulates how the real
// executor aborts when the context is cancelled by msgHandler).
type contextAwareExecutor struct {
	lines []string
}

func (e *contextAwareExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	destDir := args[len(args)-1]
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for _, line := range e.lines {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if onStdout != nil {
			onStdout(line)
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// Create output file unless context was cancelled
	titleArg := args[len(args)-2]
	id, _ := strconv.Atoi(titleArg)
	filePath := filepath.Join(destDir, fmt.Sprintf("title_t%02d.mkv", id))
	return os.WriteFile(filePath, []byte("rip"), 0o644)
}

func TestMSGLicenseExpiredAborts(t *testing.T) {
	exec := &contextAwareExecutor{lines: []string{
		`MSG:5021,0,1,"This application version is too old","",""`,
	}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", t.TempDir(), []int{0}, nil)
	if err == nil {
		t.Fatal("expected error for expired license")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "license") &&
		!strings.Contains(strings.ToLower(err.Error()), "too old") {
		t.Fatalf("expected license-related error, got: %v", err)
	}
}

func TestMSGRipCompletedZeroTitles(t *testing.T) {
	// MSG:5004 with 0 saved, 0 failed — MakeMKV exits 0 but nothing was saved.
	exec := &contextAwareExecutor{lines: []string{
		`MSG:5004,0,2,"Copy complete. 0 titles saved, 0 failed.","%1 titles saved, %2 failed.","0","0"`,
	}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", t.TempDir(), []int{0}, nil)
	if err == nil {
		t.Fatal("expected error for zero titles saved")
	}
	if !strings.Contains(err.Error(), "0 titles") {
		t.Fatalf("expected zero-titles error, got: %v", err)
	}
}

func TestMSGRipCompletedSuccess(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	exec := &progressCapturingExecutor{lines: []string{
		`MSG:5004,0,2,"Copy complete. 3 titles saved, 0 failed.","%1 titles saved, %2 failed.","3","0"`,
	}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", destDir, []int{0}, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestMSGReadErrorNonFatal(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	exec := &progressCapturingExecutor{lines: []string{
		`MSG:2003,0,1,"Read error at sector 12345","",""`,
	}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", destDir, []int{0}, nil)
	if err != nil {
		t.Fatalf("read error should not be fatal, got: %v", err)
	}
}

func TestMSGWriteErrorFatal(t *testing.T) {
	exec := &contextAwareExecutor{lines: []string{
		`MSG:2019,0,1,"Write error: No such file or directory","",""`,
	}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", t.TempDir(), []int{0}, nil)
	if err == nil {
		t.Fatal("expected error for write error with 'No such file'")
	}
	if !strings.Contains(err.Error(), "No such file") {
		t.Fatalf("expected 'No such file' in error, got: %v", err)
	}
}

func TestMSGEvalSharewareExpiredAborts(t *testing.T) {
	exec := &contextAwareExecutor{lines: []string{
		`MSG:5055,0,1,"Evaluation period has expired","",""`,
	}}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", t.TempDir(), []int{0}, nil)
	if err == nil {
		t.Fatal("expected error for expired shareware")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "license") &&
		!strings.Contains(strings.ToLower(err.Error()), "expired") {
		t.Fatalf("expected license-related error, got: %v", err)
	}
}

func TestParseMSGSprintf(t *testing.T) {
	line := `MSG:5004,0,2,"Copy complete. 3 titles saved, 1 failed.","%1 titles saved, %2 failed.","3","1"`
	saved, failed := makemkv.ParseMSGSprintf(line)
	if saved != 3 {
		t.Errorf("expected saved=3, got %d", saved)
	}
	if failed != 1 {
		t.Errorf("expected failed=1, got %d", failed)
	}
}

func TestRipMinLengthIncluded(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	exec := &fileCreatingExecutor{}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec), makemkv.WithMinLength(120))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", destDir, []int{0}, nil)
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	if len(exec.args) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(exec.args))
	}
	args := exec.args[0]
	found := false
	for _, a := range args {
		if a == "--minlength=120" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected --minlength=120 in args, got %v", args)
	}
}

func TestRipMinLengthExcludedWhenZero(t *testing.T) {
	tmp := t.TempDir()
	destDir := filepath.Join(tmp, "dest")
	exec := &fileCreatingExecutor{}
	client, err := makemkv.New("makemkvcon", 5, makemkv.WithExecutor(exec), makemkv.WithMinLength(0))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	_, err = client.Rip(context.Background(), "/dev/sr0", "Test", destDir, []int{0}, nil)
	if err != nil {
		t.Fatalf("Rip returned error: %v", err)
	}
	if len(exec.args) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(exec.args))
	}
	for _, a := range exec.args[0] {
		if strings.HasPrefix(a, "--minlength") {
			t.Fatalf("did not expect --minlength in args when 0, got %v", exec.args[0])
		}
	}
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
