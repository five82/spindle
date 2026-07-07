package transcription

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

const sampleSRT = `1
00:00:01,000 --> 00:00:04,000
Hello, world.

2
00:00:05,000 --> 00:00:08,000
This is a test.

3
00:00:09,000 --> 00:00:12,000
Goodbye.
`

func TestAnalyzeSRT(t *testing.T) {
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(srtPath, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}

	segments, duration, err := analyzeSRT(srtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments != 3 {
		t.Errorf("expected 3 segments, got %d", segments)
	}
	if duration != 12.0 {
		t.Errorf("expected duration 12.0s, got %.1f", duration)
	}
}

func TestAnalyzeSRTLong(t *testing.T) {
	srt := `1
00:00:01,000 --> 00:00:04,000
Hello.

2
01:38:10,000 --> 01:38:12,456
Goodbye.
`
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "long.srt")
	if err := os.WriteFile(srtPath, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}

	segments, duration, err := analyzeSRT(srtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments != 2 {
		t.Errorf("expected 2 segments, got %d", segments)
	}
	expected := 1*3600 + 38*60 + 12 + 0.456
	if duration < expected-0.001 || duration > expected+0.001 {
		t.Errorf("expected duration %.3f, got %.3f", expected, duration)
	}
}

func TestAnalyzeSRTEmpty(t *testing.T) {
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(srtPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	segments, duration, err := analyzeSRT(srtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if segments != 0 {
		t.Errorf("expected 0 segments, got %d", segments)
	}
	if duration != 0 {
		t.Errorf("expected 0 duration, got %.1f", duration)
	}
}

func TestSelectPrimaryAudioTrack(t *testing.T) {
	origInspect := inspectMedia
	t.Cleanup(func() { inspectMedia = origInspect })

	inspectMedia = func(ctx context.Context, binary, path string) (*ffprobe.Result, error) {
		if path == "" {
			return nil, fmt.Errorf("missing path")
		}
		return &ffprobe.Result{Streams: []ffprobe.Stream{
			{Index: 0, CodecType: "video", CodecName: "h264"},
			{Index: 1, CodecType: "audio", CodecName: "ac3", Channels: 2, Tags: map[string]string{"language": "ita"}, Disposition: map[string]int{}},
			{Index: 2, CodecType: "audio", CodecName: "ac3", Channels: 6, Tags: map[string]string{"language": "eng", "title": "English 5.1"}, Disposition: map[string]int{"default": 1}},
		}}, nil
	}

	svc := New(Params{Model: "large-v3", VADMethod: "silero"}, nil)
	selected, err := svc.SelectPrimaryAudioTrack(context.Background(), "/tmp/input.mkv", "en")
	if err != nil {
		t.Fatalf("SelectPrimaryAudioTrack() error = %v", err)
	}
	if selected.Index != 1 {
		t.Fatalf("Index = %d, want 1", selected.Index)
	}
	if selected.Language != "en" {
		t.Fatalf("Language = %q, want en", selected.Language)
	}
	if selected.Label == "" {
		t.Fatal("expected non-empty label")
	}
}

func TestSelectPrimaryAudioTrackFallsBackLanguage(t *testing.T) {
	origInspect := inspectMedia
	t.Cleanup(func() { inspectMedia = origInspect })

	inspectMedia = func(ctx context.Context, binary, path string) (*ffprobe.Result, error) {
		return &ffprobe.Result{Streams: []ffprobe.Stream{
			{Index: 0, CodecType: "audio", CodecName: "ac3", Channels: 2, Tags: map[string]string{}, Disposition: map[string]int{"default": 1}},
		}}, nil
	}

	svc := New(Params{Model: "large-v3", VADMethod: "silero"}, nil)
	selected, err := svc.SelectPrimaryAudioTrack(context.Background(), "/tmp/input.mkv", "english")
	if err != nil {
		t.Fatalf("SelectPrimaryAudioTrack() error = %v", err)
	}
	if selected.Index != 0 {
		t.Fatalf("Index = %d, want 0", selected.Index)
	}
	if selected.Language != "en" {
		t.Fatalf("Language = %q, want en", selected.Language)
	}
}

func TestBuildWhisperXInvocation(t *testing.T) {
	svc := New(Params{Model: "large-v3", CUDAEnabled: true, VADMethod: "pyannote", HFToken: "hf-token"}, nil)
	invocation := svc.buildWhisperXInvocation(
		[]string{"/tmp/audio.wav"},
		[]TranscribeRequest{{OutputDir: "/tmp/out", Language: "en"}},
		"large-v3",
	)
	if invocation.TranscriptionProfileName != transcriptionProfileID {
		t.Fatalf("profile = %q, want %q", invocation.TranscriptionProfileName, transcriptionProfileID)
	}
	if invocation.Device != "cuda" {
		t.Fatalf("device = %q, want cuda", invocation.Device)
	}
	if invocation.ComputeType != "float16" {
		t.Fatalf("compute type = %q, want float16", invocation.ComputeType)
	}
	joined := strings.Join(invocation.Args, " ")
	for _, want := range []string{"--from whisperx", "--audio /tmp/audio.wav", "--output-dir /tmp/out", "--vad-method pyannote", "--batch-size 16", "--chunk-size 30", "--vad-onset 0.500", "--vad-offset 0.363", "--condition-on-previous-text false", "--transcription-profile-name whisperx_wrapper_v2", "--hf-token hf-token"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("invocation args missing %q: %s", want, joined)
		}
	}
	if !strings.Contains(strings.Join(invocation.Env, " "), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1") {
		t.Fatalf("expected torch compatibility env in %v", invocation.Env)
	}
}

func TestBuildWhisperXInvocationBatch(t *testing.T) {
	svc := New(Params{Model: "large-v3", VADMethod: "silero"}, nil)
	invocation := svc.buildWhisperXInvocation(
		[]string{"/tmp/e1/audio.wav", "/tmp/e2/audio.wav"},
		[]TranscribeRequest{
			{OutputDir: "/tmp/e1", Language: "en"},
			{OutputDir: "/tmp/e2", Language: "de"},
		},
		"large-v3",
	)
	joined := strings.Join(invocation.Args, " ")
	for _, want := range []string{
		"--audio /tmp/e1/audio.wav --output-dir /tmp/e1 --language en",
		"--audio /tmp/e2/audio.wav --output-dir /tmp/e2 --language de",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("invocation args missing %q: %s", want, joined)
		}
	}
	// Model and profile flags appear once, after the per-item triples.
	if strings.Count(joined, "--model ") != 1 {
		t.Fatalf("expected exactly one --model flag: %s", joined)
	}
}

func TestTranscribeBatchRejectsMixedModels(t *testing.T) {
	svc := New(Params{Model: "large-v3", VADMethod: "silero"}, nil)
	_, err := svc.TranscribeBatch(context.Background(), []TranscribeRequest{
		{OutputDir: "/tmp/a", Language: "en"},
		{OutputDir: "/tmp/b", Language: "en", Model: "large-v3-turbo"},
	})
	if err == nil || !strings.Contains(err.Error(), "mixed models") {
		t.Fatalf("expected mixed-models error, got %v", err)
	}
}

func TestTranscribeBatchRejectsEmpty(t *testing.T) {
	svc := New(Params{Model: "large-v3", VADMethod: "silero"}, nil)
	if _, err := svc.TranscribeBatch(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty batch")
	}
}
