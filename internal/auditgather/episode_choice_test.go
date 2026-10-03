package auditgather

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/ripspec"
)

func TestEpisodeProbabilityAnomaliesDistinguishAcceptanceFromAbstention(t *testing.T) {
	for _, p := range []float64{0, 0.49, 0.5, 0.65, 0.75, 0.85, 0.899, 0.90, 1} {
		for _, resolved := range []bool{false, true} {
			for _, slot := range []bool{false, true} {
				if slot && !resolved {
					continue
				}
				t.Run(fmt.Sprintf("p=%g/resolved=%v/slot=%v", p, resolved, slot), func(t *testing.T) {
					ep := ripspec.Episode{Key: "s01_001", MatchProbability: p, NeedsReview: !resolved, SlotCorroborated: slot}
					if resolved {
						ep.Episode = 1
					}
					r := &Report{
						StageGate: StageGate{PhaseEpisodeID: true},
						Envelope:  &ripspec.Envelope{Episodes: []ripspec.Episode{ep}},
					}
					a := computeAnalysis(r)
					// Slot corroboration lowers the acceptance bar to 0.50, never removes it.
					wantBelow, floor := 0, 0.90
					if slot {
						floor = 0.50
					}
					if resolved && p < floor {
						wantBelow = 1
					}
					if a.EpisodeStats.Below090 != wantBelow {
						t.Fatalf("stats = %+v, want below090=%d", a.EpisodeStats, wantBelow)
					}
					critical, unresolved := 0, 0
					for _, anomaly := range a.Anomalies {
						if anomaly.Severity == "critical" {
							critical++
							if !strings.Contains(anomaly.Message, "resolved episode(s) below their acceptance probability") {
								t.Errorf("unexpected critical anomaly: %+v", anomaly)
							}
						}
						if anomaly.Message == "1 unresolved episode(s)" {
							unresolved++
							if anomaly.Severity != "warning" {
								t.Errorf("abstention severity = %s", anomaly.Severity)
							}
						}
					}
					if critical != wantBelow || (!resolved && unresolved != 1) || (resolved && unresolved != 0) {
						t.Fatalf("unexpected anomalies: %+v", a.Anomalies)
					}
					if slot != (a.EpisodeStats.SlotCorroborated == 1) {
						t.Fatalf("slot count: %+v", a.EpisodeStats)
					}
				})
			}
		}
	}
}

func TestEpisodeStatsIncludesWholeCanonicalRanges(t *testing.T) {
	episodes := []ripspec.Episode{{Episode: 1, EpisodeEnd: 3, MatchProbability: 0.95}, {Episode: 4, MatchProbability: 0.90}}
	stats := computeEpisodeStats(episodes)
	if !stats.SequenceContiguous || stats.EpisodeRange != "1-4" || stats.Matched != 2 {
		t.Fatalf("canonical range counted as a gap: %+v", stats)
	}
	episodes[1].Episode = 3
	if computeEpisodeStats(episodes).SequenceContiguous {
		t.Fatal("overlapping range counted as contiguous")
	}
}

func TestJevEpisodeEvidenceSurvivesAuditJSONAndDigest(t *testing.T) {
	r := &Report{
		Item:      ItemSummary{ID: 7, Stage: "episode_identification"},
		StageGate: StageGate{PhaseEpisodeID: true, MediaType: "tv"},
		Logs:      &LogAnalysis{},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{
				{Key: "s01_001", Season: 1, Episode: 1, MatchProbability: 0.90},
				{Key: "s01_002", Season: 1, Episode: 2, MatchProbability: 0.98, NeedsReview: true, ReviewReason: "encoding review"},
				{Key: "s01_003", Season: 1, MatchProbability: 0.74, NeedsReview: true, ReviewReason: "episode probability below acceptance threshold"},
				{Key: "s01_004", Season: 1, NeedsReview: true, ReviewReason: "no reference has distinctive overlapping dialogue"},
			},
			Attributes: ripspec.EnvelopeAttributes{ContentID: &ripspec.ContentIDSummary{
				Method: "whisperx_jev_reference_choice", ReferenceSource: "opensubtitles", ReferenceEpisodes: 20,
				TranscribedEpisodes: 4, MatchedEpisodes: 2, UnresolvedEpisodes: 2,
				ReviewEpisodes: 2, ReviewThreshold: 0.90, Completed: true,
			}},
		},
	}
	for _, line := range []string{
		`{"level":"INFO","item_id":7,"msg":"episode identification plan","decision_type":"contentid_matches","decision_result":"classify_full_season","decision_reason":"five-minute middle excerpts matched to title-consistent dialogue references across the canonical TMDB season","candidate_episodes":20,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode classification decided","decision_type":"episode_match","decision_result":"matched","decision_reason":"distinctive dialogue overlap meets episode probability threshold","episode_key":"s01_001","title_id":1,"candidate":"E01","reference_file_id":77,"match_probability":0.9,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode classification decided","decision_type":"episode_match","decision_result":"review","decision_reason":"episode probability below acceptance threshold","episode_key":"s01_003","title_id":3,"candidate":"E03","match_probability":0.74,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode classification decided","decision_type":"episode_match","decision_result":"review","decision_reason":"no reference has distinctive overlapping dialogue","episode_key":"s01_004","title_id":4,"candidate":"none","match_probability":0.99,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode reference decided","decision_type":"reference_search","decision_result":"selected","decision_reason":"canonical title present, no competing episode title, single English full-subtitle file","season":1,"episode":1,"episode_title":"First","reference_file_id":77,"release":"Show First","file_name":"First.srt"}`,
		`{"level":"INFO","item_id":7,"msg":"episode reference decided","decision_type":"reference_search","decision_result":"omitted","decision_reason":"no unambiguous canonical title in a single-file English full-subtitle candidate","season":1,"episode":21,"reference_file_id":0}`,
	} {
		parseLogLine(line, &httpapi.ItemResponse{ID: 7}, r.Logs, time.Time{})
	}
	r.Analysis = computeAnalysis(r)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{"confidence_min", "confidence_max", "confidence_mean", "below_070", "below_080"} {
		if strings.Contains(string(data), stale) {
			t.Errorf("JSON retains obsolete %s", stale)
		}
	}
	for _, field := range []string{`"probability_min":0.9`, `"probability_max":0.98`, `"probability_mean":0.94`, `"below_090":0`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("JSON missing %s", field)
		}
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, report := range []*Report{r, &decoded} {
		es := report.Analysis.EpisodeStats
		if es.Matched != 2 || es.Unresolved != 2 || es.ProbabilityMean != 0.94 || es.Below090 != 0 {
			t.Fatalf("abstentions polluted accepted probabilities: %+v", es)
		}
		decisions := report.Analysis.NotableDecisions
		if len(decisions) != 6 || decisions[3].Extras["candidate"] != "none" || decisions[3].Extras["match_probability"] != 0.99 {
			t.Fatalf("lost choice evidence: %+v", decisions)
		}
		if decisions[4].Extras["reference_file_id"] != float64(77) || decisions[4].Extras["file_name"] != "First.srt" || decisions[5].DecisionResult != "omitted" {
			t.Fatalf("lost reference acquisition evidence: %+v", decisions)
		}
		for _, a := range report.Analysis.Anomalies {
			if a.Severity == "critical" {
				t.Errorf("safe abstention treated as critical: %+v", a)
			}
		}
		digest := RenderDigest(report, "/tmp/audit.json")
		for _, want := range []string{
			"method=whisperx_jev_reference_choice references=opensubtitles (20)", "matched=2 unresolved=2 review=2 | completed=true",
			"resolved episode probability min=0.90 mean=0.94 max=0.98 | below acceptance rule: 0",
			"3 episode(s) explicitly flagged for review", "2 unresolved episode(s)",
			"candidate=none", "match_probability=0.99", "probability_threshold=0.9",
			"reference_file_id=77", "file_name=First.srt", "reference_search", "omitted",
			"UNRESOLVED probability=0.74 REVIEW: episode probability below acceptance threshold",
			"S01E02 probability=0.98 REVIEW: encoding review",
		} {
			if !strings.Contains(digest, want) {
				t.Errorf("digest missing %q:\n%s", want, digest)
			}
		}
	}
}

// Item 4 (TNG S2D1): E02 accepted at 0.85 by slot corroboration must read as a
// deliberate, visible decision, not as a clean match or an acceptance-rule bug.
func TestSlotCorroboratedEpisodeIsVisibleNotCritical(t *testing.T) {
	r := &Report{
		StageGate: StageGate{PhaseEpisodeID: true, MediaType: "tv"},
		Envelope: &ripspec.Envelope{Episodes: []ripspec.Episode{
			{Key: "s02_003", Season: 2, Episode: 3, MatchProbability: 0.99},
			{Key: "s02_004", Season: 2, Episode: 2, EpisodeTitle: "Where Silence Has Lease", MatchProbability: 0.85, SlotCorroborated: true},
			{Key: "s02_005", Season: 2, Episode: 1, MatchProbability: 0.99},
		}},
	}
	r.Analysis = computeAnalysis(r)
	if es := r.Analysis.EpisodeStats; es.SlotCorroborated != 1 || es.Below090 != 0 || !es.SequenceContiguous {
		t.Fatalf("stats %+v", es)
	}
	var info bool
	for _, a := range r.Analysis.Anomalies {
		if a.Severity == "critical" {
			t.Fatalf("slot identity flagged critical: %+v", a)
		}
		info = info || (a.Severity == "info" && strings.Contains(a.Message, "1 episode(s) accepted below 0.90"))
	}
	if !info {
		t.Fatalf("slot decision not surfaced: %+v", r.Analysis.Anomalies)
	}
	var b strings.Builder
	writeDigestEpisodeID(&b, r)
	for _, want := range []string{"3 matched (1 slot-corroborated)", `S02E02 probability=0.85 "Where Silence Has Lease" SLOT-CORROBORATED`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("digest missing %q:\n%s", want, b.String())
		}
	}
}
