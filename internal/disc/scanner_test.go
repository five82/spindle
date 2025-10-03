package disc_test

import (
	"context"
	"strings"
	"testing"

	"spindle/internal/disc"
)

type stubExec struct {
	output []byte
	err    error
}

func (s stubExec) Run(ctx context.Context, binary string, args []string) ([]byte, error) {
	return s.output, s.err
}

type captureExec struct {
	output   []byte
	lastArgs []string
}

func (c *captureExec) Run(ctx context.Context, binary string, args []string) ([]byte, error) {
	c.lastArgs = append([]string(nil), args...)
	return c.output, nil
}

func TestScannerParsesFingerprint(t *testing.T) {
	output := `MSG:1005,0,1,"start"
CINFO:32,0,"0123456789ABCDEF0123456789ABCDEF"
TINFO:0,2,0,"Main Feature"
TINFO:0,9,0,"1:39:03"
`
	scanner := disc.NewScannerWithExecutor("makemkvcon", stubExec{output: []byte(output)})
	result, err := scanner.Scan(context.Background(), "disc:0")
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.Fingerprint != "0123456789ABCDEF0123456789ABCDEF" {
		t.Fatalf("unexpected fingerprint: %s", result.Fingerprint)
	}
	if len(result.Titles) != 1 || result.Titles[0].ID != 0 {
		t.Fatalf("unexpected titles: %#v", result.Titles)
	}
	if result.Titles[0].Duration != 5943 {
		t.Fatalf("unexpected duration: %d", result.Titles[0].Duration)
	}
}

func TestScannerAllowsMissingFingerprint(t *testing.T) {
	output := "TINFO:0,2,0,\"Feature\"\n"
	scanner := disc.NewScannerWithExecutor("makemkvcon", stubExec{output: []byte(output)})
	result, err := scanner.Scan(context.Background(), "disc:0")
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.Fingerprint != "" {
		t.Fatalf("expected empty fingerprint, got %q", result.Fingerprint)
	}
}

func TestScannerNeedsBinary(t *testing.T) {
	scanner := disc.NewScannerWithExecutor("", stubExec{})
	if _, err := scanner.Scan(context.Background(), "disc:0"); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestScannerNormalizesDevicePath(t *testing.T) {
	// Include a title so bd_info doesn't get called (which would overwrite lastArgs)
	output := `CINFO:32,0,"ABCDEF0123456789"
TINFO:0,2,0,"Movie Title"
`
	capture := &captureExec{output: []byte(output)}
	scanner := disc.NewScannerWithExecutor("makemkvcon", capture)
	if _, err := scanner.Scan(context.Background(), "/dev/sr0"); err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(capture.lastArgs) == 0 {
		t.Fatalf("expected arguments to be recorded")
	}
	// The target device should be normalized to dev:/dev/sr0 format
	// This should be the 4th argument in the command: ["-r", "--cache=1", "info", target, "--robot"]
	if len(capture.lastArgs) < 4 {
		t.Fatalf("expected at least 4 arguments, got %d", len(capture.lastArgs))
	}
	targetArg := capture.lastArgs[3]
	if targetArg != "dev:/dev/sr0" {
		t.Fatalf("expected device argument to be dev:/dev/sr0, got %q", targetArg)
	}
}

type failingExitError struct {
	code   int
	stderr []byte
}

func (f failingExitError) Error() string  { return "makemkv failed" }
func (f failingExitError) ExitCode() int  { return f.code }
func (f failingExitError) Stderr() []byte { return f.stderr }

func TestScannerIncludesMakemkvDetailsOnFailure(t *testing.T) {
	stdout := []byte("MSG:5010,0,1,\"MakeMKV failed to open disc\"")
	err := failingExitError{code: 10, stderr: []byte("additional context")}
	scanner := disc.NewScannerWithExecutor("makemkvcon", stubExec{output: stdout, err: err})
	_, scanErr := scanner.Scan(context.Background(), "disc:0")
	if scanErr == nil {
		t.Fatalf("expected error")
	}
	msg := scanErr.Error()
	if !strings.Contains(msg, "exit status 10") {
		t.Fatalf("expected exit status in message, got %q", msg)
	}
	if !strings.Contains(msg, "MakeMKV failed to open disc") {
		t.Fatalf("expected parsed MakeMKV message, got %q", msg)
	}
}

// mockExecutor handles both makemkv and bd_info commands
type mockExecutor struct {
	responses map[string][]byte
	errors    map[string]error
	lastCmd   map[string][]string
}

func (m *mockExecutor) Run(ctx context.Context, binary string, args []string) ([]byte, error) {
	key := binary
	if len(args) > 0 {
		key += " " + strings.Join(args, " ")
	}

	if m.lastCmd == nil {
		m.lastCmd = make(map[string][]string)
	}
	m.lastCmd[binary] = args

	if err, exists := m.errors[key]; exists {
		return nil, err
	}
	if output, exists := m.responses[key]; exists {
		return output, nil
	}

	// Default empty response for unknown commands
	return []byte{}, nil
}

func TestScannerUsesBDInfoForMissingTitle(t *testing.T) {
	// Mock MakeMKV output with empty title
	makemkvOutput := `MSG:1005,0,1,"start"
CINFO:32,0,"0123456789ABCDEF0123456789ABCDEF"
TINFO:0,2,0,""
TINFO:0,9,0,"1:39:03"
`

	// Mock bd_info output
	bdInfoOutput := `Using libbluray version 1.3.4
Volume Identifier   : 00000095_50_FIRST_DATES
BluRay detected     : yes
AACS detected       : yes
`

	executor := &mockExecutor{
		responses: map[string][]byte{
			"makemkvcon -r --cache=1 info dev:/dev/sr0 --robot": []byte(makemkvOutput),
			"bd_info /dev/sr0": []byte(bdInfoOutput),
		},
	}

	scanner := disc.NewScannerWithExecutor("makemkvcon", executor)
	result, err := scanner.Scan(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	// Verify bd_info was called
	if _, called := executor.lastCmd["bd_info"]; !called {
		t.Fatal("expected bd_info to be called")
	}

	// Verify bd_info data was parsed
	if result.BDInfo == nil {
		t.Fatal("expected bd_info data to be populated")
	}

	if result.BDInfo.VolumeIdentifier != "00000095_50_FIRST_DATES" {
		t.Fatalf("unexpected volume identifier: %s", result.BDInfo.VolumeIdentifier)
	}

	if !result.BDInfo.IsBluRay {
		t.Fatal("expected BluRay detection")
	}

	if !result.BDInfo.HasAACS {
		t.Fatal("expected AACS detection")
	}

	// Verify title was populated from bd_info disc name
	expectedTitle := "50 FIRST DATES"
	if len(result.Titles) == 0 || result.Titles[0].Name != expectedTitle {
		t.Fatalf("expected title to be populated from bd_info, got: %v", result.Titles)
	}
}

func TestScannerUsesBDInfoForGenericTitle(t *testing.T) {
	// Mock MakeMKV output with generic title
	makemkvOutput := `MSG:1005,0,1,"start"
CINFO:2,0,"LOGICAL_VOLUME_ID"
CINFO:30,0,"LOGICAL_VOLUME_ID"
CINFO:32,0,"0123456789ABCDEF0123456789ABCDEF"
TINFO:0,2,0,"LOGICAL_VOLUME_ID"
TINFO:0,9,0,"1:39:03"
`

	// Mock bd_info output
	bdInfoOutput := `Using libbluray version 1.3.4
Volume Identifier   : 00000095_50_FIRST_DATES
BluRay detected     : yes
AACS detected       : yes
`

	executor := &mockExecutor{
		responses: map[string][]byte{
			"makemkvcon -r --cache=1 info dev:/dev/sr0 --robot": []byte(makemkvOutput),
			"bd_info /dev/sr0": []byte(bdInfoOutput),
		},
	}

	scanner := disc.NewScannerWithExecutor("makemkvcon", executor)
	result, err := scanner.Scan(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	// Verify bd_info was called because title was generic
	if _, called := executor.lastCmd["bd_info"]; !called {
		t.Fatal("expected bd_info to be called for generic title")
	}

	// Verify bd_info data was parsed
	if result.BDInfo == nil {
		t.Fatal("expected bd_info data to be populated")
	}

	// Verify title was replaced with bd_info disc name
	expectedTitle := "50 FIRST DATES"
	if len(result.Titles) == 0 || result.Titles[0].Name != expectedTitle {
		t.Fatalf("expected generic title to be replaced with bd_info title, got: %v", result.Titles)
	}
}

func TestScannerHandlesBDInfoUnavailable(t *testing.T) {
	// Mock MakeMKV output with empty title
	makemkvOutput := `MSG:1005,0,1,"start"
CINFO:32,0,"0123456789ABCDEF0123456789ABCDEF"
TINFO:0,2,0,""
TINFO:0,9,0,"1:39:03"
`

	executor := &mockExecutor{
		responses: map[string][]byte{
			"makemkvcon -r --cache=1 info dev:/dev/sr0 --robot": []byte(makemkvOutput),
		},
		errors: map[string]error{
			"bd_info /dev/sr0": &failingExitError{code: 1, stderr: []byte("bd_info not found")},
		},
	}

	scanner := disc.NewScannerWithExecutor("makemkvcon", executor)
	result, err := scanner.Scan(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	// Verify bd_info was attempted
	if _, called := executor.lastCmd["bd_info"]; !called {
		t.Fatal("expected bd_info to be called")
	}

	// Verify scan still succeeded despite bd_info failure
	if result == nil {
		t.Fatal("expected scan result even when bd_info fails")
	}

	// Should not have bd_info data when command fails
	if result.BDInfo != nil {
		t.Fatal("expected no bd_info data when command fails")
	}
}

func TestExtractDiscNameFromVolumeID(t *testing.T) {
	testCases := []struct {
		volumeID string
		expected string
	}{
		{"00000095_50_FIRST_DATES", "50 FIRST DATES"},
		{"12345_MOVIE_TITLE", "MOVIE TITLE"},
		{"SHOW_S1_DISC_1", "SHOW"},
		{"00000042_TV_SERIES", "TV SERIES"},
		{"SIMPLE_TITLE", "SIMPLE TITLE"},
		{"", ""},
		{"12345", ""}, // Numbers only should result in empty
	}

	for _, tc := range testCases {
		result := disc.ExtractDiscNameFromVolumeID(tc.volumeID)
		if result != tc.expected {
			t.Errorf("ExtractDiscNameFromVolumeID(%q) = %q, expected %q",
				tc.volumeID, result, tc.expected)
		}
	}
}

func TestIsGenericLabel(t *testing.T) {
	testCases := []struct {
		label    string
		expected bool
	}{
		{"LOGICAL_VOLUME_ID", true},
		{"logical_volume_id", true}, // case insensitive
		{"DVD_VIDEO", true},
		{"BLURAY", true},
		{"BD_ROM", true},
		{"UNTITLED", true},
		{"12345", true},           // numbers only
		{"ABC", true},             // short alphanumeric
		{"", true},                // empty
		{"50_FIRST_DATES", false}, // real title
		{"Movie Title", false},    // normal title
		{"THE_MATRIX", false},     // normal title with underscores
	}

	for _, tc := range testCases {
		result := disc.IsGenericLabel(tc.label)
		if result != tc.expected {
			t.Errorf("IsGenericLabel(%q) = %v, expected %v",
				tc.label, result, tc.expected)
		}
	}
}
