package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/fingerprint"
	"github.com/five82/spindle/internal/ripcache"
)

func TestCacheRipPreflightAndExistingFingerprint(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}, RipCache: config.RipCacheConfig{MaxGiB: 1}}
	flagSocket = filepath.Join(dir, "missing.sock")
	cmd := newCacheRipCmd()
	if err := cmd.Flags().Set("title", "1"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("choose", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("conflicting flags: %v", err)
	}
	if err := cmd.Flags().Set("choose", "false"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "no device specified") {
		t.Fatalf("missing device: %v", err)
	}
	if err := cmd.RunE(cmd, []string{"/dev/does-not-exist"}); err == nil || !strings.Contains(err.Error(), "probe disc") {
		t.Fatalf("failed probe: %v", err)
	}

	// Stub only lsblk; a real temporary directory supplies the disc content
	// for the production fingerprinting path.
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "lsblk")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"DISC\"}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := cmd.RunE(cmd, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("unmounted disc: %v", err)
	}
	mount := filepath.Join(dir, "mount")
	if err := os.Mkdir(mount, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mount, "disc.txt"), []byte("disc bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	fp, err := fingerprint.Generate(mount, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ripcache.New(cfg.RipCacheDir(), 1).WriteMetadata(fp, ripcache.EntryMetadata{Fingerprint: fp, DiscTitle: "DISC"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("#!/bin/sh\necho '{\"blockdevices\":[{\"name\":\"sr0\",\"label\":\"DISC\",\"mountpoint\":%q}]}'\n", mount)), 0o755); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"/dev/sr0"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Disc already cached") {
		t.Fatalf("cached disc: %s", got)
	}
	if err := ripcache.New(cfg.RipCacheDir(), 1).Remove(fp); err != nil {
		t.Fatal(err)
	}
	// An uncached disc reaches one-shot identification. The empty MakeMKV
	// boundary fails safely and must not leave a temporary queue item.
	cfg.MakeMKV.OpticalDrive = "/dev/sr0"
	if err := cmd.RunE(cmd, []string{"/dev/sr0"}); err == nil || !strings.Contains(err.Error(), "identification:") {
		t.Fatalf("identification failure: %v", err)
	}
}
