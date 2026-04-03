package audioanalysis

import (
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestUpdateProgress(t *testing.T) {
	h := &Handler{}
	item := &queue.Item{}
	h.updateProgress(item, 75, "Phase 3/3 - Post-refinement audio analysis")
	if item.ProgressPercent != 75 {
		t.Fatalf("ProgressPercent = %f, want 75", item.ProgressPercent)
	}
	if item.ProgressMessage != "Phase 3/3 - Post-refinement audio analysis" {
		t.Fatalf("ProgressMessage = %q", item.ProgressMessage)
	}
}

func TestAssetKeys_Movie(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
	}

	keys := env.AssetKeys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0] != "main" {
		t.Fatalf("expected key 'main', got %q", keys[0])
	}
}

func TestAssetKeys_TV(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: "s01e02"},
			{Key: "s01e03"},
		},
	}

	keys := env.AssetKeys()
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	expected := []string{"s01e01", "s01e02", "s01e03"}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("key[%d]: expected %q, got %q", i, want, keys[i])
		}
	}
}

func TestAssetKeys_TV_SkipsEmptyKeys(t *testing.T) {
	env := &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Episodes: []ripspec.Episode{
			{Key: "s01e01"},
			{Key: ""},
			{Key: "s01e03"},
		},
	}

	keys := env.AssetKeys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys (skipping empty), got %d", len(keys))
	}
	if keys[0] != "s01e01" || keys[1] != "s01e03" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestTempOutputDir(t *testing.T) {
	dir := tempOutputDir("abc123", "s01e01", 2)
	want := "/tmp/spindle-commentary-abc123-s01e01-2"
	if dir != want {
		t.Fatalf("expected %q, got %q", want, dir)
	}
}

func TestBuildCommentaryUserPrompt_WithTitle(t *testing.T) {
	stream := ffprobe.Stream{
		Tags: map[string]string{"title": "Director Commentary"},
	}
	prompt := buildCommentaryUserPrompt(stream, "Some transcript text here.")

	if !contains(prompt, "Title: Director Commentary") {
		t.Errorf("expected title in prompt, got:\n%s", prompt)
	}
	if !contains(prompt, "Some transcript text here.") {
		t.Errorf("expected transcript in prompt, got:\n%s", prompt)
	}
}

func TestBuildCommentaryUserPrompt_NoTitle(t *testing.T) {
	stream := ffprobe.Stream{
		Tags: map[string]string{},
	}
	prompt := buildCommentaryUserPrompt(stream, "Transcript.")

	if contains(prompt, "Title:") {
		t.Errorf("expected no title line, got:\n%s", prompt)
	}
	if !contains(prompt, "Transcript.") {
		t.Errorf("expected transcript in prompt, got:\n%s", prompt)
	}
}

func TestBuildCommentaryUserPrompt_Truncation(t *testing.T) {
	long := make([]byte, maxTranscriptLen+500)
	for i := range long {
		long[i] = 'a'
	}

	stream := ffprobe.Stream{Tags: map[string]string{}}
	prompt := buildCommentaryUserPrompt(stream, string(long))

	if !contains(prompt, "[truncated]") {
		t.Error("expected truncation marker in prompt")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
