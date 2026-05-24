package subtitle

import (
	"context"
	"encoding/json"
	"fmt"
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
	stats, err := filterWhisperXJSON(src, dst, 1200)
	if err != nil {
		t.Fatalf("filterWhisperXJSON() error = %v", err)
	}
	if stats.OriginalSegments != 3 || stats.FilteredSegments != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", stats.OriginalSegments, stats.FilteredSegments)
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

func TestFilterWhisperXJSON_RemovesLowInformationLongSegment(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "audio.json")
	dst := filepath.Join(dir, "audio.filtered.json")
	payload := whisperXPayload{
		Language: "en",
		Segments: []map[string]any{
			{"start": 10.0, "end": 12.0, "text": "Normal dialogue"},
			{"start": 50.0, "end": 65.0, "text": "all", "words": []map[string]any{{"word": "all", "start": 50.0, "end": 65.0, "probability": 0.22}}},
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
	stats, err := filterWhisperXJSON(src, dst, 1200)
	if err != nil {
		t.Fatalf("filterWhisperXJSON() error = %v", err)
	}
	if stats.RemovedBySegmentHeuristics != 1 {
		t.Fatalf("RemovedBySegmentHeuristics = %d, want 1", stats.RemovedBySegmentHeuristics)
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

func TestFilterWhisperXJSON_DoesNotMutateCanonicalSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "audio.json")
	dst := filepath.Join(dir, "audio.filtered.json")
	original := `{"language":"en","segments":[{"start":10,"end":12,"text":"Normal dialogue"},{"start":50,"end":65,"text":"all","words":[{"word":"all","start":50,"end":65,"probability":0.22}]},{"start":90,"end":92,"text":"More dialogue"}]}`
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := filterWhisperXJSON(src, dst, 1200); err != nil {
		t.Fatalf("filterWhisperXJSON() error = %v", err)
	}
	srcData, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(srcData) != original {
		t.Fatalf("canonical source mutated: got %s", string(srcData))
	}
}

func TestFormatForcedSubtitleFromCanonical_FiltersForeignSegments(t *testing.T) {
	origRun := runStableTS
	t.Cleanup(func() { runStableTS = origRun })

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "audio.json")
	payload := whisperXPayload{
		Language: "en",
		Segments: []map[string]any{
			{"start": 1.0, "end": 2.0, "text": "Hello there", "source_language": "en", "task": "transcribe", "foreign": false},
			{"start": 3.0, "end": 4.0, "text": "Where is the package?", "source_language": "fr", "target_language": "en", "task": "translate", "foreign": true},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	runStableTS = func(ctx context.Context, args []string) ([]byte, error) {
		inputData, err := os.ReadFile(args[5])
		if err != nil {
			t.Fatal(err)
		}
		input := string(inputData)
		if strings.Contains(input, "Hello there") || !strings.Contains(input, "Where is the package?") {
			t.Fatalf("forced payload not filtered correctly: %s", input)
		}
		for _, unsupported := range []string{"foreign", "source_language", "target_language", "task"} {
			if strings.Contains(input, unsupported) {
				t.Fatalf("forced formatter payload contains unsupported %q field: %s", unsupported, input)
			}
		}
		if err := os.WriteFile(args[6], []byte("1\n00:00:03,000 --> 00:00:04,000\nWhere is the package?\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}

	result, err := formatForcedSubtitleFromCanonical(context.Background(), transcriptionArtifacts{JSONPath: jsonPath}, filepath.Join(dir, "work"), filepath.Join(dir, "Movie.en.forced.srt"), 120, "en")
	if err != nil {
		t.Fatalf("formatForcedSubtitleFromCanonical() error = %v", err)
	}
	if result.Path == "" || result.Segments != 1 || fmt.Sprint(result.Languages) != "[fr]" || result.Decision != "generated" {
		t.Fatalf("result = %+v", result)
	}
}

func TestFormatForcedSubtitleFromCanonical_NoneDetected(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "audio.json")
	payload := whisperXPayload{Language: "en", Segments: []map[string]any{{"start": 1.0, "end": 2.0, "text": "Hello there"}}}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := formatForcedSubtitleFromCanonical(context.Background(), transcriptionArtifacts{JSONPath: jsonPath}, filepath.Join(dir, "work"), filepath.Join(dir, "Movie.en.forced.srt"), 120, "en")
	if err != nil {
		t.Fatalf("formatForcedSubtitleFromCanonical() error = %v", err)
	}
	if result.Path != "" || result.Segments != 0 || result.Decision != "none_detected" {
		t.Fatalf("result = %+v", result)
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
			"start":           1.0,
			"end":             3.0,
			"text":            "General Kenobi",
			"foreign":         true,
			"source_language": "fr",
			"target_language": "en",
			"task":            "translate",
			"words": []map[string]any{
				{"word": "General", "start": 1.0, "end": 1.5, "score": 0.9, "speaker": "SPEAKER_00"},
				{"word": " Kenobi", "start": 1.5, "end": 3.0, "probability": 0.8},
			},
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
		inputData, err := os.ReadFile(args[5])
		if err != nil {
			t.Fatal(err)
		}
		input := string(inputData)
		for _, unsupported := range []string{"foreign", "source_language", "target_language", "task", "speaker", "score"} {
			if strings.Contains(input, unsupported) {
				t.Fatalf("display formatter payload contains unsupported %q field: %s", unsupported, input)
			}
		}
		if !strings.Contains(input, "probability") {
			t.Fatalf("display formatter payload did not translate word score to probability: %s", input)
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
	if result.RemovedByTextRules != 0 || result.RemovedBySegmentHeuristics != 0 {
		t.Fatalf("unexpected removals: %+v", result)
	}
	if result.SplitCues != 0 || result.WrappedCues != 0 || result.RetimedCues != 0 {
		t.Fatalf("unexpected post-processing: %+v", result)
	}
	contents, err := os.ReadFile(displayPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "General Kenobi") {
		t.Fatalf("formatted subtitle missing expected content: %s", string(contents))
	}
}
