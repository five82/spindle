package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestCacheRipGuardsAndProbe(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "absent.sock")
	cmd := newCacheRipCmd()
	if err := cmd.Flags().Set("title", "1"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("choose", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("conflicting options: %v", err)
	}
	if err := cmd.Flags().Set("choose", "false"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "no device specified") {
		t.Fatalf("missing device: %v", err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lsblk"), []byte("#!/bin/sh\necho '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"Disc\",\"fstype\":\"udf\",\"mountpoint\":null}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := cmd.RunE(cmd, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "disc not mounted") {
		t.Fatalf("unmounted disc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "lsblk"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "probe disc") {
		t.Fatalf("failed probe: %v", err)
	}
}

func TestConfigValidateRejectsInvalidConfig(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = &config.Config{}
	cmd := newConfigValidateCmd()
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "config invalid") {
		t.Fatalf("invalid config: %v", err)
	}
}

func TestIdentifyRequiresDevice(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = nil
	cmd := newIdentifyCmd()
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "no device specified") {
		t.Fatalf("missing device: %v", err)
	}
}
