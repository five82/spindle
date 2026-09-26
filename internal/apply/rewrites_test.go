package apply

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentaryDispositionRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "encoded.mkv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	installTool(t, "ffmpeg", "printf '%s\\n' \"$@\" > \"$FFMPEG_ARGS\"\nfor last do :; done\nprintf labeled > \"$last\"\n")
	argsPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("FFMPEG_ARGS", argsPath)
	if err := applyCommentaryDisposition(context.Background(), slog.Default(), path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatalf("empty targets ran ffmpeg: %v", err)
	}
	if err := applyCommentaryDisposition(context.Background(), slog.Default(), path, []commentaryTarget{{Index: 1, Title: "Director"}}); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "-disposition:a:1\ncomment\n") || !strings.Contains(string(args), "title=Director (Commentary)") {
		t.Fatalf("args: %s", args)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "labeled" {
		t.Fatalf("file: %q, %v", data, err)
	}
}

func TestCommentaryDispositionFailedRemux(t *testing.T) {
	path := filepath.Join(t.TempDir(), "encoded.mkv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	installTool(t, "ffmpeg", "for last do :; done\necho partial > \"$last\"\nexit 2\n")
	err := applyCommentaryDisposition(context.Background(), slog.Default(), path, []commentaryTarget{{Index: 0}})
	if err == nil || !strings.Contains(err.Error(), "ffmpeg disposition") {
		t.Fatalf("error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("file: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".disposition-encoded.mkv")); !os.IsNotExist(err) {
		t.Fatalf("temp: %v", err)
	}
}

func TestSubtitleMuxRewriteAndIdentify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(path, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	installTool(t, "mkvmerge", "if [ \"$1\" = --identify ]; then printf 'Track ID 0: video\\nTrack ID 1: subtitles\\n'; exit 0; fi\n[ \"$1\" = -o ] || exit 2\nprintf '%s\\n' \"$@\" > \"$MKV_ARGS\"\nprintf muxed > \"$2\"\n")
	args := filepath.Join(dir, "args")
	t.Setenv("MKV_ARGS", args)
	if !MKVHasSubtitleTrack(context.Background(), path) {
		t.Fatal("subtitle track not detected")
	}
	output, err := MuxSubtitleTrack(context.Background(), MuxRequest{VideoPath: path, Track: MuxTrack{Path: "sub.srt", Language: "eng"}, ReplaceExisting: true})
	if err != nil || output != path {
		t.Fatalf("output = %s, err = %v", output, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "muxed" {
		t.Fatalf("file: %q, %v", data, err)
	}
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), "--no-subtitles") || !strings.Contains(string(recorded), "sub.srt") {
		t.Fatalf("args: %s", recorded)
	}
	out, err := muxDisplaySubtitle(context.Background(), slog.Default(), path, "sub.srt", "main", "eng")
	if err != nil || out != filepath.Join(dir, "source.subtitled.mkv") {
		t.Fatalf("display mux = %s, %v", out, err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestSubtitleMuxFailureAndInvalidInput(t *testing.T) {
	if _, err := MuxSubtitleTrack(context.Background(), MuxRequest{}); err == nil {
		t.Fatal("expected missing video error")
	}
	path := filepath.Join(t.TempDir(), "video.mkv")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	installTool(t, "mkvmerge", "if [ \"$1\" = --identify ]; then exit 1; fi\nprintf partial > \"$2\"\nexit 1\n")
	if MKVHasSubtitleTrack(context.Background(), path) {
		t.Fatal("unexpected subtitle track")
	}
	if _, err := muxDisplaySubtitle(context.Background(), slog.Default(), path, "sub.srt", "main", "eng"); err == nil || !strings.Contains(err.Error(), "mux subtitles main") {
		t.Fatalf("error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "video.subtitled.tmp.mkv")); !os.IsNotExist(err) {
		t.Fatalf("partial mux remains: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("source: %q, %v", data, err)
	}
}
