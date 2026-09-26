package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestDebugCropAndCommentaryCommandsWithStubProbes(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })
	cfg = &config.Config{}
	bin := t.TempDir()
	probe := `#!/bin/sh
if [ "$PROBE_MODE" = fail ]; then exit 1; fi
if [ "$PROBE_MODE" = empty ]; then
  printf '%s\n' '{"streams":[],"format":{"duration":"120"}}'
elif [ "$PROBE_MODE" = audio ]; then
  printf '%s\n' '{"streams":[{"index":0,"codec_type":"audio","codec_name":"aac","channels":2,"channel_layout":"stereo","tags":{"language":"eng","title":"Main"}}],"format":{"duration":"120"}}'
else
  printf '%s\n' '{"streams":[{"index":0,"codec_type":"video","width":1920,"height":1080,"color_transfer":"smpte2084"}],"format":{"duration":"120"}}'
fi
`
	ffmpeg := `#!/bin/sh
if [ "$FFMPEG_MODE" = fail ]; then echo 'decode failed' >&2; exit 1; fi
echo 'crop=1920:800:0:140 crop=1920:800:0:140 crop=1920:1080:0:0' >&2
`
	for name, script := range map[string]string{"ffprobe": probe, "ffmpeg": ffmpeg} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	file := filepath.Join(bin, "movie.mkv")
	if err := os.WriteFile(file, []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newDebugCropCmd()
	got := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{file}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Crop Detection Results", "1920x1080", "crop=1920:800:0:140", "Black bars detected", "Multiple ratios:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	t.Setenv("FFMPEG_MODE", "fail")
	if _, err := detectDebugCrop(context.Background(), file); err == nil || !strings.Contains(err.Error(), "decode failed") {
		t.Fatalf("ffmpeg failure: %v", err)
	}
	t.Setenv("PROBE_MODE", "audio")
	if _, err := detectDebugCrop(context.Background(), file); err == nil || !strings.Contains(err.Error(), "no video") {
		t.Fatalf("no video: %v", err)
	}
	commentary := newDebugCommentaryCmd()
	got = captureStdout(t, func() {
		if err := commentary.RunE(commentary, []string{file}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "Only one audio stream") || !strings.Contains(got, "lang=eng") {
		t.Fatalf("commentary: %s", got)
	}
	t.Setenv("PROBE_MODE", "empty")
	got = captureStdout(t, func() {
		if err := commentary.RunE(commentary, []string{file}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(got, "No audio streams found") {
		t.Fatalf("empty audio: %s", got)
	}
	t.Setenv("PROBE_MODE", "fail")
	if _, err := detectDebugCrop(context.Background(), file); err == nil || !strings.Contains(err.Error(), "ffprobe") {
		t.Fatalf("probe failure: %v", err)
	}
	if err := commentary.RunE(commentary, []string{file}); err == nil || !strings.Contains(err.Error(), "ffprobe") {
		t.Fatalf("commentary probe failure: %v", err)
	}
}
