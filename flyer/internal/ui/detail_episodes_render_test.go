package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

func TestRenderEpisodeListStates(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	styles := m.theme.Styles()
	base := spindle.QueueItem{ID: 42, Episodes: []spindle.EpisodeStatus{
		{Key: "a", Season: 1, Episode: 1, Title: "Pilot", RippedPath: "/rip", EncodedPath: "/enc", SubtitledPath: "/sub", FinalPath: "/final"},
		{Key: "b", Season: 1, Episode: 2, Title: "Second", Status: "failed", ErrorMessage: strings.Repeat("x", 90)},
	}}
	tests := []struct {
		name      string
		item      spindle.QueueItem
		collapsed bool
		want      []string
		absent    []string
	}{
		{"empty", spindle.QueueItem{ID: 42}, false, nil, []string{"planned"}},
		{"expanded failures and assets", base, false, []string{"2 planned", "1 failed", "S01E01", "Pilot", "R✓", "S01E02", "Second", "failed", strings.Repeat("x", 77) + "...", "Press t to collapse"}, nil},
		{"collapsed", base, true, []string{"2 planned", "Press t to expand"}, []string{"Pilot", "Press t to collapse"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.detailState.episodeCollapsed[42] = tc.collapsed
			var b strings.Builder
			_, totals := tc.item.EpisodeSnapshot()
			m.renderEpisodeList(&b, tc.item, styles, totals)
			got := stripANSI(b.String())
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("renderEpisodeList() = %q, missing %q", got, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("renderEpisodeList() = %q, unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

func TestRenderEpisodeListUnmatchedWarningAndActiveRow(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	item := spindle.QueueItem{ID: 7, Tasks: []spindle.Task{{State: "running", ActiveAssetKey: "B"}}, Episodes: []spindle.EpisodeStatus{
		{Key: "a", Season: 2, Episode: 1, Title: "Mapped"},
		{Key: "b", Title: "Unmapped", SourceTitleID: 3, RuntimeSeconds: 3660, SubtitleLanguage: "en", SubtitleSource: "whisperx", NeedsReview: true, ReviewReason: "check subtitle"},
	}}
	var b strings.Builder
	_, totals := item.EpisodeSnapshot()
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	got := stripANSI(b.String())
	for _, want := range []string{"1 matched", "Episode numbers not confirmed", "S??E??", "R◉", "Unmapped", "Title 03", "61m", "EN", "AI", "Unmatched", "check subtitle"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderEpisodeList() = %q, missing %q", got, want)
		}
	}
}

func TestDescribeEpisodeWithExtrasAndCompactMeta(t *testing.T) {
	title, extras := describeEpisodeWithExtras(spindle.EpisodeStatus{OutputBasename: "fallback.mkv", RuntimeSeconds: 120, SubtitleLanguage: " fr ", SubtitleSource: "WhisperX"})
	if title != "fallback.mkv" || !equalStringSlices(extras, []string{"2m", "FR", "AI"}) {
		t.Fatalf("describeEpisodeWithExtras() = (%q, %v)", title, extras)
	}
	if got := compactEpisodeMeta("Title 01", "Mapped", extras); got != "Title 01  ·  Mapped  ·  2m · FR · AI" {
		t.Errorf("compactEpisodeMeta() = %q", got)
	}
	if got := compactEpisodeMeta("", "", nil); got != "" {
		t.Errorf("compactEpisodeMeta(empty) = %q", got)
	}
}
