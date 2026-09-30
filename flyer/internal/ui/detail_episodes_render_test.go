package ui

import (
	"encoding/json"
	"strings"
	"testing"

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
