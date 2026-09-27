package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHelpAndVersion(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	for _, cmd := range []string{"help", "--help", "version", "-v"} {
		os.Args = []string{"reel", cmd}
		main()
	}
}

func TestEncodeConfigReachesProbeWithoutEncoding(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "invalid.mkv")
	if err := os.WriteFile(source, []byte("not a movie"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--input", source, "--output", filepath.Join(dir, "out"), "--log-dir", filepath.Join(dir, "logs"), "--no-log", "--quality-mode", "crf", "--crf", "25", "--preset", "8", "--level-of-parallelism", "2", "--disable-autocrop", "--grain-treatment", "off", "--probe-metric", "cvvdp", "--keep-workdir", "--color", "auto"}
	// Per-file probe errors are reported and the batch returns without an output.
	if err := runEncode(args); err != nil {
		t.Fatalf("batch returned an unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "invalid.mkv")); !os.IsNotExist(err) {
		t.Fatalf("invalid input produced an output: %v", err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runEncode([]string{"-i", empty, "-o", filepath.Join(dir, "out"), "--no-log"}); err == nil || !strings.Contains(err.Error(), "no video files found") {
		t.Fatalf("empty directory: %v", err)
	}
}
