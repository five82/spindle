package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestStandaloneSubtitleCommandReportsFailedAdoption(t *testing.T) {
	oldCfg, oldQuiet, oldVerbose := cfg, flagQuiet, flagVerbose
	t.Cleanup(func() { cfg, flagQuiet, flagVerbose = oldCfg, oldQuiet, oldVerbose })
	cfg = &config.Config{Subtitles: config.SubtitlesConfig{OpenSubtitlesAPIKey: ""}}
	flagQuiet = false
	flagVerbose = false
	movie := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(movie, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newGensubtitleCmd()
	if err := cmd.Flags().Set("tmdb-id", "55"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("external", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{movie}); err == nil || !strings.Contains(err.Error(), "1 of 1 file(s) got no subtitles") {
		t.Fatalf("subtitle: %v", err)
	}
}

func TestStandaloneSubtitleBatchAudioPreflight(t *testing.T) {
	oldCfg, oldQuiet := cfg, flagQuiet
	t.Cleanup(func() { cfg, flagQuiet = oldCfg, oldQuiet })
	cfg = &config.Config{}
	flagQuiet = false
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "one.mkv"), filepath.Join(dir, "two.mkv")}
	for _, file := range files {
		if err := os.WriteFile(file, []byte("video"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := newGensubtitleCmd()
	if err := cmd.Flags().Set("tmdb-id", "55"); err != nil {
		t.Fatal(err)
	}
	// Both files must resolve their identity before batch audio selection.
	t.Setenv("PATH", t.TempDir())
	if err := cmd.RunE(cmd, files); err == nil || !strings.Contains(err.Error(), "select primary audio") {
		t.Fatalf("batch: %v", err)
	}
}
