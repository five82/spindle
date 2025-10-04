package drapto

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		fmt.Println(`{"percent":0,"stage":"start","message":"begin"}`)
		fmt.Println(`{"percent":50,"stage":"encoding","message":"halfway"}`)
		fmt.Println(`{"percent":100,"stage":"complete","message":"done"}`)
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "encode failed")
		os.Exit(1)
	case "badjson":
		fmt.Println("not-json")
		fmt.Println(`{"percent":75,"stage":"encoding","message":"progress"}`)
		os.Exit(0)
	default:
		os.Exit(0)
	}
}
