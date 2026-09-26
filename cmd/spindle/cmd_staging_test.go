package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/config"
)

func TestStagingListAndClean(t *testing.T) {
	oldCfg := cfg
	dir := t.TempDir()
	cfg = &config.Config{}
	cfg.Paths.StagingDir = dir
	t.Cleanup(func() { cfg = oldCfg })

	cmd := newStagingCmd()
	list, _, err := cmd.Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if out := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "No staging directories") {
		t.Fatalf("empty list = %q", out)
	}
	entry := filepath.Join(dir, "orphan-fingerprint")
	if err := os.Mkdir(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "video.mkv"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(entry, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if out := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "orphan-finge") || !strings.Contains(out, "1 directories, 7 B total") {
		t.Fatalf("populated list = %q", out)
	}
	if err := list.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if out := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, `"Name": "orphan-fingerprint"`) {
		t.Fatalf("JSON list = %q", out)
	}

	clean, _, err := cmd.Find([]string{"clean"})
	if err != nil {
		t.Fatal(err)
	}
	if err := clean.Flags().Set("all", "true"); err != nil {
		t.Fatal(err)
	}
	if err := clean.RunE(clean, nil); err == nil || !strings.Contains(err.Error(), "confirmation required") {
		t.Fatalf("clean without --yes = %v", err)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("unconfirmed clean removed directory: %v", err)
	}
	if err := clean.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	if out := captureStdout(t, func() {
		if err := clean.RunE(clean, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "Removed 1 staging directories") {
		t.Fatalf("clean output = %q", out)
	}
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Fatalf("clean left orphan directory: %v", err)
	}
}

func TestStagingListMissingDirectory(t *testing.T) {
	oldCfg := cfg
	cfg = &config.Config{}
	cfg.Paths.StagingDir = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { cfg = oldCfg })
	list := newStagingListCmd()
	if err := list.RunE(list, nil); err == nil || !strings.Contains(err.Error(), "read staging dir") {
		t.Fatalf("missing staging dir = %v", err)
	}
}
