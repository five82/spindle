package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestParseLogLine_MatchesFingerprintWithoutItemID(t *testing.T) {
	item := &queue.Item{ID: 24, DiscFingerprint: "abc123", DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"disc ID cache miss","decision_type":"disc_id_cache","decision_result":"miss","decision_reason":"fingerprint not in cache","fingerprint":"abc123"}`,
		item, report)

	if len(report.Decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(report.Decisions))
	}
	if report.Decisions[0].DecisionType != "disc_id_cache" {
		t.Fatalf("decision_type = %s, want disc_id_cache", report.Decisions[0].DecisionType)
	}
}

func TestParseLogLine_InferDiscSourceFromDiscDetectedLabel(t *testing.T) {
	item := &queue.Item{ID: 24, DiscTitle: "STAR TREK TNG S1 D1"}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:39:39Z","level":"INFO","msg":"disc detected, starting enqueue pipeline","event_type":"disc_detected","label":"STAR TREK TNG S1 D1","disc_type":"Blu-ray","device":"/dev/sr0"}`,
		item, report)

	if report.InferredDiscSource != "bluray" {
		t.Fatalf("inferred_disc_source = %q, want bluray", report.InferredDiscSource)
	}
}

func TestParseLogLine_InferMediaHintFromTMDBSearchDecision(t *testing.T) {
	item := &queue.Item{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:41:09Z","level":"INFO","msg":"media type hint detected","item_id":24,"decision_type":"tmdb_search","decision_result":"tv","decision_reason":"raw_title=\"STAR TREK TNG S1 D1\""}`,
		item, report)

	if report.InferredMediaHint != "tv" {
		t.Fatalf("inferred_media_hint = %q, want tv", report.InferredMediaHint)
	}
}

func TestParseLogLine_InferDiscSourceFromBDInfoAvailability(t *testing.T) {
	item := &queue.Item{ID: 24}
	report := &LogAnalysis{}

	parseLogLine(`{"time":"2026-04-04T21:39:43Z","level":"INFO","msg":"disc source determined","item_id":24,"decision_type":"bdinfo_availability","decision_result":"bluray","decision_reason":"disc_type=Blu-ray"}`,
		item, report)

	if report.InferredDiscSource != "bluray" {
		t.Fatalf("inferred_disc_source = %q, want bluray", report.InferredDiscSource)
	}
}
