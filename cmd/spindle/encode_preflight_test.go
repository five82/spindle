package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestEncodeCommandChecksInputAndOutputBeforeStartingReel(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "absent.sock")
	cmd := newEncodeCmd()
	if err := cmd.RunE(cmd, []string{filepath.Join(dir, "missing.mkv")}); err == nil || !strings.Contains(err.Error(), "input file") {
		t.Fatalf("missing input: %v", err)
	}
	input := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(input, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "blocker")
	if err := os.WriteFile(output, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("output-dir", filepath.Join(output, "subdir")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{input}); err == nil || !strings.Contains(err.Error(), "create output dir") {
		t.Fatalf("blocked output: %v", err)
	}
	if err := cmd.Flags().Set("output-dir", filepath.Join(dir, "encoded")); err != nil {
		t.Fatal(err)
	}
	// A corrupt file fails inside Reel without touching the source or reporting success.
	if err := cmd.RunE(cmd, []string{input}); err == nil || !strings.Contains(err.Error(), "encode failed") {
		t.Fatalf("invalid input: %v", err)
	}
	if data, err := os.ReadFile(input); err != nil || string(data) != "source" {
		t.Fatalf("source changed: %q %v", data, err)
	}
}
