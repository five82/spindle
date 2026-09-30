package ui

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestMovieFileDetailsIncludeDeliveredAndIntermediateFacts(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	m.width = 80
	var final spindle.FinalValidation
	if err := json.Unmarshal([]byte(`{"passed":true,"resolution":"1920x1048","video_codec":"av1","audio":["en opus 8ch"],"av_sync":{"passed":true}}`), &final); err != nil {
		t.Fatal(err)
	}
	item := spindle.QueueItem{ID: 1, Metadata: json.RawMessage(`{"media_type":"movie"}`), Episodes: []spindle.EpisodeStatus{{Key: "main", Title: "Air", FinalPath: "/library/Air.mkv", FinalSizeBytes: 1500000000, FinalRoute: "library", FinalValidation: &final, EncodeStats: &spindle.EncodeStats{EncodedSizeBytes: 2000000000, EncodeSeconds: 100, Speed: 2}, SubtitleSource: "opensubtitles", SubtitledPath: "sub"}}}
	m.detailState.episodeCollapsed[1] = false
	var b strings.Builder
	_, totals := item.EpisodeSnapshot()
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	got := stripANSI(b.String())
	for _, want := range []string{"File Air", "/library/Air.mkv", "1920x1048", "en opus 8ch", "passed", "(before Apply)", "to library", "SRT applied"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}

func TestFailedAssetDoesNotInventFailedPipelineColumn(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	item := spindle.QueueItem{ID: 1, Episodes: []spindle.EpisodeStatus{{Key: "a", RippedPath: "rip", Status: "failed", ErrorMessage: "subtitles failed"}}}
	var b strings.Builder
	_, totals := item.EpisodeSnapshot()
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	got := stripANSI(b.String())
	if !strings.Contains(got, "encode ○") || !strings.Contains(got, "subtitles failed") {
		t.Fatal(got)
	}
	if strings.Contains(got, "Encode failed") {
		t.Fatal(got)
	}
}

// Passing Reel checks collapse to a count with failures itemized, staging
// paths keep both ends on one row, and the path caveat rides with them.
func TestFileDetailsCondenseChecksAndPaths(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	m.width = 80
	rip := "/home/op/.local/share/spindle/staging/" + strings.Repeat("78ECD2A05F36", 6) + "/ripped/Show- Disc 2_t00.mkv"
	item := spindle.QueueItem{ID: 1, Episodes: []spindle.EpisodeStatus{{Key: "main", RippedPath: rip, EncodeStats: &spindle.EncodeStats{
		Validation: &spindle.EncodingValidation{Steps: []spindle.EncodingValidationStep{
			{Name: "Video codec", Passed: true}, {Name: "Bit depth", Passed: true}, {Name: "Audio tracks", Details: "1 of 2 Opus"},
		}},
	}}}}
	_, totals := item.EpisodeSnapshot()
	var b strings.Builder
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	if got := stripANSI(b.String()); strings.Contains(got, "Paths are recorded") || strings.Contains(got, "Reel checks") {
		t.Fatalf("collapsed details leaked: %s", got)
	}
	m.detailState.episodeCollapsed[1] = false
	b.Reset()
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	got := stripANSI(b.String())
	for _, want := range []string{"Reel checks  2/3 passed", "Failed       Audio tracks: 1 of 2 Opus", "...ECD2A05F36/ripped/Show- Disc 2_t00.mkv", "Paths are recorded artifacts"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "passed=") || strings.Contains(got, "Video codec") {
		t.Fatalf("passing checks must not get rows: %s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "Rip file") && lipgloss.Width(line) > panelInnerWidth(m.width) {
			t.Fatalf("rip path overflows: %q", line)
		}
	}
}
