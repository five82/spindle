package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

func TestShouldAutoExpandEpisodes(t *testing.T) {
	tests := []struct {
		name     string
		item     spindle.QueueItem
		episodes []spindle.EpisodeStatus
		totals   spindle.EpisodeTotals
		want     bool
	}{
		{
			name:     "small sets expand",
			episodes: make([]spindle.EpisodeStatus, 3),
			totals:   spindle.EpisodeTotals{Planned: 3},
			want:     true,
		},
		{
			name:     "failed episodes expand",
			episodes: []spindle.EpisodeStatus{{Key: "a"}, {Key: "b", Status: "failed"}},
			totals:   spindle.EpisodeTotals{Planned: 2},
			want:     true,
		},
		{
			name:     "incomplete matching expands",
			item:     spindle.QueueItem{EpisodeIdentifiedCount: 5},
			episodes: make([]spindle.EpisodeStatus, 10),
			totals:   spindle.EpisodeTotals{Planned: 10},
			want:     true,
		},
		{
			name:     "final mismatch expands",
			item:     spindle.QueueItem{Stage: "completed"},
			episodes: make([]spindle.EpisodeStatus, 10),
			totals:   spindle.EpisodeTotals{Planned: 10, Final: 9},
			want:     true,
		},
		{
			name:     "large healthy set stays collapsed",
			episodes: make([]spindle.EpisodeStatus, 10),
			totals:   spindle.EpisodeTotals{Planned: 10, Final: 10},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoExpandEpisodes(tc.item, tc.episodes, tc.totals); got != tc.want {
				t.Fatalf("shouldAutoExpandEpisodes() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchedEpisodeCount(t *testing.T) {
	episodes := []spindle.EpisodeStatus{{Episode: 1}, {MatchedEpisode: 2}, {Key: "x"}}
	if got := matchedEpisodeCount(spindle.QueueItem{}, episodes); got != 2 {
		t.Fatalf("matchedEpisodeCount() = %d, want 2", got)
	}
	if got := matchedEpisodeCount(spindle.QueueItem{EpisodeIdentifiedCount: 5}, episodes); got != 3 {
		t.Fatalf("matchedEpisodeCount() with explicit count = %d, want 3", got)
	}
}

func TestToggleEpisodesCollapsed_UsesEffectiveDefaultState(t *testing.T) {
	m := New(Options{ThemeName: "slate"})
	episodes := make([]spindle.EpisodeStatus, 10)
	for i := range episodes {
		episodes[i].FinalPath = "/final.mkv"
	}
	m.snapshot.Queue = []spindle.QueueItem{{
		ID:       1,
		Episodes: episodes,
	}}

	item := m.getSelectedItem()
	if item == nil {
		t.Fatal("getSelectedItem() = nil")
		return
	}
	episodes, totals := item.EpisodeSnapshot()
	if !m.isEpisodesCollapsed(*item, episodes, totals) {
		t.Fatal("isEpisodesCollapsed() before toggle = false, want true")
	}

	m.inspectedID = 1
	m.toggleInspectedEpisodes()

	item = m.getSelectedItem()
	if item == nil {
		t.Fatal("getSelectedItem() after toggle = nil")
		return
	}
	episodes, totals = item.EpisodeSnapshot()
	if m.isEpisodesCollapsed(*item, episodes, totals) {
		t.Fatal("isEpisodesCollapsed() after one toggle = true, want false")
	}
}

func TestActiveEpisodeIndex(t *testing.T) {
	episodes := []spindle.EpisodeStatus{{Key: "a", Active: true}, {Key: "b"}}
	idx, ok := activeEpisodeIndex(spindle.QueueItem{}, episodes)
	if !ok || idx != 0 {
		t.Fatalf("activeEpisodeIndex() = (%d, %v), want (0, true) for Active flag", idx, ok)
	}

	episodes = []spindle.EpisodeStatus{{Key: "a"}, {Key: "b"}}
	item := spindle.QueueItem{Tasks: []spindle.Task{
		{Type: "encoding", State: "running", ActiveAssetKey: "B"},
	}}
	idx, ok = activeEpisodeIndex(item, episodes)
	if !ok || idx != 1 {
		t.Fatalf("activeEpisodeIndex() = (%d, %v), want (1, true) matched via task's active asset key", idx, ok)
	}

	idx, ok = activeEpisodeIndex(spindle.QueueItem{}, []spindle.EpisodeStatus{{Key: "a"}})
	if ok {
		t.Fatalf("activeEpisodeIndex() = (%d, %v), want (_, false) when nothing is active", idx, ok)
	}
}

func TestDescribeEpisodeHelpers(t *testing.T) {
	ep := spindle.EpisodeStatus{MatchScore: 0.93}
	if got := describeEpisodeMapping(ep); got != "Match 0.93" {
		t.Fatalf("describeEpisodeMapping() = %q, want %q", got, "Match 0.93")
	}
	ep = spindle.EpisodeStatus{NeedsReview: true, ReviewReason: "subtitle no-match"}
	if got := describeEpisodeIssue(ep); got != "subtitle no-match" {
		t.Fatalf("describeEpisodeIssue() = %q, want %q", got, "subtitle no-match")
	}
	ep = spindle.EpisodeStatus{NeedsReview: true}
	if got := describeEpisodeIssue(ep); got != "needs review" {
		t.Fatalf("describeEpisodeIssue() default reason = %q, want %q", got, "needs review")
	}
	ep = spindle.EpisodeStatus{}
	if got := describeEpisodeIssue(ep); got != "" {
		t.Fatalf("describeEpisodeIssue() healthy = %q, want empty", got)
	}
	ep = spindle.EpisodeStatus{Status: "failed"}
	if got := describeEpisodeIssue(ep); got != "failed" {
		t.Fatalf("describeEpisodeIssue() failed = %q, want %q", got, "failed")
	}
}

func TestEpisodeAssetStates(t *testing.T) {
	tests := []struct {
		name   string
		ep     spindle.EpisodeStatus
		active bool
		want   [4]episodeAssetState
	}{
		{
			name: "all pending",
			ep:   spindle.EpisodeStatus{},
			want: [4]episodeAssetState{episodeAssetPending, episodeAssetPending, episodeAssetPending, episodeAssetPending},
		},
		{
			name:   "next pending column is active",
			ep:     spindle.EpisodeStatus{RippedPath: "/r.mkv", EncodedPath: "/e.mkv"},
			active: true,
			want:   [4]episodeAssetState{episodeAssetDone, episodeAssetDone, episodeAssetActive, episodeAssetPending},
		},
		{
			name: "failed episode marks next column failed",
			ep:   spindle.EpisodeStatus{RippedPath: "/r.mkv", Status: "failed"},
			want: [4]episodeAssetState{episodeAssetDone, episodeAssetFailed, episodeAssetPending, episodeAssetPending},
		},
		{
			name: "failed with nothing done yet marks first column",
			ep:   spindle.EpisodeStatus{Status: "failed"},
			want: [4]episodeAssetState{episodeAssetFailed, episodeAssetPending, episodeAssetPending, episodeAssetPending},
		},
		{
			name: "all done leaves nothing active or failed",
			ep: spindle.EpisodeStatus{
				RippedPath:    "/r.mkv",
				EncodedPath:   "/e.mkv",
				SubtitledPath: "/s.mkv",
				FinalPath:     "/f.mkv",
			},
			active: true,
			want:   [4]episodeAssetState{episodeAssetDone, episodeAssetDone, episodeAssetDone, episodeAssetDone},
		},
		{
			name:   "active without any done marks first column",
			ep:     spindle.EpisodeStatus{},
			active: true,
			want:   [4]episodeAssetState{episodeAssetActive, episodeAssetPending, episodeAssetPending, episodeAssetPending},
		},
		{
			name:   "not active and not failed leaves next column pending",
			ep:     spindle.EpisodeStatus{RippedPath: "/r.mkv"},
			active: false,
			want:   [4]episodeAssetState{episodeAssetDone, episodeAssetPending, episodeAssetPending, episodeAssetPending},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := episodeAssetStates(tc.ep, tc.active); got != tc.want {
				t.Fatalf("episodeAssetStates() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRenderEpisodeAssetGrid_ContainsColumnGlyphs(t *testing.T) {
	m := Model{theme: GetTheme("slate")}
	styles := m.theme.Styles()
	ep := spindle.EpisodeStatus{RippedPath: "/r.mkv", EncodedPath: "/e.mkv"}
	got := renderEpisodeAssetGrid(ep, true, styles)
	for _, want := range []string{"R✓", "E✓", "S◉", "F·"} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderEpisodeAssetGrid() = %q, want to contain %q", got, want)
		}
	}
}

func TestDescribeItemFileStates_DerivesTotals(t *testing.T) {
	m := Model{theme: GetTheme("slate")}
	item := spindle.QueueItem{
		Episodes: []spindle.EpisodeStatus{{
			RippedPath:    "/r.mkv",
			EncodedPath:   "/e.mkv",
			SubtitledPath: "/s.mkv",
			FinalPath:     "/f.mkv",
		}},
	}
	if got := m.describeItemFileStates(item); got != "RIP ENC SUB FIN" {
		t.Fatalf("describeItemFileStates() = %q, want %q", got, "RIP ENC SUB FIN")
	}
}
