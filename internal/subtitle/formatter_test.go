package subtitle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/srtutil"
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

func TestFilterCanonicalTranscriptJSON_RemovesIsolatedMusicCue(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "audio.json")
	dst := filepath.Join(dir, "audio.filtered.json")
	payload := canonicalTranscriptPayload{
		Language: "en",
		Segments: []map[string]any{
			{"start": 10.0, "end": 12.0, "text": "Normal dialogue"},
			{"start": 50.0, "end": 52.0, "text": "♪"},
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
	jsonPath, original, filtered, err := filterCanonicalTranscriptJSON(src, dst, 1200)
	if err != nil {
		t.Fatalf("filterCanonicalTranscriptJSON() error = %v", err)
	}
	if jsonPath != dst {
		t.Fatalf("json path = %q, want %q", jsonPath, dst)
	}
	if original != 3 || filtered != 2 {
		t.Fatalf("counts = %d/%d, want 3/2", original, filtered)
	}
	filteredData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	var out canonicalTranscriptPayload
	if err := json.Unmarshal(filteredData, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(out.Segments))
	}
}

func TestFilterCanonicalTranscriptJSON_ReusesSourceWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "audio.json")
	dst := filepath.Join(dir, "audio.filtered.json")
	payload := canonicalTranscriptPayload{
		Language: "en",
		Segments: []map[string]any{{"start": 10.0, "end": 12.0, "text": "Normal dialogue"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	jsonPath, original, filtered, err := filterCanonicalTranscriptJSON(src, dst, 1200)
	if err != nil {
		t.Fatalf("filterCanonicalTranscriptJSON() error = %v", err)
	}
	if jsonPath != src {
		t.Fatalf("json path = %q, want %q", jsonPath, src)
	}
	if original != 1 || filtered != 1 {
		t.Fatalf("counts = %d/%d, want 1/1", original, filtered)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("expected no filtered copy, got err=%v", err)
	}
}

func TestWrapCueText_BalancesLongLine(t *testing.T) {
	got := wrapCueText("Captain's log, Stardate four one three eight six point four.")
	want := "Captain's log, Stardate four\none three eight six point four."
	if got != want {
		t.Fatalf("wrapCueText() = %q, want %q", got, want)
	}
}

func TestRegroupDisplayCues_MergesTinyAdjacentFragments(t *testing.T) {
	cues := regroupDisplayCues([]srtutil.Cue{
		{Index: 1, Start: 0, End: 0.5, Text: "Anything"},
		{Index: 2, Start: 0.5, End: 1.5, Text: "on that design, Data?"},
	})
	if len(cues) != 1 {
		t.Fatalf("expected 1 merged cue, got %d", len(cues))
	}
	if cues[0].Text != "Anything on that design, Data?" {
		t.Fatalf("merged cue text = %q", cues[0].Text)
	}
}

func TestRegroupDisplayCues_WrapsLongCueWithoutOrphanLine(t *testing.T) {
	cues := regroupDisplayCues([]srtutil.Cue{{Index: 1, Start: 0, End: 4.4, Text: "which automatic scanners recorded, providing us with the long awaited opportunity to"}})
	if len(cues) != 1 {
		t.Fatalf("expected 1 cue, got %d", len(cues))
	}
	lines := strings.Split(cues[0].Text, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 wrapped lines, got %d", len(lines))
	}
	if len([]rune(lines[1])) <= 3 {
		t.Fatalf("unexpected orphan second line: %q", lines[1])
	}
}

func TestRegroupDisplayCues_SplitsMultiSentenceCue(t *testing.T) {
	cues := regroupDisplayCues([]srtutil.Cue{{Index: 1, Start: 0, End: 6, Text: "First sentence continues here. Second sentence continues for a while."}})
	if len(cues) != 2 {
		t.Fatalf("expected 2 cues, got %d", len(cues))
	}
	if cues[0].Text != "First sentence continues here." {
		t.Fatalf("first cue text = %q", cues[0].Text)
	}
	if cues[1].Text != "Second sentence continues for a while." {
		t.Fatalf("second cue text = %q", cues[1].Text)
	}
	if cues[0].End <= cues[0].Start || cues[1].End <= cues[1].Start {
		t.Fatal("expected positive cue durations")
	}
}

func TestFormatSubtitleFromCanonical_UsesStableTSOutput(t *testing.T) {
	origRun := runStableTS
	t.Cleanup(func() { runStableTS = origRun })

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "audio.json")
	payload := canonicalTranscriptPayload{
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
		if got := args[5]; got != jsonPath {
			t.Fatalf("formatter json path = %q, want %q", got, jsonPath)
		}
		filteredData, err := os.ReadFile(args[5])
		if err != nil {
			t.Fatal(err)
		}
		var filtered canonicalTranscriptPayload
		if err := json.Unmarshal(filteredData, &filtered); err != nil {
			t.Fatal(err)
		}
		if got := filtered.Segments[0]["text"]; got != "General Kenobi" {
			t.Fatalf("filtered segment text = %v, want %q", got, "General Kenobi")
		}
		outputPath := args[6]
		if err := os.WriteFile(outputPath, []byte("1\n00:00:01,000 --> 00:00:03,000\nThis is a deliberately long subtitle line for wrapping\n"), 0o644); err != nil {
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
	formatted := string(contents)
	if !strings.Contains(formatted, "This is a deliberately long\nsubtitle line for wrapping") {
		t.Fatalf("formatted subtitle missing expected regrouped line break: %s", formatted)
	}
}
