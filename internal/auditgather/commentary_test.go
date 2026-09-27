package auditgather

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/ripspec"
)

func TestJevCommentaryEvidenceSurvivesAuditJSONAndDigest(t *testing.T) {
	r := &Report{
		Item:      ItemSummary{ID: 7, Stage: "analysis"},
		StageGate: StageGate{PhaseCommentary: true, MediaType: "movie"},
		Logs:      &LogAnalysis{},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "movie"},
			Attributes: ripspec.EnvelopeAttributes{AudioAnalysis: &ripspec.AudioAnalysisData{
				CommentaryTracks: []ripspec.CommentaryTrackRef{
					{Index: 1, Confidence: 0.65, Reason: "Jev commentary probability 0.65 >= 0.65"},
					{Index: 4, Confidence: 0, Reason: "llm classification failed: empty commentary transcript"},
				},
			}},
		},
	}
	for _, line := range []string{
		`{"time":"2026-09-27T00:00:01Z","level":"INFO","item_id":7,"msg":"track classified as commentary","decision_type":"commentary_classification","decision_result":"commentary","decision_reason":"Jev commentary probability 0.65 >= 0.65","track_index":1,"commentary_probability":0.65}`,
		`{"time":"2026-09-27T00:00:02Z","level":"INFO","item_id":7,"msg":"track classified as not commentary","decision_type":"commentary_classification","decision_result":"not_commentary","decision_reason":"Jev commentary probability 0.64 < 0.65","track_index":2,"commentary_probability":0.64}`,
		`{"time":"2026-09-27T00:00:03Z","level":"INFO","item_id":7,"msg":"track classified as not commentary","decision_type":"commentary_classification","decision_result":"not_commentary","decision_reason":"Jev commentary probability 0 < 0.65","track_index":3,"commentary_probability":0}`,
		`{"time":"2026-09-27T00:00:04Z","level":"WARN","item_id":7,"msg":"LLM commentary classification failed, conservatively marking as commentary","event_type":"commentary_detection_failed","error_hint":"commentary classification error","impact":"track preserved as commentary","error":"empty commentary transcript","track_index":4}`,
	} {
		parseLogLine(line, &httpapi.ItemResponse{ID: 7}, r.Logs, time.Time{})
	}
	r.Analysis = computeAnalysis(r)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	checkDecisions := func(decisions []LogDecision) {
		t.Helper()
		if len(decisions) != 3 {
			t.Fatalf("lost decisions: %+v", decisions)
		}
		for i, p := range []float64{0.65, 0.64, 0} {
			d := decisions[i]
			actual, present := d.Extras["commentary_probability"]
			if !present || actual != p || d.Extras["track_index"] != float64(i+1) {
				t.Fatalf("lost probability or track: %+v", d)
			}
			want := "not_commentary"
			if i == 0 {
				want = "commentary"
			}
			if d.DecisionResult != want || !strings.Contains(d.DecisionReason, "Jev commentary probability") {
				t.Fatalf("lost verdict/rule: %+v", d)
			}
			if _, present := d.Extras["confidence"]; present {
				t.Fatalf("probability relabeled as confidence: %+v", d)
			}
		}
	}
	checkDecisions(r.Logs.Decisions)
	for _, report := range []*Report{r, &decoded} {
		var grouped []LogDecision
		for _, group := range report.Analysis.DecisionGroups {
			grouped = append(grouped, group.Entries...)
		}
		checkDecisions(grouped)
		checkDecisions(report.Analysis.NotableDecisions)
		if report.Analysis.AudioSummary == nil {
			t.Fatal("missing audio summary")
		}
		checkDecisions(report.Analysis.AudioSummary.CommentaryDecisions)
		tracks := report.Envelope.Attributes.AudioAnalysis.CommentaryTracks
		if len(tracks) != 2 || tracks[0].Confidence != 0.65 || tracks[1].Confidence != 0 || !strings.Contains(tracks[1].Reason, "empty commentary transcript") {
			t.Fatalf("lost classified/fallback track evidence: %+v", tracks)
		}
		if len(report.Logs.Warnings) != 1 || report.Logs.Warnings[0].Extras["impact"] != "track preserved as commentary" || report.Logs.Warnings[0].Extras["error"] != "empty commentary transcript" {
			t.Fatalf("lost conservative-fallback warning: %+v", report.Logs.Warnings)
		}
		for _, anomaly := range report.Analysis.Anomalies {
			if strings.Contains(strings.ToLower(anomaly.Message), "confidence") {
				t.Fatalf("episode confidence rule applied to commentary: %+v", anomaly)
			}
		}
		digest := RenderDigest(report, "/tmp/audit.json")
		for _, want := range []string{"commentary_probability=0.65", "commentary_probability=0.64", "commentary_probability=0", "Jev commentary probability 0.65 >= 0.65", "Jev commentary probability 0.64 < 0.65", "track_index=4", "track preserved as commentary", "empty commentary transcript"} {
			if !strings.Contains(digest, want) {
				t.Errorf("digest lost %q:\n%s", want, digest)
			}
		}
	}
}
