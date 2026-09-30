package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func sectionOrder(t *testing.T, got string, names ...string) {
	t.Helper()
	last := -1
	for _, name := range names {
		idx := strings.Index(got, name)
		if idx < 0 || idx < last {
			t.Fatalf("section %q missing/out of order:\n%s", name, got)
		}
		last = idx
	}
}
func overviewFor(t *testing.T, item spindle.QueueItem) string {
	t.Helper()
	m := New(Options{ThemeName: "slate", PrefsPath: t.TempDir() + "/prefs.json"})
	m.width = 100
	return stripANSI(m.renderDetailContent(item, 100))
}

func TestOverviewStableSkeletonAndConcurrentWork(t *testing.T) {
	started := time.Now().Add(-time.Minute).Format(time.RFC3339)
	item := spindle.QueueItem{ID: 1, Stage: "ripping", Metadata: json.RawMessage(`{"media_type":"tv"}`), CreatedAt: started,
		Episodes: []spindle.EpisodeStatus{{Key: "a"}, {Key: "b", RippedPath: "rip"}},
		Encoding: &spindle.EncodingStatus{Resolution: "1920x1080", InputFile: "Title 01"},
		Tasks: []spindle.Task{
			{Type: "ripping", State: "running", ActiveAssetKey: "a", Activities: []spindle.Activity{{Operation: "optical_read", AssetKey: "a", State: "running", Message: "Reading title 03", StartedAt: started, Completed: 25, Total: 100, Unit: "MakeMKV units"}}},
			{Type: "encoding", State: "running", ActiveAssetKey: "b", Progress: spindle.TaskProgress{Percent: 49}, Activities: []spindle.Activity{{ID: "video", Operation: "encoding", AssetKey: "b", State: "running", Message: "Accepted video frames", StartedAt: started, Completed: 47, Total: 100, Unit: "frames"}}},
			{Type: "apply", State: "pending", DependsOn: []string{"encoding", "subtitling"}},
		}}
	got := overviewFor(t, item)
	sectionOrder(t, got, "Pipeline", "Media", "Output", "episode list", "created")
	for _, want := range []string{"Running Ripping", "1/2 files", "0/2 files", "Reading title 03", "47/100 frames", "Needs Encoding + Subtitling"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "49%") || strings.Contains(got, "ETA") {
		t.Fatal("unscoped percentage/ETA", got)
	}
	if plainTaskStrip(item) != "◉◉○" {
		t.Fatal(plainTaskStrip(item))
	}
}

func TestEncoderInputWaitIsNotResourceWait(t *testing.T) {
	item := spindle.QueueItem{Tasks: []spindle.Task{{Type: "encoding", State: "running", Activities: []spindle.Activity{{Operation: "input", State: "waiting", Message: "Waiting for first rip; encoder slot reserved"}}}}}
	got := overviewFor(t, item)
	if !strings.Contains(got, "Waiting for first rip; encoder slot reserved") || strings.Contains(got, "Running Encoding") {
		t.Fatal(got)
	}
	item.Tasks[0].State = "pending"
	item.Tasks[0].Activities = []spindle.Activity{{Operation: "resources", State: "waiting", Message: "Waiting for encode held by #2/encoding"}}
	if got := overviewFor(t, item); !strings.Contains(got, "held by #2") {
		t.Fatal(got)
	}
}

func TestOverviewExceptionsLeadAndDoNotHideHistory(t *testing.T) {
	for _, item := range []spindle.QueueItem{
		{Stage: "failed", ErrorMessage: "disk full", Tasks: []spindle.Task{{Type: "encoding", State: "failed", Error: "disk full", Attempts: 3}}},
		{NeedsReview: true, ReviewReasons: []string{"subtitle no-match"}},
		{Encoding: &spindle.EncodingStatus{Warning: "bit-depth fallback"}},
		{Episodes: []spindle.EpisodeStatus{{Key: "main", SubtitleSource: "none", SubtitleSkipReason: "no match"}}},
	} {
		got := overviewFor(t, item)
		sectionOrder(t, got, "Attention", "Pipeline")
		if !strings.Contains(got, "see 3 Problems") {
			t.Fatal(got)
		}
	}
}

func TestOverviewCompletedItemUsesDeliveredFacts(t *testing.T) {
	var v spindle.FinalValidation
	if err := json.Unmarshal([]byte(`{"passed":true,"av_sync":{"passed":true}}`), &v); err != nil {
		t.Fatal(err)
	}
	item := spindle.QueueItem{Stage: "completed", CreatedAt: "2026-07-05T10:00:00Z", UpdatedAt: "2026-07-05T12:30:00Z",
		Episodes: []spindle.EpisodeStatus{{Key: "main", FinalPath: "/library/Air.mkv", FinalSizeBytes: 142 << 20, FinalValidation: &v, EncodeStats: &spindle.EncodeStats{EncodedSizeBytes: 190 << 20, EncodeSeconds: 60}, SubtitleSource: "opensubtitles", SubtitledPath: "sub"}},
		Tasks:    []spindle.Task{{Type: "encoding", State: "done"}}, Encoding: &spindle.EncodingStatus{EncodedSize: 999, Validation: &spindle.EncodingValidation{Passed: true}}}
	got := overviewFor(t, item)
	sectionOrder(t, got, "Pipeline", "Output", "created")
	for _, want := range []string{"142.00 MiB delivered", "190.00 MiB (before Apply)", "Checks   1 passed", "/library/Air.mkv", "Elapsed 2h 30m", "OpenSubtitles SRT"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Attention") {
		t.Fatal(got)
	}
}

func TestOverviewMediaScopeAndQuality(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{Encoding: &spindle.EncodingStatus{InputFile: "feature.mkv", Resolution: "3840x2160", OutputResolution: "3840x2080", DynamicRange: "HDR", Encoder: "SVT-AV1", Preset: "6", Tune: "0", Quality: "CVVDP target 9.15-9.55 JOD (initial CRF 26, metric workers 4)"}})
	for _, want := range []string{"3840x2160 -> 3840x2080 cropped HDR (feature.mkv)", "SVT-AV1 preset 6", "CVVDP target 9.15-9.55 JOD"} {
		if !strings.Contains(got, want) {
			t.Error(got)
		}
	}
	if strings.Count(got, "feature.mkv") != 1 {
		t.Fatalf("input file must be named once: %s", got)
	}
	if strings.Contains(got, "initial CRF") {
		t.Fatal(got)
	}
}
func TestSummarizeQuality(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"CVVDP target 9.15-9.55 JOD (initial CRF 26)", "CVVDP target 9.15-9.55 JOD"}, {"SSIMULACRA2 target 82-86 (metric workers 4)", "SSIMULACRA2 target 82-86"}, {"CRF 26 (UHD)", "CRF 26 (UHD)"}, {"", ""}} {
		if got := summarizeQuality(tc.in); got != tc.want {
			t.Fatalf("%q != %q", got, tc.want)
		}
	}
}
func TestOverviewTaskDurationsAndOverlap(t *testing.T) {
	got := overviewFor(t, spindle.QueueItem{Stage: "completed", CreatedAt: "2026-07-05T10:00:00Z", UpdatedAt: "2026-07-05T12:00:00Z", Tasks: []spindle.Task{
		{Type: "identification", State: "done", StartedAt: "2026-07-05T10:00:00Z", FinishedAt: "2026-07-05T10:00:00Z"},
		{Type: "ripping", State: "done", StartedAt: "2026-07-05T10:00:00Z", FinishedAt: "2026-07-05T11:30:00Z"},
		{Type: "encoding", State: "done", StartedAt: "2026-07-05T10:30:00Z", FinishedAt: "2026-07-05T12:00:00Z"},
	}})
	if !strings.Contains(got, "<1s") || !strings.Contains(got, "Elapsed 2h 0m (stages overlap)") {
		t.Fatal(got)
	}
}
func TestOverviewTVDiscAndSourceSummary(t *testing.T) {
	item := spindle.QueueItem{DiscNumber: 2, Metadata: json.RawMessage(`{"media_type":"tv"}`), Episodes: make([]spindle.EpisodeStatus, 4)}
	got := overviewFor(t, item)
	sectionOrder(t, got, "Pipeline", "Output")
	// The disc number is identity: the item band carries it, not Media.
	if strings.Contains(got, "Disc ") || !strings.Contains(got, "0/4 ripped") || !strings.Contains(got, "Press 2 for the episode list") {
		t.Fatal(got)
	}
}
func TestCountsNeverUseManifestPosition(t *testing.T) {
	for _, key := range []string{"a", "b", "c"} {
		item := spindle.QueueItem{Episodes: []spindle.EpisodeStatus{{Key: "a"}, {Key: "b", RippedPath: "rip"}, {Key: "c"}}, Tasks: []spindle.Task{{Type: "encoding", State: "running", ActiveAssetKey: key, Progress: spindle.TaskProgress{Percent: 49}}, {Type: "ripping", State: "running", ActiveAssetKey: key}}}
		got := strings.Join(strings.Fields(overviewFor(t, item)), " ")
		if !strings.Contains(got, "Encoding 0/3 files") || !strings.Contains(got, "Ripping 1/3 files") {
			t.Fatal(got)
		}
	}
}
func TestMilestonePercentNeverGetsBar(t *testing.T) {
	for _, stage := range []string{"subtitling", "apply", "episode_identification"} {
		got := overviewFor(t, spindle.QueueItem{Tasks: []spindle.Task{{Type: stage, State: "running", Progress: spindle.TaskProgress{Percent: 35, Message: "Working"}}}})
		if !strings.Contains(got, "Working") || strings.Contains(got, "35%") || strings.Contains(got, "█") {
			t.Fatal(got)
		}
	}
}
func TestSubtitleCompletedCountsIncludeSkipsNotMux(t *testing.T) {
	for _, g := range []*spindle.SubtitleGenerationStatus{{OpenSubtitles: 7}, {OpenSubtitles: 4, Skipped: 3}, {Skipped: 7}} {
		got := overviewFor(t, spindle.QueueItem{Episodes: make([]spindle.EpisodeStatus, 7), SubtitleGeneration: g, Tasks: []spindle.Task{{Type: "subtitling", State: "done"}, {Type: "apply", State: "pending"}}})
		if !strings.Contains(got, "7/7 files") || strings.Contains(got, "7 applied") {
			t.Fatal(got)
		}
	}
}
func TestWrapText(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{{"hello world", 20, "hello world"}, {"alpha beta gamma", 11, "alpha beta|gamma"}, {"abcdefghij", 4, "abcd|efgh|ij"}, {"hello", 0, "hello"}, {"", 10, ""}, {"\u754c\u754c\u754c", 4, "\u754c\u754c|\u754c"}} {
		if got := strings.Join(wrapText(tc.in, tc.width), "|"); got != tc.want {
			t.Fatalf("%q != %q", got, tc.want)
		}
	}
}

// Counts agree in number, and zero matching outcomes stay silent.
func TestOverviewPluralsAndMatchingCounts(t *testing.T) {
	item := spindle.QueueItem{
		ContentID: &spindle.ContentID{Method: "jev", MatchedEpisodes: 3},
		Episodes:  []spindle.EpisodeStatus{{Key: "a", EncodeStats: &spindle.EncodeStats{EncodeSeconds: 60}}},
	}
	got := overviewFor(t, item)
	if !strings.Contains(got, "Encode   1 file;") || !strings.Contains(got, "3 matched (via jev)") || strings.Contains(got, "unresolved") {
		t.Fatal(got)
	}
	item.ContentID.ReviewEpisodes = 1
	if got := overviewFor(t, item); !strings.Contains(got, "3 matched; 1 for review (via jev)") {
		t.Fatal(got)
	}
}
