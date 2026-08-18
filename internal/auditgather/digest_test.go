package auditgather

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func digestReport() *Report {
	return &Report{
		Item: ItemSummary{
			ID:            7,
			DiscTitle:     "Example Disc",
			Stage:         "failed",
			FailedAtStage: "encoding",
			ErrorMessage:  "encoder exploded",
			NeedsReview:   true,
			ReviewReasons: []string{"validation failed"},
			Tasks: []TaskSummary{
				{Type: "ripping", State: "done"},
				{Type: "encoding", State: "running", ProgressPercent: 42.5, ProgressMessage: "Phase 2/3 - Encoding"},
			},
		},
		StageGate: StageGate{
			FurthestStage: "encoding",
			MediaType:     "movie",
			DiscSource:    "bluray",
			PhaseLogs:     true,
			PhaseRipCache: true,
		},
		Logs: &LogAnalysis{
			Paths:         []string{"/var/log/spindle.log"},
			LinesScanned:  100,
			IsDebug:       true,
			EventsOmitted: 5,
			Warnings: []LogEntry{{
				TS: "2026-08-11T22:03:13-04:00", Level: "WARN", EventType: "reel_warning",
				Message: "low disk space", ErrorHint: "free space", Extras: map[string]any{"free_gb": 3},
			}},
			Events: []LogEntry{{
				TS: "2026-08-11T22:03:14-04:00", EventType: "rip_progress",
				Message: "rip progress", Extras: map[string]any{"percent": 100, "total": 65536},
			}},
		},
		Envelope: &ripspec.Envelope{
			Episodes: []ripspec.Episode{
				{Key: "s01_001", TitleID: 1, Season: 1, Episode: 3, MatchConfidence: 0.95, EpisodeTitle: "Third"},
				{Key: "s01_002", TitleID: 2, NeedsReview: true, ReviewReason: "unresolved"},
			},
		},
		Media: []MediaFileProbe{{Path: "/x/broken.mkv", EpisodeKey: "s01_002", Error: "ffprobe failed"}},
		Analysis: &Analysis{
			Anomalies: []Anomaly{{Severity: "critical", Category: "assets", Message: "episode missing"}},
			AVSync: &AVSyncSummary{Failed: 1, Entries: []AVSyncEntry{{
				EpisodeKey: "main", SourceAudioOffsetSec: 0.501, OutputAudioOffsetSec: 0, DriftMilliseconds: -501,
			}}},
			DecisionGroups: []DecisionGroup{
				{
					DecisionType: "rip_retry", DecisionResult: "retried", DecisionReason: "io error", Count: 3,
					Entries: []LogDecision{
						{TS: "2026-08-11T22:00:01-04:00", Message: "retrying"},
						{TS: "2026-08-11T22:05:01-04:00", Message: "retrying"},
						{TS: "2026-08-11T22:10:01-04:00", Message: "retrying"},
					},
				},
				{
					DecisionType: "title_rip", DecisionResult: "completed", Count: 2,
					Entries: []LogDecision{
						{TS: "2026-08-11T22:20:01-04:00", Message: "ripped title 0"},
						{TS: "2026-08-11T22:30:01-04:00", Message: "ripped title 1"},
					},
				},
			},
			EpisodeStats: &EpisodeStats{Count: 2, Matched: 1, Unresolved: 1},
		},
		Errors: []string{"missing log file"},
	}
}

func TestRenderDigestCoreSections(t *testing.T) {
	out := RenderDigest(digestReport(), "/tmp/audit.json")

	for _, want := range []string{
		"item 7: Example Disc",
		"FAILED at encoding: encoder exploded",
		"Review reasons: validation failed",
		"Task encoding: running 42.5%",
		"Applicable phases: logs, rip_cache",
		"Full JSON: /tmp/audit.json",
		"GATHERING ERRORS",
		"missing log file",
		"[CRITICAL] assets: episode missing",
		"[reel_warning] low disk space | hint: free space | free_gb=3",
		"[rip_progress] rip progress | percent=100 total=65536",
		"5 progress ticks omitted",
		// Identical repeats: one line with every timestamp (retry spacing).
		"rip_retry: retried (io error) x3 @ 08-11 22:00:01, 08-11 22:05:01, 08-11 22:10:01",
		// Varying messages: expanded entries.
		"title_rip: completed x2:",
		"08-11 22:20:01 ripped title 0",
		"08-11 22:30:01 ripped title 1",
		// Episode manifest with unresolved flagged.
		"s01_001 title_id=1 S01E03 conf=0.95",
		"s01_002 title_id=2 UNRESOLVED",
		"REVIEW: unresolved",
		"## Audio/video sync (independent source comparison)",
		"main: FAILED | source offset +501ms -> output +0ms | audio 501ms earlier",
		// Probe errors must surface even without valid summaries.
		"PROBE ERROR /x/broken.mkv (s01_002): ffprobe failed",
		"## Digest limits",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q", want)
		}
	}
}

func TestRenderDigestPlaceholderManifestLabel(t *testing.T) {
	r := digestReport()
	r.Analysis.EpisodeStats.PlaceholderOnly = true
	r.StageGate.PhaseEpisodeID = false
	out := RenderDigest(r, "/tmp/audit.json")
	if !strings.Contains(out, "PLACEHOLDER INVENTORY") {
		t.Error("expected placeholder manifest label")
	}
}

func TestRenderDigestCapsGroupEntries(t *testing.T) {
	r := digestReport()
	var entries []LogDecision
	for i := range 40 {
		entries = append(entries, LogDecision{
			TS:      "2026-08-11T22:00:01-04:00",
			Message: strings.Repeat("m", i+1), // vary so entries expand
		})
	}
	r.Analysis.DecisionGroups = []DecisionGroup{{
		DecisionType: "noisy", DecisionResult: "varies", Count: len(entries), Entries: entries,
	}}
	out := RenderDigest(r, "/tmp/audit.json")
	if !strings.Contains(out, "(+10 more in JSON)") {
		t.Error("expected explicit omission note for capped group entries")
	}
}

func TestFormatExtrasTruncatesLongValues(t *testing.T) {
	got := formatExtras(map[string]any{"big": strings.Repeat("x", maxExtraValueLen+50)})
	if !strings.Contains(got, "truncated, full value in JSON") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}
