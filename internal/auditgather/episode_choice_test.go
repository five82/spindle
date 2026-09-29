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
	for _, p := range []float64{0, 0.65, 0.75, 0.85, 0.899, 0.90, 1} {
		for _, resolved := range []bool{false, true} {
			t.Run(fmt.Sprintf("p=%g/resolved=%v", p, resolved), func(t *testing.T) {
				ep := ripspec.Episode{Key: "s01_001", MatchProbability: p, NeedsReview: !resolved}
				if resolved {
					ep.Episode = 1
				}
				r := &Report{
					StageGate: StageGate{PhaseEpisodeID: true},
					Envelope:  &ripspec.Envelope{Episodes: []ripspec.Episode{ep}},
				}
				a := computeAnalysis(r)
				wantBelow := 0
				if resolved && p < 0.90 {
					wantBelow = 1
				}
				if a.EpisodeStats.Below090 != wantBelow {
					t.Fatalf("stats = %+v, want below090=%d", a.EpisodeStats, wantBelow)
				}
				critical, unresolved := 0, 0
				for _, anomaly := range a.Anomalies {
					if anomaly.Severity == "critical" {
						critical++
						if !strings.Contains(anomaly.Message, "resolved episode(s) below the 0.90 acceptance probability") {
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
			})
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
				{Key: "s01_004", Season: 1, NeedsReview: true, ReviewReason: "no listed episode has distinctive plot support"},
			},
			Attributes: ripspec.EnvelopeAttributes{ContentID: &ripspec.ContentIDSummary{
				Method: "whisperx_jev_episode_choice", ReferenceSource: "tmdb", ReferenceEpisodes: 20,
				TranscribedEpisodes: 4, MatchedEpisodes: 2, UnresolvedEpisodes: 2,
				ReviewEpisodes: 2, ReviewThreshold: 0.90, Completed: true,
			}},
		},
	}
	for _, line := range []string{
		`{"level":"INFO","item_id":7,"msg":"episode identification plan","decision_type":"contentid_matches","decision_result":"classify_full_season","decision_reason":"full transcripts matched to canonical TMDB episode overviews","candidate_episodes":20,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode classification decided","decision_type":"episode_match","decision_result":"matched","decision_reason":"distinctive plot evidence meets episode probability threshold","episode_key":"s01_001","title_id":1,"candidate":"E01","match_probability":0.9,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode classification decided","decision_type":"episode_match","decision_result":"review","decision_reason":"episode probability below acceptance threshold","episode_key":"s01_003","title_id":3,"candidate":"E03","match_probability":0.74,"probability_threshold":0.9}`,
		`{"level":"INFO","item_id":7,"msg":"episode classification decided","decision_type":"episode_match","decision_result":"review","decision_reason":"no listed episode has distinctive plot support","episode_key":"s01_004","title_id":4,"candidate":"none","match_probability":0.99,"probability_threshold":0.9}`,
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
		if len(decisions) != 4 || decisions[3].Extras["candidate"] != "none" || decisions[3].Extras["match_probability"] != 0.99 {
			t.Fatalf("lost choice evidence: %+v", decisions)
		}
		for _, a := range report.Analysis.Anomalies {
			if a.Severity == "critical" {
				t.Errorf("safe abstention treated as critical: %+v", a)
			}
		}
		digest := RenderDigest(report, "/tmp/audit.json")
		for _, want := range []string{
			"method=whisperx_jev_episode_choice catalog=tmdb (20)", "matched=2 unresolved=2 review=2 | completed=true",
			"resolved episode probability min=0.90 mean=0.94 max=0.98 | resolved <0.90: 0",
			"3 episode(s) explicitly flagged for review", "2 unresolved episode(s)",
			"candidate=none", "match_probability=0.99", "probability_threshold=0.9",
			"UNRESOLVED probability=0.74 REVIEW: episode probability below acceptance threshold",
			"S01E02 probability=0.98 REVIEW: encoding review",
		} {
			if !strings.Contains(digest, want) {
				t.Errorf("digest missing %q:\n%s", want, digest)
			}
		}
	}
}
