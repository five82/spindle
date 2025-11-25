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
	if _, err := cli.Encode(context.Background(), "", "/tmp", EncodeOptions{}); err == nil {
		t.Fatal("expected error when input path is empty")
	}
}

func TestCLIEncodeRequiresOutputDir(t *testing.T) {
	cli := NewCLI()
	if _, err := cli.Encode(context.Background(), "/media/movie.mkv", "", EncodeOptions{}); err == nil {
		t.Fatal("expected error when output directory is empty")
	}
}

func TestCLIEncodeDisablesLogs(t *testing.T) {
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

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, EncodeOptions{}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	if findArg(capturedArgs, "--no-log") == -1 {
		t.Fatalf("expected Drapto command to include --no-log, got %v", capturedArgs)
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

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, EncodeOptions{}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	if len(capturedArgs) == 0 {
		t.Fatalf("expected Drapto command arguments to be captured")
	}

	if idx := findArg(capturedArgs, "--responsive"); idx == -1 {
		t.Fatalf("expected Drapto command to include --responsive, got %v", capturedArgs)
	}

	if findArg(capturedArgs, "--no-denoise") != -1 {
		t.Fatalf("expected Drapto command to omit --no-denoise, got %v", capturedArgs)
	}

	if findArg(capturedArgs, "--progress-json") == -1 {
		t.Fatalf("expected Drapto command to include --progress-json, got %v", capturedArgs)
	}
	if findArg(capturedArgs, "--preset") != -1 {
		t.Fatalf("expected Drapto command to omit --preset, got %v", capturedArgs)
	}
}

func TestCLIEncodeIncludesPresetProfile(t *testing.T) {
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

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, EncodeOptions{PresetProfile: "grain"}); err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	idx := findArg(capturedArgs, "--drapto-preset")
	if idx == -1 {
		t.Fatalf("expected Drapto command to include --drapto-preset, got %v", capturedArgs)
	}
	if idx+1 >= len(capturedArgs) {
		t.Fatalf("--drapto-preset flag missing value, args=%v", capturedArgs)
	}
	if capturedArgs[idx+1] != "grain" {
		t.Fatalf("expected preset profile grain, got %q", capturedArgs[idx+1])
	}
}

func TestCLIEncodeSuccess(t *testing.T) {
	setHelperCommand(t, "success")

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "source.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	var updates []ProgressUpdate
	path, err := cli.Encode(context.Background(), input, outputDir, EncodeOptions{Progress: func(update ProgressUpdate) {
		updates = append(updates, update)
	}})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	expected := filepath.Join(outputDir, stem+".mkv")
	if path != expected {
		t.Fatalf("expected output path %q, got %q", expected, path)
	}
	if len(updates) < 6 {
		t.Fatalf("expected multiple progress updates, got %d", len(updates))
	}
	if updates[0].Type != EventTypeHardware || updates[0].Hardware == nil || updates[0].Hardware.Hostname != "white" {
		t.Fatalf("expected first update to include hardware hostname, got %+v", updates[0])
	}
	var (
		progressSeen    bool
		validationSeen  bool
		resultSeen      bool
		finalPercent100 bool
	)
	for _, update := range updates {
		switch update.Type {
		case EventTypeEncodingProgress:
			progressSeen = true
			if update.Stage != "encoding" {
				t.Fatalf("expected encoding stage, got %q", update.Stage)
			}
			if update.ETA != 5*time.Minute {
				t.Fatalf("expected eta 5m, got %s", update.ETA)
			}
			if update.Speed != 3.0 {
				t.Fatalf("expected speed 3.0x, got %f", update.Speed)
			}
			if update.FPS != 72.0 {
				t.Fatalf("expected fps 72, got %f", update.FPS)
			}
		case EventTypeValidation:
			if update.Validation == nil || !update.Validation.Passed {
				t.Fatalf("expected validation summary to pass, got %+v", update.Validation)
			}
			validationSeen = true
		case EventTypeEncodingComplete:
			if update.Result == nil || update.Result.OriginalSize != 1000 || update.Result.EncodedSize != 800 {
				t.Fatalf("unexpected encoding result payload: %+v", update.Result)
			}
			resultSeen = true
		case EventTypeStageProgress:
			if update.Percent == 100 {
				finalPercent100 = true
			}
		}
	}
	if !progressSeen {
		t.Fatalf("expected to observe encoding progress event")
	}
	if !validationSeen {
		t.Fatalf("expected validation_complete event")
	}
	if !resultSeen {
		t.Fatalf("expected encoding_complete event")
	}
	if !finalPercent100 {
		t.Fatalf("expected final stage progress to reach 100 percent")
	}
}

func TestCLIEncodeFailure(t *testing.T) {
	setHelperCommand(t, "failure")

	cli := NewCLI()
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "movie.mkv")
	outputDir := filepath.Join(tempDir, "encoded")

	if _, err := cli.Encode(context.Background(), input, outputDir, EncodeOptions{}); err == nil {
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
	if _, err := cli.Encode(context.Background(), input, outputDir, EncodeOptions{Progress: func(update ProgressUpdate) {
		updates = append(updates, update)
	}}); err != nil {
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
		fmt.Println(`{"type":"hardware","hostname":"white"}`)
		fmt.Println(`{"type":"initialization","input_file":"source.mkv","output_file":"source.mkv","duration":"00:21:31","resolution":"1280x720","category":"HD","dynamic_range":"SDR","audio_description":"Stereo"}`)
		fmt.Println(`{"type":"encoding_config","encoder":"SVT-AV1","preset":"6","tune":"0","quality":"CRF 25","pixel_format":"yuv420p10le","matrix_coefficients":"bt709","audio_codec":"Opus","audio_description":"Stereo","drapto_preset":"Default","drapto_preset_settings":[{"key":"CRF","value":"25"}],"svtav1_params":"tune=0"}`)
		fmt.Println(`{"type":"stage_progress","percent":0,"stage":"analysis","message":"Analyzing video"}`)
		fmt.Println(`{"type":"encoding_progress","percent":50,"stage":"encoding","eta_seconds":300,"speed":3.0,"fps":72.0,"bitrate":"3400kbps","total_frames":1000,"current_frame":500}`)
		fmt.Println(`{"type":"validation_complete","validation_passed":true,"validation_steps":[{"step":"Video codec","passed":true,"details":"ok"}]}`)
		fmt.Println(`{"type":"encoding_complete","input_file":"source.mkv","output_file":"encoded.mkv","original_size":1000,"encoded_size":800,"video_stream":"AV1","audio_stream":"Opus","average_speed":3.1,"output_path":"/tmp/out.mkv","duration_seconds":98,"size_reduction_percent":-20}`)
		fmt.Println(`{"type":"operation_complete","message":"Encoding finished successfully"}`)
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
