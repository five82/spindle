package auditgather

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

func TestComputeAnalysisFromDeliveredTVReport(t *testing.T) {
	r := &Report{
		Item:      ItemSummary{ID: 7, DiscTitle: "Example UHD", Stage: "completed"},
		Paths:     AuditPaths{LibraryDir: "/library", ReviewDir: "/review"},
		StageGate: StageGate{DiscSource: "bluray", PhaseEncoded: true, PhaseSubtitles: true},
		Logs: &LogAnalysis{Decisions: []LogDecision{
			{DecisionType: logs.DecisionFileProbe, DecisionResult: "found", DecisionReason: "resolution=3840x2160 codecs=hevc,ac3", Message: "input inspected"},
			{DecisionType: logs.DecisionAudioSelection, DecisionResult: "selected", DecisionReason: "primary english", Message: "audio selected"},
		}, Stages: []StageEvent{{Stage: "encoding", EventType: "stage_start", TS: "start"}, {Stage: "encoding", EventType: "stage_complete", TS: "end", DurationSeconds: 12}}},
		Envelope: &ripspec.Envelope{
			Metadata:   ripspec.Metadata{MediaType: "tv"},
			Episodes:   []ripspec.Episode{{Key: "s01e01", Season: 1, Episode: 1}, {Key: "s01e02", Season: 1, Episode: 2, NeedsReview: true}},
			Assets:     ripspec.Assets{Final: []ripspec.Asset{{EpisodeKey: "s01e01", Path: "/library/show/e01.mkv", Status: ripspec.AssetStatusCompleted}, {EpisodeKey: "s01e02", Path: "/review/show/e02.mkv", Status: ripspec.AssetStatusCompleted}}},
			Attributes: ripspec.EnvelopeAttributes{SubtitleGenerationResults: []ripspec.SubtitleGenRecord{{EpisodeKey: "s01e01", Source: "opensubtitles", Language: "en", Segments: 8, ValidationResult: "passed"}, {EpisodeKey: "s01e02", Source: "none"}}},
		},
		Media: []MediaFileProbe{
			{Role: "final", EpisodeKey: "s01e01", Path: "/library/show/e01.mkv", DurationSeconds: 1800, SizeBytes: 1024, Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: "av1", Width: 3840, Height: 2160, ColorTransfer: "smpte2084"}, {Index: 1, CodecType: "audio", CodecName: "opus", Channels: 2, Tags: map[string]string{"language": "eng"}}, {Index: 2, CodecType: "subtitle", CodecName: "subrip", Tags: map[string]string{"language": "eng"}}}}},
			{Role: "final", EpisodeKey: "s01e02", Path: "/review/show/e02.mkv", DurationSeconds: 1810, SizeBytes: 1100, Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: "av1", Width: 3840, Height: 2160}, {Index: 1, CodecType: "audio", CodecName: "opus", Channels: 2}}}},
		},
	}
	a := computeAnalysis(r)
	if a.SourceSummary == nil || !a.SourceSummary.UHDLikely || a.SourceSummary.InputResolution != "3840x2160" || a.SourceSummary.OutputCodec != "av1" {
		t.Fatalf("source: %+v", a.SourceSummary)
	}
	if len(a.DecisionGroups) != 2 || len(a.NotableDecisions) != 2 || len(a.StageTimings) != 1 {
		t.Fatalf("decisions or timing: %+v", a)
	}
	if a.RoutingSummary == nil || len(a.RoutingSummary.Entries) != 2 || !a.RoutingSummary.Entries[1].MatchesExpected {
		t.Fatalf("routing: %+v", a.RoutingSummary)
	}
	if a.AudioSummary == nil || a.SubtitleSummary == nil || a.EpisodeConsistency == nil || a.MediaStats == nil || a.AssetHealth == nil || a.EpisodeStats == nil {
		t.Fatalf("incomplete analysis: %+v", a)
	}
	if len(a.OutputMedia) != 2 {
		t.Fatalf("media: %+v", a.OutputMedia)
	}
}

func TestGatherHandlesIncompleteAndMalformedItem(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{StateDir: dir, LibraryDir: filepath.Join(dir, "library"), ReviewDir: filepath.Join(dir, "review")}}
	item := &httpapi.ItemResponse{ID: 123, DiscTitle: "Example", Stage: "identification", RipSpec: json.RawMessage(`{"version":1,"metadata":{"media_type":"movie"}}`)}
	r, err := Gather(context.Background(), cfg, item)
	if err != nil || r == nil || r.Analysis == nil || r.Envelope == nil || r.Item.ID != 123 {
		t.Fatalf("gather: %+v %v", r, err)
	}
	item.RipSpec = json.RawMessage(`{broken`)
	r, err = Gather(context.Background(), cfg, item)
	if err != nil || r == nil || len(r.Errors) == 0 || !strings.Contains(r.Errors[0], "parse envelope") {
		t.Fatalf("malformed: %+v %v", r, err)
	}
	if _, err := Gather(context.Background(), cfg, nil); err == nil {
		t.Fatal("nil item accepted")
	}
	// Only decisions from this item may enter the report.
	logPath := filepath.Join(cfg.DaemonLogDir(), "spindle-20260101T000000.000Z.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"time":"2026-01-01T00:00:00Z","level":"INFO","msg":"other","item_id":999,"decision_type":"tmdb_match","decision_result":"selected"}` + "\n" +
		`{"time":"2026-01-01T00:00:01Z","level":"INFO","msg":"matched","item_id":123,"decision_type":"tmdb_match","decision_result":"selected","decision_reason":"high confidence"}` + "\n"
	if err := os.WriteFile(logPath, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = Gather(context.Background(), cfg, item)
	if err != nil || r.Logs == nil || len(r.Logs.Decisions) != 1 || r.Logs.Decisions[0].Message != "matched" {
		t.Fatalf("filtered logs: %+v %v", r.Logs, err)
	}
}
