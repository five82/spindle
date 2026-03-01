package subtitles

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"spindle/internal/ripspec"
)

func TestLookupTranscriptPath_NilEnvelope(t *testing.T) {
	if got := lookupTranscriptPath(nil, "s01e01"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLookupTranscriptPath_MissingAttribute(t *testing.T) {
	env := &ripspec.Envelope{Attributes: map[string]any{"other": "value"}}
	if got := lookupTranscriptPath(env, "s01e01"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLookupTranscriptPath_MapStringString(t *testing.T) {
	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]string{
				"s01e01": "/tmp/s01e01.srt",
				"s01e02": "/tmp/s01e02.srt",
			},
		},
	}
	if got := lookupTranscriptPath(env, "s01e01"); got != "/tmp/s01e01.srt" {
		t.Fatalf("expected /tmp/s01e01.srt, got %q", got)
	}
	if got := lookupTranscriptPath(env, "s01e03"); got != "" {
		t.Fatalf("expected empty for missing key, got %q", got)
	}
}

func TestLookupTranscriptPath_MapStringAny(t *testing.T) {
	// Simulates JSON round-trip where map[string]string becomes map[string]any.
	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]any{
				"s01e01": "/tmp/s01e01.srt",
				"s01e02": "/tmp/s01e02.srt",
			},
		},
	}
	if got := lookupTranscriptPath(env, "s01e02"); got != "/tmp/s01e02.srt" {
		t.Fatalf("expected /tmp/s01e02.srt, got %q", got)
	}
}

func TestLookupTranscriptPath_EmptyKey(t *testing.T) {
	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]string{"s01e01": "/tmp/s01e01.srt"},
		},
	}
	if got := lookupTranscriptPath(env, ""); got != "" {
		t.Fatalf("expected empty for blank key, got %q", got)
	}
}

const sampleSRT = `1
00:00:01,000 --> 00:00:04,000
Hello world.

2
00:00:05,000 --> 00:00:08,000
This is a test.

3
00:00:10,000 --> 00:00:15,000
Final cue here.
`

func TestTryReuseCachedTranscript_Hit(t *testing.T) {
	tmp := t.TempDir()

	// Write a cached SRT file.
	cachedPath := filepath.Join(tmp, "contentid", "s01e01", "s01e01-contentid.srt")
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedPath, []byte(sampleSRT), 0o644); err != nil {
		t.Fatal(err)
	}

	outputDir := filepath.Join(tmp, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]string{
				"s01e01": cachedPath,
			},
		},
	}

	stage := &Stage{logger: slog.Default()}
	target := subtitleTarget{
		EpisodeKey: "s01e01",
		OutputDir:  outputDir,
		BaseName:   "Show - S01E01",
	}

	result, ok := stage.tryReuseCachedTranscript(target, env)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if result.SegmentCount != 3 {
		t.Fatalf("expected 3 segments, got %d", result.SegmentCount)
	}
	if result.Duration.Seconds() < 14 || result.Duration.Seconds() > 16 {
		t.Fatalf("unexpected duration: %v", result.Duration)
	}
	expectedPath := filepath.Join(outputDir, "Show - S01E01.srt")
	if result.SubtitlePath != expectedPath {
		t.Fatalf("expected path %q, got %q", expectedPath, result.SubtitlePath)
	}

	// Verify the file was actually copied.
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("failed to read copied file: %v", err)
	}
	if !strings.Contains(string(data), "Hello world") {
		t.Fatal("copied file missing expected content")
	}
}

func TestTryReuseCachedTranscript_MissingFile(t *testing.T) {
	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]string{
				"s01e01": "/nonexistent/path/s01e01.srt",
			},
		},
	}

	stage := &Stage{logger: slog.Default()}
	target := subtitleTarget{
		EpisodeKey: "s01e01",
		OutputDir:  t.TempDir(),
		BaseName:   "Show - S01E01",
	}

	_, ok := stage.tryReuseCachedTranscript(target, env)
	if ok {
		t.Fatal("expected cache miss for nonexistent file")
	}
}

func TestTryReuseCachedTranscript_EmptySRT(t *testing.T) {
	tmp := t.TempDir()

	// Write an empty SRT file.
	cachedPath := filepath.Join(tmp, "empty.srt")
	if err := os.WriteFile(cachedPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]string{
				"s01e01": cachedPath,
			},
		},
	}

	stage := &Stage{logger: slog.Default()}
	target := subtitleTarget{
		EpisodeKey: "s01e01",
		OutputDir:  t.TempDir(),
		BaseName:   "Show - S01E01",
	}

	_, ok := stage.tryReuseCachedTranscript(target, env)
	if ok {
		t.Fatal("expected cache miss for empty SRT")
	}
}

func TestTryReuseCachedTranscript_MovieSkipped(t *testing.T) {
	env := &ripspec.Envelope{
		Attributes: map[string]any{
			ripspec.AttrContentIDTranscripts: map[string]string{
				"primary": "/some/path.srt",
			},
		},
	}

	stage := &Stage{logger: slog.Default()}
	target := subtitleTarget{
		EpisodeKey: "", // normalizes to "primary"
		OutputDir:  t.TempDir(),
		BaseName:   "Movie",
	}

	_, ok := stage.tryReuseCachedTranscript(target, env)
	if ok {
		t.Fatal("expected cache skip for movie (primary key)")
	}
}

func TestTryReuseCachedTranscript_NoAttributes(t *testing.T) {
	env := &ripspec.Envelope{}

	stage := &Stage{logger: slog.Default()}
	target := subtitleTarget{
		EpisodeKey: "s01e01",
		OutputDir:  t.TempDir(),
		BaseName:   "Show - S01E01",
	}

	_, ok := stage.tryReuseCachedTranscript(target, env)
	if ok {
		t.Fatal("expected cache miss with no attributes")
	}
}
