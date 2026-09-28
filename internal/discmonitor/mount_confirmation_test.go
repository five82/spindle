//go:build linux

package discmonitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoMountNeedsMountTableConfirmation(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "unmounted")
	if err := os.WriteFile(filepath.Join(bin, "mount"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mp, err := autoMount(context.Background(), marker)
	if mp != "" || err == nil || !strings.Contains(err.Error(), "mount point not found") {
		t.Fatalf("mount returned success without mount table entry: %q %v", mp, err)
	}
}
