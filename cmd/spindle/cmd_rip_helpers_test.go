package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/makemkv"
)

func TestRipTitleDisplayHelpers(t *testing.T) {
	if got := availableTitleIDs([]makemkv.TitleInfo{{ID: 3}, {ID: 17}}); got != "3, 17" {
		t.Fatalf("availableTitleIDs = %q", got)
	}
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{0, "0:00:00"},
		{59, "0:00:59"},
		{3661, "1:01:01"},
	} {
		if got := formatTitleDuration(tc.seconds); got != tc.want {
			t.Errorf("formatTitleDuration(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestNewMKVNameOnlyReportsFreshFiles(t *testing.T) {
	dir := t.TempDir()
	if got := newMKVName(dir, snapshotMKVNames(dir)); got != "" {
		t.Fatalf("no new files = %q", got)
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old.mkv")
	before := snapshotMKVNames(dir)
	write("z.MKV")
	write("a.mkv")
	write("notes.txt")
	if err := os.Mkdir(filepath.Join(dir, "directory.mkv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := newMKVName(dir, before); got != "a.mkv" {
		t.Fatalf("newMKVName = %q, want lexicographically first new MKV", got)
	}
	if got := newMKVName(dir, snapshotMKVNames(dir)); got != "" {
		t.Fatalf("unchanged directory = %q", got)
	}
	if got := newMKVName(filepath.Join(dir, "missing"), nil); got != "" {
		t.Fatalf("missing directory = %q", got)
	}
}
