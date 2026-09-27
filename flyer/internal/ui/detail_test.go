package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

// sectionOrder asserts that each name appears in got, in the given order.
func sectionOrder(t *testing.T, got string, names ...string) {
	t.Helper()
	last := -1
	for _, name := range names {
		idx := strings.Index(got, name)
		if idx == -1 {
			t.Fatalf("overview missing section %q, got:\n%s", name, got)
		}
		if idx < last {
			t.Fatalf("section %q out of order, got:\n%s", name, got)
		}
		last = idx
	}
}

func overviewFor(t *testing.T, item spindle.QueueItem) string {
	t.Helper()
	m := New(Options{ThemeName: "slate"})
	return stripANSI(m.renderDetailContent(item, 100))
}

func TestOverviewActiveItem_FixedSkeleton(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:    1,
		Stage: "encoding",
		Tasks: []spindle.Task{
			{Type: "ripping", State: "done"},
			{Type: "encoding", State: "running", Progress: spindle.TaskProgress{Percent: 42, Message: "pass 1"}},
		},
		Encoding: &spindle.EncodingStatus{
			Percent:             42,
			Resolution:          "1920x1080",
			Preset:              "6",
			Encoder:             "svt-av1",
			EstimatedTotalBytes: 4 << 30,
		},
		PrimaryAudioDescription: "TrueHD 7.1",
	})

	sectionOrder(t, got, "Pipeline", "Media", "Output")
	if strings.Contains(got, "Attention") {
		t.Fatalf("healthy item must not render Attention, got:\n%s", got)
	}
	for _, want := range []string{"Encoding", "42%", "1920x1080", "TrueHD 7.1", "svt-av1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("overview missing %q, got:\n%s", want, got)
		}
	}
}

func TestOverviewFailedItem_AttentionFirst(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:           2,
		Stage:        "failed",
		ErrorMessage: "ffmpeg exited 1",
		Tasks: []spindle.Task{
			{Type: "encoding", State: "failed", Error: "ffmpeg exited 1", Attempts: 3},
		},
	})

	sectionOrder(t, got, "Attention", "Pipeline")
	if !strings.Contains(got, "ffmpeg exited 1") {
		t.Fatalf("overview missing error message, got:\n%s", got)
	}
}

func TestOverviewReviewItem_ShowsReasons(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:            3,
		Stage:         "encoding",
		NeedsReview:   true,
		ReviewReasons: []string{"subtitle no-match"},
	})

	sectionOrder(t, got, "Attention", "Pipeline")
	if !strings.Contains(got, "subtitle no-match") {
		t.Fatalf("overview missing review reason, got:\n%s", got)
	}
}

func TestOverviewEncoderWarning_RendersInAttention(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:    6,
		Stage: "encoding",
		Encoding: &spindle.EncodingStatus{
			Warning: "bit-depth fallback: encoding at 8-bit",
		},
	})

	sectionOrder(t, got, "Attention", "Warning", "Pipeline")
	if !strings.Contains(got, "bit-depth fallback: encoding at 8-bit") {
		t.Fatalf("overview missing encoder warning, got:\n%s", got)
	}
}

func TestOverviewCompletedItem_OutputResults(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:        4,
		Stage:     "completed",
		CreatedAt: "2026-07-05T10:00:00Z",
		UpdatedAt: "2026-07-05T12:30:00Z",
		Tasks: []spindle.Task{
			{Type: "encoding", State: "done"},
		},
		Episodes: []spindle.EpisodeStatus{
			{Key: "main", Episode: 0, FinalPath: "/library/movies/Avatar (2009)/Avatar (2009).mkv"},
		},
		Encoding: &spindle.EncodingStatus{
			OriginalSize:          20 << 30,
			EncodedSize:           5 << 30,
			SizeReductionPercent:  75,
			AverageSpeed:          3.2,
			EncodeDurationSeconds: 3600,
			Validation: &spindle.EncodingValidation{
				Passed: true,
				Steps:  []spindle.EncodingValidationStep{{Name: "duration", Passed: true}},
			},
		},
	})

	sectionOrder(t, got, "Pipeline", "Output", "created")
	for _, want := range []string{
		"75% reduction", "3.2x avg", "Passed · 1/1",
		"✓ duration", // passed runs list the named checks
		"/library/movies/Avatar (2009)/Avatar (2009).mkv",
		"Elapsed 2h 30m",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("overview missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Attention") {
		t.Fatalf("completed healthy item must not render Attention, got:\n%s", got)
	}
}

func TestOverviewMediaRows_CuratedFormatting(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:    7,
		Stage: "encoding",
		Encoding: &spindle.EncodingStatus{
			Resolution:   "3840x2160",
			DynamicRange: "hdr",
			CropRequired: true,
			CropFilter:   "crop=3840:2080:0:40",
			Encoder:      "SVT-AV1",
			Preset:       "6",
			Tune:         "0",
			Quality:      "CVVDP target 9.15-9.55 JOD (initial CRF 26 with adaptive priors, whole-chunk probes, CRF search 4.25-63.75, metric workers 4)",
		},
		PrimaryAudioDescription: "English | opus | 8ch | Surround 7.1",
	})

	for _, want := range []string{
		"3840x2160 -> 3840x2080 HDR (cropped)",
		"SVT-AV1 · Preset 6 · Tune 0",
		"Quality  CVVDP target 9.15-9.55 JOD",
		"English · opus · 8ch · Surround 7.1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("overview missing %q, got:\n%s", want, got)
		}
	}
	for _, reject := range []string{"3840:2080:0:40", "initial CRF", "metric workers"} {
		if strings.Contains(got, reject) {
			t.Fatalf("overview must not render %q, got:\n%s", reject, got)
		}
	}
}

func TestSummarizeQuality(t *testing.T) {
	tests := []struct{ in, want string }{
		{
			"CVVDP target 9.15-9.55 JOD (initial CRF 26 with adaptive priors, whole-chunk probes, CRF search 4.25-63.75, metric workers 4)",
			"CVVDP target 9.15-9.55 JOD",
		},
		{
			"SSIMULACRA2 target 82.0-86.0 (auto for SDR <=1080p; initial CRF 26 with adaptive priors, whole-chunk probes, CRF search 4.25-63.75, metric workers 4)",
			"SSIMULACRA2 target 82.0-86.0",
		},
		// Fixed-CRF mode's parenthetical is the resolution tier, not
		// search machinery: keep it.
		{"CRF 26 (UHD)", "CRF 26 (UHD)"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := summarizeQuality(tc.in); got != tc.want {
			t.Fatalf("summarizeQuality(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOverviewSubSecondTaskDuration(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:    8,
		Stage: "encoding",
		Tasks: []spindle.Task{{
			Type:       "identifying",
			State:      "done",
			StartedAt:  "2026-07-05T10:00:00Z",
			FinishedAt: "2026-07-05T10:00:00Z",
		}},
	})

	if !strings.Contains(got, "<1s") {
		t.Fatalf("sub-second done task must show <1s, not a blank cell, got:\n%s", got)
	}
}

func TestOverviewElapsedMarksOverlappingStages(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:        9,
		Stage:     "completed",
		CreatedAt: "2026-07-05T10:00:00Z",
		UpdatedAt: "2026-07-05T12:00:00Z",
		Tasks: []spindle.Task{
			{Type: "ripping", State: "done", StartedAt: "2026-07-05T10:00:00Z", FinishedAt: "2026-07-05T11:30:00Z"},
			{Type: "encoding", State: "done", StartedAt: "2026-07-05T10:30:00Z", FinishedAt: "2026-07-05T12:00:00Z"},
		},
	})

	if !strings.Contains(got, "Elapsed 2h 0m (stages overlap)") {
		t.Fatalf("overlapping task durations must be annotated, got:\n%s", got)
	}
}

func TestOverviewTVItem_ShowsDiscNumber(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:         5,
		Stage:      "ripping",
		DiscNumber: 2,
		Metadata:   []byte(`{"media_type":"tv","season_number":1}`),
	})

	sectionOrder(t, got, "Pipeline", "Media", "Episodes")
	if !strings.Contains(got, "Disc     2") {
		t.Fatalf("overview missing disc number, got:\n%s", got)
	}
}

func TestOverviewOmitsUnsetDiscNumber(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:       6,
		Stage:    "encoding",
		Metadata: []byte(`{"media_type":"movie"}`),
	})

	if strings.Contains(got, "Disc     ") {
		t.Fatalf("overview unexpectedly shows an unset disc number, got:\n%s", got)
	}
}

func TestOverviewTVItem_EpisodeSummary(t *testing.T) {
	episodes := make([]spindle.EpisodeStatus, 4)
	for i := range episodes {
		episodes[i].Key = string(rune('a' + i))
		episodes[i].Episode = i + 1
	}
	got := overviewFor(t, spindle.QueueItem{
		ID:       5,
		Stage:    "ripping",
		Episodes: episodes,
	})

	sectionOrder(t, got, "Pipeline", "Episodes")
	if !strings.Contains(got, "4 planned") {
		t.Fatalf("overview missing episode summary, got:\n%s", got)
	}
}

func TestOverviewRunningStageCountUsesActiveEpisodePosition(t *testing.T) {
	episodes := make([]spindle.EpisodeStatus, 6)
	for i := range episodes {
		episodes[i].Key = string(rune('a' + i))
	}
	episodes[0].RippedPath = "/ripped/a.mkv"

	got := overviewFor(t, spindle.QueueItem{
		ID:       6,
		Stage:    "ripping",
		Episodes: episodes,
		Tasks: []spindle.Task{
			{
				Type:           "ripping",
				State:          "running",
				ActiveAssetKey: "b",
				Progress: spindle.TaskProgress{
					Percent: 24,
					Message: "Phase 2/6 - Ripping title 1",
				},
			},
		},
	})

	normalized := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(normalized, "Ripping 2/6") {
		t.Fatalf("overview running count does not follow active episode, got:\n%s", got)
	}
	if !strings.Contains(normalized, "6 planned · 1 ripped") {
		t.Fatalf("overview completion summary should remain completion-based, got:\n%s", got)
	}
}

func TestOverviewSubtitlingHidesUnreportedProgress(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:    6,
		Stage: "subtitling",
		Tasks: []spindle.Task{
			{
				Type:  "subtitling",
				State: "running",
				Progress: spindle.TaskProgress{
					Message: "Generating subtitles",
				},
			},
		},
	})

	if !strings.Contains(got, "Subtitling") || !strings.Contains(got, "Generating subtitles") {
		t.Fatalf("overview missing running subtitle activity, got:\n%s", got)
	}
	if strings.Contains(got, "0%") || strings.Contains(got, strings.Repeat("░", 20)) {
		t.Fatalf("overview shows a false zero-progress subtitle bar, got:\n%s", got)
	}
}

func TestOverviewSubtitlingShowsReportedProgress(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{
		ID:    6,
		Stage: "subtitling",
		Tasks: []spindle.Task{
			{
				Type:     "subtitling",
				State:    "running",
				Progress: spindle.TaskProgress{Percent: 35},
			},
		},
	})

	if !strings.Contains(got, "35%") || !strings.Contains(got, "█") {
		t.Fatalf("overview missing reported subtitle progress, got:\n%s", got)
	}
}

func TestOverviewSubtitlingThroughputUsesGenerationCount(t *testing.T) {
	episodes := make([]spindle.EpisodeStatus, 7)
	got := overviewFor(t, spindle.QueueItem{
		ID:                 6,
		Stage:              "encoding",
		Episodes:           episodes,
		SubtitleGeneration: &spindle.SubtitleGenerationStatus{WhisperX: 7},
		Tasks: []spindle.Task{
			{Type: "subtitling", State: "done"},
			{Type: "apply", State: "pending"},
		},
	})

	normalized := strings.Join(strings.Fields(got), " ")
	if !strings.Contains(normalized, "Subtitled 7/7") {
		t.Fatalf("overview subtitle throughput = unexpected, got:\n%s", got)
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"short passes through", "hello world", 20, []string{"hello world"}},
		{"wraps at word boundary", "alpha beta gamma", 11, []string{"alpha beta", "gamma"}},
		{"hard-splits long words", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"zero width passes through", "hello", 0, []string{"hello"}},
		{"empty input", "", 10, []string{""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapText(tc.in, tc.width)
			if len(got) != len(tc.want) {
				t.Fatalf("wrapText() = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("wrapText() = %q, want %q", got, tc.want)
				}
			}
		})
	}
}
