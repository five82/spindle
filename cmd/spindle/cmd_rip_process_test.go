package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestRipCommandPreflight(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "missing.sock")
	cmd := newRipCmd()
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "select titles") {
		t.Fatalf("selection: %v", err)
	}
	if err := cmd.Flags().Set("all", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("title", "1"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("conflicting selection: %v", err)
	}
	if err := cmd.Flags().Set("all", "false"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "no device") {
		t.Fatalf("missing drive: %v", err)
	}
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("output-dir", filepath.Join(blocker, "child")); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"disc:0"}); err == nil || !strings.Contains(err.Error(), "create output dir") {
		t.Fatalf("output directory: %v", err)
	}
}

func TestRipCommandWithStubMakeMKV(t *testing.T) {
	oldCfg, oldSocket, oldQuiet := cfg, flagSocket, flagQuiet
	t.Cleanup(func() { cfg, flagSocket, flagQuiet = oldCfg, oldSocket, oldQuiet })
	bin := t.TempDir()
	script := `#!/bin/sh
case "$3" in
info)
  printf '%s\n' 'CINFO:2,0,"Fixture Disc"' 'TINFO:1,2,0,"Feature"' 'TINFO:1,9,0,"1:30:00"' 'TINFO:2,2,0,"Bonus"' 'TINFO:2,9,0,"0:10:00"'
  ;;
mkv)
  if [ "$RIP_MODE" = fail ]; then exit 1; fi
  printf 'mkv' > "$6/title-$5.mkv"
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "makemkvcon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: bin}, MakeMKV: config.MakeMKVConfig{OpticalDrive: "disc:0", InfoTimeout: 5, RipTimeout: 5}}
	flagSocket = filepath.Join(bin, "absent.sock")
	flagQuiet = false
	out := filepath.Join(bin, "rips")
	cmd := newRipCmd()
	if err := cmd.Flags().Set("title", "1,2"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("output-dir", out); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Ripped 2 title(s)") || !strings.Contains(got, "Phase 2/2") {
		t.Fatalf("rip output: %s", got)
	}
	for _, name := range []string{"title-1.mkv", "title-2.mkv"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("repeat rip: %v", err)
	}
	missing := newRipCmd()
	if err := missing.Flags().Set("title", "8"); err != nil {
		t.Fatal(err)
	}
	if err := missing.Flags().Set("output-dir", out); err != nil {
		t.Fatal(err)
	}
	if err := missing.RunE(missing, nil); err == nil || !strings.Contains(err.Error(), "available: 1, 2") {
		t.Fatalf("missing title: %v", err)
	}
	all := newRipCmd()
	if err := all.Flags().Set("all", "true"); err != nil {
		t.Fatal(err)
	}
	if err := all.Flags().Set("output-dir", filepath.Join(bin, "all")); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := all.RunE(all, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Setenv("RIP_MODE", "fail")
	if err := all.RunE(all, nil); err == nil || !strings.Contains(err.Error(), "rip title 1") {
		t.Fatalf("failed rip: %v", err)
	}
}
