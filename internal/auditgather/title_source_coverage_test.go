package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

func TestSourceSummaryAndTitleSelectionWithFallbackEvidence(t *testing.T) {
	if got := computeSourceSummary(nil); got != nil {
		t.Fatalf("nil report: %+v", got)
	}
	if got := computeSourceSummary(&Report{}); got != nil {
		t.Fatalf("empty evidence: %+v", got)
	}
	report := &Report{Item: ItemSummary{DiscTitle: "Feature UHD"}, Envelope: &ripspec.Envelope{
		Metadata: ripspec.Metadata{MediaType: "movie"},
		Titles:   []ripspec.Title{{ID: 1, Duration: 4000, Chapters: 10}, {ID: 2, Duration: 4010, Chapters: 12}, {ID: 3, Duration: 500, Chapters: 1}},
		Assets:   ripspec.Assets{Ripped: []ripspec.Asset{{EpisodeKey: "main", TitleID: 2, Path: "/tmp/ripped.mkv", Status: ripspec.AssetStatusCompleted}}},
	}}
	source := computeSourceSummary(report)
	if source == nil || !source.UHDLikely {
		t.Fatalf("UHD label evidence: %+v", source)
	}
	selection := computeTitleSelection(report)
	if selection.SelectedID != 2 || selection.FeatureCandidateCount != 2 || selection.SimilarRuntimeCount != 2 {
		t.Fatalf("fallback ripped title: %+v", selection)
	}
	report.Logs = &LogAnalysis{Decisions: []LogDecision{{DecisionType: logs.DecisionTitleSelection, DecisionResult: "title 1", DecisionReason: "feature"}}}
	selection = computeTitleSelection(report)
	if selection.SelectedID != 1 || !selection.Candidates[0].Selected || selection.DecisionReason != "feature" {
		t.Fatalf("logged selection: %+v", selection)
	}
	if _, ok := parseSelectedTitleID("unknown"); ok {
		t.Fatal("no title ID accepted")
	}
	if _, ok := parseSelectedTitleID("title not-a-number"); ok {
		t.Fatal("malformed title ID accepted")
	}
	if got := decisionReasonValue("resolution=3840x2160, codec=hevc", "resolution"); got != "3840x2160" {
		t.Fatalf("parsed reason: %s", got)
	}
	report.Logs.Decisions = []LogDecision{{DecisionType: logs.DecisionFileProbe, Extras: map[string]any{"resolution": "1920x1080", "codecs": "h264,ac3"}}}
	report.Media = []MediaFileProbe{{Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "audio"}, {CodecType: "video", CodecName: "av1", Width: 1920, Height: 1080}}}}}
	source = computeSourceSummary(report)
	if source.InputResolution != "1920x1080" || len(source.InputCodecs) != 2 || source.OutputResolution != "1920x1080" || source.OutputCodec != "av1" {
		t.Fatalf("probe and log evidence: %+v", source)
	}
}
