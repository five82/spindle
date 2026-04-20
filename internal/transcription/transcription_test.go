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

func TestCacheKeyWithContentKey(t *testing.T) {
	req := TranscribeRequest{
		InputPath:  "/some/path.mkv",
		AudioIndex: 1,
		Language:   "en",
		ContentKey: "stable-content-id",
	}
	key := cacheKey(req, "large-v3")
	if key == "" {
		t.Fatal("expected non-empty cache key")
	}

	// Content key should NOT depend on input path.
	req2 := TranscribeRequest{
		InputPath:  "/different/path.mkv",
		AudioIndex: 2,
		Language:   "en",
		ContentKey: "stable-content-id",
	}
	key2 := cacheKey(req2, "large-v3")
	if key != key2 {
		t.Errorf("content-stable keys should match: %s != %s", key, key2)
	}
}

func TestCacheKeyWithoutContentKey(t *testing.T) {
	req := TranscribeRequest{
		InputPath:  "/some/path.mkv",
		AudioIndex: 1,
		Language:   "en",
	}
	key := cacheKey(req, "large-v3")
	if key == "" {
		t.Fatal("expected non-empty cache key")
	}

	// Different path should produce different key.
	req2 := TranscribeRequest{
		InputPath:  "/different/path.mkv",
		AudioIndex: 1,
		Language:   "en",
	}
	key2 := cacheKey(req2, "large-v3")
	if key == key2 {
		t.Errorf("path-based keys should differ for different paths")
	}
}

func TestCacheKeySameContentKeySameModelLanguage(t *testing.T) {
	req1 := TranscribeRequest{
		ContentKey: "disc-abc-title-1",
		Language:   "en",
	}
	req2 := TranscribeRequest{
		ContentKey: "disc-abc-title-1",
		Language:   "en",
	}
	k1 := cacheKey(req1, "large-v3")
	k2 := cacheKey(req2, "large-v3")
	if k1 != k2 {
		t.Errorf("same content key, model, language should produce same key: %s != %s", k1, k2)
	}
}

func TestCacheKeyDifferentContentKey(t *testing.T) {
	req1 := TranscribeRequest{
		ContentKey: "disc-abc-title-1",
		Language:   "en",
	}
	req2 := TranscribeRequest{
		ContentKey: "disc-xyz-title-2",
		Language:   "en",
	}
	k1 := cacheKey(req1, "large-v3")
	k2 := cacheKey(req2, "large-v3")
	if k1 == k2 {
		t.Errorf("different content keys should produce different cache keys")
	}
}

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

func TestLookupMiss(t *testing.T) {
	dir := t.TempDir()
	svc := New("large-v3", false, "silero", "", dir, nil)

	result, ok := svc.Lookup("nonexistent-key")
	if ok {
		t.Errorf("expected cache miss, got hit: %+v", result)
	}
	if result != nil {
		t.Errorf("expected nil result on miss")
	}
}

func TestLookupRejectsCacheWithoutJSON(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, "cache-key")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "audio.srt"), []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New("large-v3", false, "silero", "", dir, nil)
	if result, ok := svc.Lookup("cache-key"); ok || result != nil {
		t.Fatalf("expected cache miss when JSON artifact is missing, got %+v", result)
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

	svc := New("large-v3", false, "silero", "", t.TempDir(), nil)
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

	svc := New("large-v3", false, "silero", "", t.TempDir(), nil)
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
	svc := New("large-v3", true, "pyannote", "hf-token", t.TempDir(), nil)
	invocation := svc.buildWhisperXInvocation("/tmp/audio.wav", "/tmp/out", "large-v3", "en")
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
	for _, want := range []string{"--from whisperx", "--audio /tmp/audio.wav", "--output-dir /tmp/out", "--vad-method pyannote", "--batch-size 16", "--chunk-size 30", "--vad-onset 0.500", "--vad-offset 0.363", "--condition-on-previous-text false", "--hf-token hf-token"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("invocation args missing %q: %s", want, joined)
		}
	}
	if !strings.Contains(strings.Join(invocation.Env, " "), "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1") {
		t.Fatalf("expected torch compatibility env in %v", invocation.Env)
	}
}

func TestStoreAndLookupRoundTrip(t *testing.T) {
	cacheDir := t.TempDir()
	svc := New("large-v3", false, "silero", "", cacheDir, nil)

	// Write a sample SRT to a source location.
	srcDir := t.TempDir()
	srtPath := filepath.Join(srcDir, "audio.srt")
	if err := os.WriteFile(srtPath, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}

	jsonPath := filepath.Join(srcDir, "audio.json")
	if err := os.WriteFile(jsonPath, []byte(`{"segments":[{"start":1.0,"end":4.0,"text":"Hello, world."}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	original := &TranscribeResult{
		SRTPath:  srtPath,
		JSONPath: jsonPath,
		Segments: 3,
	}

	key := "test-round-trip-key"
	if err := svc.Store(key, original); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	result, ok := svc.Lookup(key)
	if !ok {
		t.Fatal("expected cache hit after store")
	}
	if result.Segments != 3 {
		t.Errorf("expected 3 segments, got %d", result.Segments)
	}
	if result.SRTPath == "" {
		t.Error("expected non-empty SRT path")
	}
	if result.JSONPath == "" {
		t.Error("expected non-empty JSON path")
	}
	if result.Duration != 12.0 {
		t.Errorf("expected Duration 12.0, got %.1f", result.Duration)
	}

	// Verify the cached file exists and is readable.
	data, err := os.ReadFile(result.SRTPath)
	if err != nil {
		t.Fatalf("cached SRT not readable: %v", err)
	}
	if len(data) == 0 {
		t.Error("cached SRT is empty")
	}
	jsonData, err := os.ReadFile(result.JSONPath)
	if err != nil {
		t.Fatalf("cached JSON not readable: %v", err)
	}
	if len(jsonData) == 0 {
		t.Error("cached JSON is empty")
	}
}
