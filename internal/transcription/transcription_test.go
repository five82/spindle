package transcription

import (
	"os"
	"path/filepath"
	"testing"
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

func TestCountSRTSegments(t *testing.T) {
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "test.srt")
	if err := os.WriteFile(srtPath, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := countSRTSegments(srtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 segments, got %d", count)
	}
}

func TestCountSRTSegmentsEmpty(t *testing.T) {
	dir := t.TempDir()
	srtPath := filepath.Join(dir, "empty.srt")
	if err := os.WriteFile(srtPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := countSRTSegments(srtPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 segments, got %d", count)
	}
}

func TestLookupMiss(t *testing.T) {
	dir := t.TempDir()
	svc := New("large-v3", false, "silero", "", dir)

	result, ok := svc.Lookup("nonexistent-key")
	if ok {
		t.Errorf("expected cache miss, got hit: %+v", result)
	}
	if result != nil {
		t.Errorf("expected nil result on miss")
	}
}

func TestStoreAndLookupRoundTrip(t *testing.T) {
	cacheDir := t.TempDir()
	svc := New("large-v3", false, "silero", "", cacheDir)

	// Write a sample SRT to a source location.
	srcDir := t.TempDir()
	srtPath := filepath.Join(srcDir, "audio.srt")
	if err := os.WriteFile(srtPath, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}

	original := &TranscribeResult{
		SRTPath:  srtPath,
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

	// Verify the cached file exists and is readable.
	data, err := os.ReadFile(result.SRTPath)
	if err != nil {
		t.Fatalf("cached SRT not readable: %v", err)
	}
	if len(data) == 0 {
		t.Error("cached SRT is empty")
	}
}
