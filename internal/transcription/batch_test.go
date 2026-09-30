package transcription

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscribeBatchWithStubProcesses(t *testing.T) {
	bin := t.TempDir()
	ffmpeg := `#!/bin/sh
for arg do last="$arg"; done
printf 'wave' > "$last"
`
	uvx := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = '--output-dir' ]; then
    shift
    printf '1\n00:00:01,000 --> 00:00:03,500\nHello.\n' > "$1/audio.srt"
    printf '{}' > "$1/audio.json"
  fi
  shift
done
`
	for name, script := range map[string]string{"ffmpeg": ffmpeg, "uvx": uvx} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := New(Params{}, nil)
	dir := t.TempDir()
	reqs := []TranscribeRequest{
		{InputPath: "first.mkv", OutputDir: filepath.Join(dir, "one"), Language: "en"},
		{InputPath: "second.mkv", OutputDir: filepath.Join(dir, "two"), Language: "fr", AudioIndex: 1},
	}
	var phases []Phase
	results, err := s.TranscribeBatch(context.Background(), reqs, func(p Phase, elapsed time.Duration) { phases = append(phases, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Segments != 1 || results[1].Duration != 3.5 || results[0].SRTPath != filepath.Join(dir, "one", "audio.srt") {
		t.Fatalf("results: %+v", results)
	}
	if len(phases) != 4 || phases[0] != PhaseExtract || phases[1] != PhaseExtract || phases[2] != PhaseTranscribe || phases[3] != PhaseTranscribe {
		t.Fatalf("progress phases: %v", phases)
	}
	if _, err := os.Stat(filepath.Join(dir, "two", "audio.wav")); err != nil {
		t.Fatal(err)
	}
	single, err := s.Transcribe(context.Background(), reqs[0])
	if err != nil || single.Segments != 1 {
		t.Fatalf("single: %+v %v", single, err)
	}
}

func TestTranscribeBatchProcessFailures(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := New(Params{}, nil)
	req := TranscribeRequest{InputPath: "input.mkv", OutputDir: filepath.Join(t.TempDir(), "out"), Language: "en"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.TranscribeBatch(ctx, []TranscribeRequest{req}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := s.TranscribeBatch(context.Background(), []TranscribeRequest{req}); err == nil || !strings.Contains(err.Error(), "ffmpeg audio extraction") {
		t.Fatalf("ffmpeg error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte("#!/bin/sh\nfor arg do last=\"$arg\"; done\nprintf wave > \"$last\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "uvx"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TranscribeBatch(context.Background(), []TranscribeRequest{req}); err == nil || !strings.Contains(err.Error(), "whisperx transcription") {
		t.Fatalf("uvx error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "uvx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TranscribeBatch(context.Background(), []TranscribeRequest{req}); err == nil || !strings.Contains(err.Error(), "srt output not found") {
		t.Fatalf("missing srt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(req.OutputDir, "audio.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TranscribeBatch(context.Background(), []TranscribeRequest{req}); err == nil || !strings.Contains(err.Error(), "json output not found") {
		t.Fatalf("missing json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(req.OutputDir, "audio.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(req.OutputDir, "audio.srt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(req.OutputDir, "audio.srt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TranscribeBatch(context.Background(), []TranscribeRequest{req}); err == nil || !strings.Contains(err.Error(), "analyze srt") {
		t.Fatalf("bad srt: %v", err)
	}
}
