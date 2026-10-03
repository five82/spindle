package auditgather

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/ripspec"
)

func TestDetectAnomaliesReportsOperationalFailures(t *testing.T) {
	report := &Report{
		Item:      ItemSummary{NeedsReview: true, ReviewReasons: []string{"inspect source"}, ErrorMessage: "drive lost", FailedAtStage: "ripping"},
		Logs:      &LogAnalysis{Warnings: []LogEntry{{Message: "warn"}}, Errors: []LogEntry{{Message: "error"}}},
		StageGate: StageGate{PhaseEpisodeID: true},
		Envelope:  &ripspec.Envelope{Episodes: []ripspec.Episode{{Key: "one", NeedsReview: true}}},
		Encoding:  &EncodingReport{Snapshot: encodingstate.Snapshot{Validation: &encodingstate.Validation{Passed: false}, Error: &encodingstate.Issue{Message: "encode crashed"}, Warning: "slow disk"}},
		Media:     []MediaFileProbe{{Path: "bad.mkv", Error: "probe failed"}},
	}
	analysis := &Analysis{
		EpisodeStats: &EpisodeStats{Matched: 3, Unresolved: 1, Below090: 3, EpisodeRange: "1,3"},
		AssetHealth:  &AssetHealth{Ripped: &AssetCounts{Failed: 1}, Encoded: &AssetCounts{Failed: 2}, Subtitled: &AssetCounts{Failed: 1}, Final: &AssetCounts{Failed: 1}, Transcript: &AssetCounts{Failed: 1}},
	}
	anomalies := detectAnomalies(report, analysis)
	messages := make(map[string]string)
	for _, a := range anomalies {
		messages[a.Message] = a.Severity
	}
	for text, severity := range map[string]string{
		"item failed at ripping: drive lost": "critical", "item needs review: inspect source": "warning",
		"1 error(s) in item log": "critical", "1 warning(s) in item log": "warning",
		"1 episode(s) explicitly flagged for review": "warning", "1 unresolved episode(s)": "warning",
		"3 resolved episode(s) below their acceptance probability (0.90 direct, 0.50 slot-corroborated)": "critical", "non-contiguous episode sequence: 1,3": "warning",
		"encoding validation failed": "critical", "encoding error: encode crashed": "critical", "encoding warning: slow disk": "warning",
		"1 failed ripped asset(s)": "critical", "2 failed encoded asset(s)": "critical", "1 failed transcript asset(s)": "critical",
		"1 media probe(s) failed": "warning",
	} {
		if got := messages[text]; got != severity {
			t.Errorf("%q severity = %q, want %q", text, got, severity)
		}
	}
	if len(anomalies) < len(messages) || !strings.Contains(anomalies[0].Message, "drive lost") {
		t.Fatalf("unexpected anomalies: %+v", anomalies)
	}
}

func TestDetectAnomaliesFlagsIncompleteContentIDProvenance(t *testing.T) {
	report := &Report{StageGate: StageGate{MediaType: "tv", PhaseEpisodeID: true}, Envelope: &ripspec.Envelope{Metadata: ripspec.Metadata{MediaType: "tv"}}}
	for _, summary := range []*ripspec.ContentIDSummary{{}, {Method: "whisperx_jev_reference_choice"}} {
		report.Envelope.Attributes.ContentID = summary
		anomalies := detectAnomalies(report, &Analysis{})
		if len(anomalies) != 1 || anomalies[0].Category != "episodes" || anomalies[0].Severity != "warning" {
			t.Fatalf("summary %+v anomalies: %+v", summary, anomalies)
		}
	}
}
