package drapto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewCLIWithBinary(t *testing.T) {
	cli := NewCLI(WithBinary("/opt/drapto"))
	if cli.binary != "/opt/drapto" {
		t.Fatalf("expected binary override to be applied, got %q", cli.binary)
	}
}

func TestCLIEncodeRequiresInput(t *testing.T) {
	cli := NewCLI()
	if _, err := cli.Encode(context.Background(), "", "/tmp", nil); err == nil {
		t.Fatal("expected error when input path is empty")
	}
}

func TestCLIEncodeRequiresOutputDir(t *testing.T) {
	cli := NewCLI()
	if _, err := cli.Encode(context.Background(), "/media/movie.mkv", "", nil); err == nil {
		t.Fatal("expected error when output directory is empty")
	}
}

func TestCLIEncodeIncludesLogDir(t *testing.T) {
	var capturedArgs []string
	original := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string(nil), args...)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "DRAPTO_HELPER_MODE=success")
		return cmd
	}
	t.Cleanup(func() {
		commandContext = original
	})

	cli := NewCLI(WithLogDir("/var/log/spindle/drapto"))
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, nil); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	if len(capturedArgs) == 0 {
		t.Fatalf("expected Drapto command arguments to be captured")
	}

	expectedFlag := "--log-dir"
	found := false
	for i, arg := range capturedArgs {
		if arg == expectedFlag {
			if i+1 >= len(capturedArgs) {
				t.Fatalf("log dir flag present without accompanying value")
			}
			if capturedArgs[i+1] != "/var/log/spindle/drapto" {
				t.Fatalf("expected log dir %q, got %q", "/var/log/spindle/drapto", capturedArgs[i+1])
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Drapto command to include %s flag, got args %v", expectedFlag, capturedArgs)
	}
}

func TestCLIEncodeIncludesEncodingFlags(t *testing.T) {
	var capturedArgs []string
	original := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		capturedArgs = append([]string(nil), args...)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "DRAPTO_HELPER_MODE=success")
		return cmd
	}
	t.Cleanup(func() {
		commandContext = original
	})

	cli := NewCLI(WithPreset(6), WithDisableDenoise(true))
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, nil); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	if len(capturedArgs) == 0 {
		t.Fatalf("expected Drapto command arguments to be captured")
	}

	if idx := findArg(capturedArgs, "--responsive"); idx == -1 {
		t.Fatalf("expected Drapto command to include --responsive, got %v", capturedArgs)
	}

	idx := findArg(capturedArgs, "--preset")
	if idx == -1 {
		t.Fatalf("expected Drapto command to include --preset, got %v", capturedArgs)
	}
	if idx+1 >= len(capturedArgs) {
		t.Fatalf("--preset flag missing value in args %v", capturedArgs)
	}
	if capturedArgs[idx+1] != "6" {
		t.Fatalf("expected preset value 6, got %q", capturedArgs[idx+1])
	}

	if findArg(capturedArgs, "--no-denoise") == -1 {
		t.Fatalf("expected Drapto command to include --no-denoise, got %v", capturedArgs)
	}

	if findArg(capturedArgs, "--progress-json") == -1 {
		t.Fatalf("expected Drapto command to include --progress-json, got %v", capturedArgs)
	}
}

func TestCLIEncodeSuccess(t *testing.T) {
	setHelperCommand(t, "success")

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "source.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	var updates []ProgressUpdate
	path, err := cli.Encode(context.Background(), input, outputDir, func(update ProgressUpdate) {
		updates = append(updates, update)
	})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	expected := filepath.Join(outputDir, stem+".mkv")
	if path != expected {
		t.Fatalf("expected output path %q, got %q", expected, path)
	}
	if len(updates) != 3 {
		t.Fatalf("expected 3 progress updates, got %d", len(updates))
	}
	if updates[len(updates)-1].Percent != 100 {
		t.Fatalf("expected final update to report 100 percent, got %f", updates[len(updates)-1].Percent)
	}
	middle := updates[1]
	if middle.Stage != "encoding" {
		t.Fatalf("expected encoding stage, got %q", middle.Stage)
	}
	if middle.ETA != 5*time.Minute {
		t.Fatalf("expected eta 5m, got %s", middle.ETA)
	}
	if middle.Speed != 3.0 {
		t.Fatalf("expected speed 3.0x, got %f", middle.Speed)
	}
	if middle.FPS != 72.0 {
		t.Fatalf("expected fps 72, got %f", middle.FPS)
	}
}

func TestCLIEncodeFailure(t *testing.T) {
	setHelperCommand(t, "failure")

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, nil); err == nil {
		t.Fatal("expected encode failure error")
	}
}

func TestCLIEncodeSkipsInvalidJSON(t *testing.T) {
	setHelperCommand(t, "badjson")

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "clip.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	var updates []ProgressUpdate
	if _, err := cli.Encode(context.Background(), input, outputDir, func(update ProgressUpdate) {
		updates = append(updates, update)
	}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 progress update from valid json, got %d", len(updates))
	}
	if updates[0].Stage != "encoding" {
		t.Fatalf("expected stage 'encoding', got %q", updates[0].Stage)
	}
}

func setHelperCommand(t *testing.T, mode string) {
	t.Helper()
	original := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", fmt.Sprintf("DRAPTO_HELPER_MODE=%s", mode))
		return cmd
	}
	t.Cleanup(func() {
		commandContext = original
	})
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	switch os.Getenv("DRAPTO_HELPER_MODE") {
	case "success":
		fmt.Println(`{"type":"stage_progress","percent":0,"stage":"start","message":"begin"}`)
		fmt.Println(`{"type":"encoding_progress","percent":50,"stage":"encoding","eta_seconds":300,"speed":3.0,"fps":72.0,"bitrate":"3400kbps"}`)
		fmt.Println(`{"type":"stage_progress","percent":100,"stage":"complete","message":"done"}`)
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "encode failed")
		os.Exit(1)
	case "badjson":
		fmt.Println("not-json")
		fmt.Println(`{"type":"encoding_progress","percent":75,"stage":"encoding","eta_seconds":120}`)
		os.Exit(0)
	default:
		os.Exit(0)
	}
}

func findArg(args []string, target string) int {
	for i, arg := range args {
		if arg == target {
			return i
		}
	}
	return -1
}
