package subtitle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisplaySubtitlePath(t *testing.T) {
	got := displaySubtitlePath("/tmp/Show - S01E01.mkv", "eng")
	want := "/tmp/Show - S01E01.en.srt"
	if got != want {
		t.Fatalf("displaySubtitlePath() = %q, want %q", got, want)
	}
	got = displayForcedSubtitlePath("/tmp/Show - S01E01.mkv", "en")
	want = "/tmp/Show - S01E01.en.forced.srt"
	if got != want {
		t.Fatalf("displayForcedSubtitlePath() = %q, want %q", got, want)
	}
}

func TestFilterWhisperXJSON_RemovesIsolatedHallucination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "audio.json")
	dst := filepath.Join(dir, "audio.filtered.json")
	payload := whisperXPayload{
		Language: "en",
		Segments: []map[string]any{
			{"start": 10.0, "end": 12.0, "text": "Normal dialogue"},
			{"start": 50.0, "end": 52.0, "text": "Thank you"},
			{"start": 90.0, "end": 92.0, "text": "More dialogue"},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	original, filtered, err := filterWhisperXJSON(src, dst, 1200)
	if err != nil {
		t.Fatalf("filterWhisperXJSON() error = %v", err)
	}
	if original != 3 || filtered != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", original, filtered)
	}
	filteredData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	var out whisperXPayload
	if err := json.Unmarshal(filteredData, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(out.Segments))
	}
}

func TestFormatSubtitleFromCanonical_UsesStableTSOutput(t *testing.T) {
	origRun := runStableTS
	t.Cleanup(func() { runStableTS = origRun })

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "audio.json")
	payload := whisperXPayload{
		Language: "en",
		Segments: []map[string]any{{
			"start": 1.0,
			"end":   3.0,
			"text":  "General Kenobi",
			"words": []map[string]any{{"word": "General", "start": 1.0, "end": 1.5}, {"word": " Kenobi", "start": 1.5, "end": 3.0}},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	runStableTS = func(ctx context.Context, args []string) ([]byte, error) {
		if len(args) < 9 {
			t.Fatalf("unexpected args: %v", args)
		}
		outputPath := args[6]
		if err := os.WriteFile(outputPath, []byte("1\n00:00:01,000 --> 00:00:03,000\nGeneral Kenobi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}

	displayPath := filepath.Join(dir, "Movie.en.srt")
	result, err := formatSubtitleFromCanonical(context.Background(), transcriptionArtifacts{JSONPath: jsonPath}, filepath.Join(dir, "work"), displayPath, 120, "en")
	if err != nil {
		t.Fatalf("formatSubtitleFromCanonical() error = %v", err)
	}
	if result.DisplayPath != displayPath {
		t.Fatalf("DisplayPath = %q, want %q", result.DisplayPath, displayPath)
	}
	if result.OriginalSegments != 1 || result.FilteredSegments != 1 {
		t.Fatalf("segment counts = %d/%d, want 1/1", result.OriginalSegments, result.FilteredSegments)
	}
	contents, err := os.ReadFile(displayPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "General Kenobi") {
		t.Fatalf("formatted subtitle missing expected content: %s", string(contents))
	}
}
