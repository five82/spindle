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
	output := "CINFO:32,0,\"ABCDEF0123456789\"\n"
	capture := &captureExec{output: []byte(output)}
	scanner := disc.NewScannerWithExecutor("makemkvcon", capture)
	if _, err := scanner.Scan(context.Background(), "/dev/sr0"); err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(capture.lastArgs) == 0 {
		t.Fatalf("expected arguments to be recorded")
	}
	found := false
	for _, arg := range capture.lastArgs {
		if arg == "dev:/dev/sr0" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dev:/dev/sr0 in args, got %#v", capture.lastArgs)
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
