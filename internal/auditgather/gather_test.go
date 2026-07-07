package auditgather

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

func TestBuildItemSummaryDerivesArtifactPathsFromRipSpec(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24, DiscTitle: "Disc"}
	env := &ripspec.Envelope{
		Assets: ripspec.Assets{
			Ripped: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "first-rip.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "second-rip.mkv", Status: ripspec.AssetStatusCompleted},
			},
			Encoded: []ripspec.Asset{{EpisodeKey: "s01e02", Path: "encoded.mkv", Status: ripspec.AssetStatusCompleted}},
			Final: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "failed-final.mkv", Status: ripspec.AssetStatusFailed},
				{EpisodeKey: "s01e02", Path: "final.mkv", Status: ripspec.AssetStatusCompleted},
			},
		},
	}

	summary := buildItemSummary(item, env)

	if summary.RippedFile != "second-rip.mkv" {
		t.Fatalf("RippedFile = %q, want second-rip.mkv", summary.RippedFile)
	}
	if summary.EncodedFile != "encoded.mkv" {
		t.Fatalf("EncodedFile = %q, want encoded.mkv", summary.EncodedFile)
	}
	if summary.FinalFile != "final.mkv" {
		t.Fatalf("FinalFile = %q, want final.mkv", summary.FinalFile)
	}
}

func TestRedactRipCacheMetadataOmitsSerializedBlobs(t *testing.T) {
	meta := &ripcache.EntryMetadata{
		Version:      1,
		Fingerprint:  "abc123",
		DiscTitle:    "Disc",
		CachedAt:     time.Unix(123, 0).UTC(),
		TitleCount:   2,
		TotalBytes:   42,
		RipSpecData:  `{"large":"blob"}`,
		MetadataJSON: `{"duplicate":"metadata"}`,
	}

	redacted := redactRipCacheMetadata(meta)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("marshal redacted metadata: %v", err)
	}
	jsonText := string(data)
	if strings.Contains(jsonText, "ripspec_data") || strings.Contains(jsonText, "metadata_json") {
		t.Fatalf("redacted metadata still contains serialized blobs: %s", jsonText)
	}
	if redacted.DiscTitle != "Disc" || redacted.TitleCount != 2 || redacted.TotalBytes != 42 {
		t.Fatalf("redacted metadata lost summary fields: %+v", redacted)
	}
}

func TestParseLogLine_MatchesDiscIDCacheDecisionByItemID(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24, DiscFingerprint: "abc123", DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"disc ID cache miss","item_id":24,"decision_type":"disc_id_cache","decision_result":"miss","decision_reason":"disc_id not in cache","disc_id":"DCB2FF29F40C9CD4702BC163A3F4511A492E54A4"}`,
		item, report, time.Time{})

	if len(report.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(report.Decisions))
	}
	if report.Decisions[0].DecisionType != "disc_id_cache" {
		t.Fatalf("decision_type = %s, want disc_id_cache", report.Decisions[0].DecisionType)
	}
}

func TestParseLogLine_InferDiscSourceFromDiscDetectedLabel(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24, DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:39:39Z","level":"INFO","msg":"disc detected, starting enqueue pipeline","event_type":"disc_detected","label":"STAR TREK TNG S1 D1","disc_type":"Blu-ray","device":"/dev/sr0"}`,
		item, report, time.Time{})

	if report.InferredDiscSource != "bluray" {
		t.Fatalf("inferred_disc_source = %q, want bluray", report.InferredDiscSource)
	}
	if len(report.Events) != 1 || report.Events[0].EventType != "disc_detected" {
		t.Fatalf("expected disc_detected info event, got %+v", report.Events)
	}
}

func TestParseLogLine_CapturesInfoEventWithoutDecision(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"encoding progress","item_id":24,"event_type":"encoding_progress","percent":42.5,"eta_seconds":3600}`,
		item, report, time.Time{})

	if len(report.Events) != 1 {
		t.Fatalf("expected 1 info event, got %d", len(report.Events))
	}
	if report.Events[0].EventType != "encoding_progress" {
		t.Fatalf("event_type = %q, want encoding_progress", report.Events[0].EventType)
	}
	if report.Events[0].Extras["percent"] != 42.5 {
		t.Fatalf("percent extra = %v, want 42.5", report.Events[0].Extras["percent"])
	}
}

func TestParseLogLine_InferMediaHintFromTMDBSearchDecision(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"media type hint detected","item_id":24,"decision_type":"tmdb_search","decision_result":"tv","decision_reason":"raw_title=\"STAR TREK TNG S1 D1\""}`,
		item, report, time.Time{})

	if report.InferredMediaHint != "tv" {
		t.Fatalf("inferred_media_hint = %q, want tv", report.InferredMediaHint)
	}
}

func TestParseLogLine_InferDiscSourceFromBDInfoAvailability(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:39:43Z","level":"INFO","msg":"disc source determined","item_id":24,"decision_type":"bdinfo_availability","decision_result":"bluray","decision_reason":"disc_type=Blu-ray"}`,
		item, report, time.Time{})

	if report.InferredDiscSource != "bluray" {
		t.Fatalf("inferred_disc_source = %q, want bluray", report.InferredDiscSource)
	}
}

func TestParseLogLine_MismatchedItemIDExcludesLine(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24, DiscFingerprint: "abc123", DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}

	// Same fingerprint and title, but an explicit item_id for another item.
	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"ERROR","msg":"rip failed","item_id":5,"fingerprint":"abc123","disc_title":"STAR TREK TNG S1 D1","event_type":"rip_failure","error":"boom"}`,
		item, report, time.Time{})

	if len(report.Errors) != 0 {
		t.Fatalf("expected other item's error excluded, got %+v", report.Errors)
	}
}

func TestParseLogLine_CutoffDropsLinesBeforeItemLifetime(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24, DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}
	cutoff := time.Date(2026, 4, 4, 21, 0, 0, 0, time.UTC)

	// A previous item with a reused ID logged before this item existed.
	parseLogLine(`{"time":"2026-04-04T20:15:00Z","level":"ERROR","msg":"encode failed","item_id":24,"event_type":"encode_failure","error":"old failure"}`,
		item, report, cutoff)
	// This item's own line after creation.
	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"encoding progress","item_id":24,"event_type":"encoding_progress","percent":10.0}`,
		item, report, cutoff)

	if len(report.Errors) != 0 {
		t.Fatalf("expected pre-lifetime error dropped, got %+v", report.Errors)
	}
	if len(report.Events) != 1 {
		t.Fatalf("expected in-lifetime event kept, got %+v", report.Events)
	}
}

func TestFindLogFilesIncludesLaterFilesForRestarts(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"spindle-20260320T010000.000Z.log", // before item creation
		"spindle-20260322T010000.000Z.log", // active at creation
		"spindle-20260323T090000.000Z.log", // post-restart continuation
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := findLogFiles(dir, "2026-03-22T02:18:41Z")
	if len(got) != 2 {
		t.Fatalf("expected 2 log files (creation-time + later), got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != names[1] || filepath.Base(got[1]) != names[2] {
		t.Fatalf("unexpected files: %v", got)
	}
}

func TestCompactProgressEventsDownsamplesPerType(t *testing.T) {
	var events []LogEntry
	for i := 0; i < 100; i++ {
		events = append(events, LogEntry{TS: "t", EventType: "encoding_progress"})
	}
	events = append(events, LogEntry{TS: "t", EventType: "mux_start"})
	events = append(events, LogEntry{TS: "t", EventType: "rip_progress"})

	kept, omitted := compactProgressEvents(events)

	progressKept := 0
	muxKept := 0
	ripKept := 0
	for _, e := range kept {
		switch e.EventType {
		case "encoding_progress":
			progressKept++
		case "mux_start":
			muxKept++
		case "rip_progress":
			ripKept++
		}
	}
	if progressKept > maxProgressEventsPerType+1 {
		t.Fatalf("encoding_progress kept = %d, want <= %d", progressKept, maxProgressEventsPerType+1)
	}
	if muxKept != 1 || ripKept != 1 {
		t.Fatalf("non-flooding events must pass through: mux=%d rip=%d", muxKept, ripKept)
	}
	if omitted != len(events)-len(kept) {
		t.Fatalf("omitted = %d, want %d", omitted, len(events)-len(kept))
	}
	// First and last flooding events survive.
	if kept[0].EventType != "encoding_progress" {
		t.Fatalf("expected first progress event kept")
	}
}

func TestComputeStageGate_TaskStatesLeadItemStage(t *testing.T) {
	item := &httpapi.ItemResponse{
		Stage: "ripping",
		Tasks: []httpapi.TaskResponse{
			{Type: "identification", State: "done"},
			{Type: "ripping", State: "running"},
			{Type: "encoding", State: "running"},
			{Type: "subtitling", State: "done"},
			{Type: "apply", State: "pending"},
		},
	}

	gate := computeStageGate(item, "tv", "tv", "bluray")
	if gate.FurthestStage != "subtitling" {
		t.Fatalf("furthest_stage = %q, want subtitling", gate.FurthestStage)
	}
	if !gate.PhaseEncoded || !gate.PhaseSubtitles {
		t.Fatalf("expected encoded+subtitles phases from task states: %+v", gate)
	}
	// Subtitling done implies analysis done (same branch precedes it).
	if !gate.PhaseCommentary {
		t.Fatalf("expected commentary phase implied by subtitling: %+v", gate)
	}
}

func TestResolveMediaTypeUnknownWithoutMetadata(t *testing.T) {
	env := &ripspec.Envelope{}
	item := &httpapi.ItemResponse{DiscTitle: "MYSTERY DISC"}

	if got := resolveMediaType(env, item); got != "unknown" {
		t.Fatalf("media type = %q, want unknown", got)
	}

	item.Metadata = json.RawMessage(`{"title":"Some Movie","movie":true}`)
	if got := resolveMediaType(env, item); got != "movie" {
		t.Fatalf("media type = %q, want movie", got)
	}
}
