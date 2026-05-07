package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/httpapi"
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

func TestParseLogLine_MatchesDiscIDCacheDecisionByItemID(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24, DiscFingerprint: "abc123", DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"disc ID cache miss","item_id":24,"decision_type":"disc_id_cache","decision_result":"miss","decision_reason":"disc_id not in cache","disc_id":"DCB2FF29F40C9CD4702BC163A3F4511A492E54A4"}`,
		item, report)

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
		item, report)

	if report.InferredDiscSource != "bluray" {
		t.Fatalf("inferred_disc_source = %q, want bluray", report.InferredDiscSource)
	}
}

func TestParseLogLine_InferMediaHintFromTMDBSearchDecision(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"media type hint detected","item_id":24,"decision_type":"tmdb_search","decision_result":"tv","decision_reason":"raw_title=\"STAR TREK TNG S1 D1\""}`,
		item, report)

	if report.InferredMediaHint != "tv" {
		t.Fatalf("inferred_media_hint = %q, want tv", report.InferredMediaHint)
	}
}

func TestParseLogLine_InferDiscSourceFromBDInfoAvailability(t *testing.T) {
	item := &httpapi.ItemResponse{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:39:43Z","level":"INFO","msg":"disc source determined","item_id":24,"decision_type":"bdinfo_availability","decision_result":"bluray","decision_reason":"disc_type=Blu-ray"}`,
		item, report)

	if report.InferredDiscSource != "bluray" {
		t.Fatalf("inferred_disc_source = %q, want bluray", report.InferredDiscSource)
	}
}
