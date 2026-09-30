package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestInventoryNeverCollapses(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	item := spindle.QueueItem{ID: 1}
	for i := range 12 {
		item.Episodes = append(item.Episodes, spindle.EpisodeStatus{Key: fmt.Sprint(i), SourceTitleID: i, Title: fmt.Sprintf("Source-%02d", i)})
	}
	for _, details := range []bool{false, true} {
		m.detailState.episodeCollapsed[1] = details
		var b strings.Builder
		_, totals := item.EpisodeSnapshot()
		m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
		for i := range 12 {
			if !strings.Contains(b.String(), fmt.Sprintf("Source-%02d", i)) {
				t.Fatalf("inventory hidden: %s", b.String())
			}
		}
	}
}

func TestActivityUsesAllTaskOwnersNotMissingArtifacts(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	m.width = 80
	item := spindle.QueueItem{ID: 1, Episodes: []spindle.EpisodeStatus{{Key: "a", SourceTitleID: 3}, {Key: "b", SourceTitleID: 1, RippedPath: "rip"}}, Tasks: []spindle.Task{
		{Type: "ripping", State: "running", ActiveAssetKey: "a", Activities: []spindle.Activity{{State: "running", AssetKey: "a", Message: "Optical read"}}},
		{Type: "encoding", State: "running", ActiveAssetKey: "b", Activities: []spindle.Activity{{State: "running", AssetKey: "b", Message: "Video probes"}}},
		{Type: "analysis", State: "running", Activities: []spindle.Activity{{State: "running", AssetKey: "b", Message: "Classifying audio"}}},
	}}
	var b strings.Builder
	_, totals := item.EpisodeSnapshot()
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	got := stripANSI(b.String())
	for _, want := range []string{"> Title 03", "> Title 01", "Optical read", "Video probes", "Classifying audio", "rip ✓  encode ○  output ○", "Subtitle: not checked"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "S01E00") {
		t.Fatal(got)
	}
}

func TestEpisodeIdentityAndSubtitleOutcomes(t *testing.T) {
	for _, tc := range []struct {
		ep             spindle.EpisodeStatus
		label, outcome string
	}{
		{spindle.EpisodeStatus{Key: "main", SubtitleSource: "none", SubtitleSkipReason: "no match"}, "File", "skipped: no match"},
		{spindle.EpisodeStatus{Season: 1, Episode: 1, EpisodeEnd: 2, SubtitleSource: "opensubtitles"}, "S01E01-E02", "SRT adopted from opensubtitles; waiting for Apply"},
		{spindle.EpisodeStatus{SourceTitleID: 4, SubtitleSource: "opensubtitles", SubtitledPath: "sub"}, "Title 04", "SRT applied (opensubtitles)"},
	} {
		if got := formatEpisodeLabel(tc.ep); got != tc.label {
			t.Errorf("label %q", got)
		}
		if got := subtitleOutcome(tc.ep); got != tc.outcome {
			t.Errorf("subtitle %q", got)
		}
	}
}

func TestMatchedEpisodeCount(t *testing.T) {
	episodes := []spindle.EpisodeStatus{{Episode: 1}, {MatchedEpisode: 2}, {Key: "x"}}
	if got := matchedEpisodeCount(spindle.QueueItem{}, episodes); got != 2 {
		t.Fatal(got)
	}
	if got := matchedEpisodeCount(spindle.QueueItem{EpisodeIdentifiedCount: 5}, episodes); got != 3 {
		t.Fatal(got)
	}
}

func TestToggleDetailsCollapsedUsesEffectiveDefaultState(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	item := spindle.QueueItem{ID: 1, Episodes: []spindle.EpisodeStatus{{Key: "main"}}}
	m.snapshot.Queue = []spindle.QueueItem{item}
	m.inspectedID = 1
	if !m.isEpisodesCollapsed(item, nil, spindle.EpisodeTotals{}) {
		t.Fatal("secondary details should default closed")
	}
	m.toggleInspectedEpisodes()
	if m.isEpisodesCollapsed(item, nil, spindle.EpisodeTotals{}) {
		t.Fatal("toggle did not expand details")
	}
}

func TestEpisodeRowShowsFileGlyphsAndActiveProgress(t *testing.T) {
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	m.width = 120
	item := spindle.QueueItem{ID: 1, Episodes: []spindle.EpisodeStatus{{Key: "a", SourceTitleID: 1, RippedPath: "rip"}}, Tasks: []spindle.Task{
		{Type: "encoding", State: "running", ActiveAssetKey: "a", Activities: []spindle.Activity{{State: "running", AssetKey: "a", Message: "Video frame progress", Completed: 55, Total: 100, Unit: "frames"}}},
	}}
	var b strings.Builder
	_, totals := item.EpisodeSnapshot()
	m.renderEpisodeList(&b, item, m.theme.Styles(), totals)
	got := stripANSI(b.String())
	for _, want := range []string{"rip ✓  encode ○  output ○  ·  Subtitle: not checked", "Encoding: Video frame progress 55% (55/100 frames)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestEpisodeDetailSummarizesReelJSON(t *testing.T) {
	quality := summarizeTargetQuality([]byte(`{"ssimu2_calibration_offset":-0.74,"metrics":[{"metric":"cvvdp","target":9.55,"tolerance":0.2,"chunks":20,"score_min":9.386,"score_mean":9.564,"final_crf_min":18.25,"final_crf_median":26,"final_crf_max":35}]}`))
	if len(quality) != 1 || quality[0] != "cvvdp 9.55±0.20: mean 9.56, min 9.39; CRF 18.25-35 (median 26); 20 chunks" {
		t.Fatalf("quality = %q", quality)
	}
	grain := summarizeGrain([]byte(`{"mode":"auto","treated":true,"denoise":"fftdnoiz","median_bpp":0.3171,"treatment_bpp_cutoff":0.22,"denoise_ceiling_jod_mean":9.8994,"denoise_ceiling_jod_min":9.8684,"sample_bpp":[0.34,0.22]}`))
	if grain != "treated (fftdnoiz); median bpp 0.317 vs cutoff 0.22; denoise ceiling 9.90 JOD (min 9.87)" {
		t.Fatalf("grain = %q", grain)
	}
	if got := summarizeGrain([]byte(`{"treated":false,"reason":"SD source"}`)); got != "not treated; SD source" {
		t.Fatalf("untreated grain = %q", got)
	}
	if got := summarizeTargetQuality([]byte(`not json`)); len(got) != 1 || got[0] != "not json" {
		t.Fatalf("fallback = %q", got)
	}
}
