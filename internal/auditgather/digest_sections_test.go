package auditgather

import (
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

// Exercise the optional sections together: a missing section must not hide
// evidence from another phase of the same audit.
func TestRenderDigestOptionalEvidence(t *testing.T) {
	r := digestReport()
	r.StageGate.MediaType = "tv"
	r.StageGate.MediaHint = "movie"
	r.StageGate.PhaseEpisodeID = true
	r.StageGate.PhaseEncoded = true
	r.StageGate.PhaseCrop = true
	r.StageGate.PhaseSubtitles = true
	r.StageGate.PhaseCommentary = true
	r.StageGate.PhaseExtVal = true
	r.Item.Tasks[1].Attempts = 2
	r.Item.Tasks[1].ActiveAssetKey = "s01_001"
	r.Item.Tasks[1].Error = "retrying"
	r.Logs.Errors = []LogEntry{{TS: "2026-08-11T22:03:13-04:00", Message: "disk error"}}
	r.Transitions = []queue.Event{
		{Time: "2026-08-12T02:00:00Z", Type: "stage_start", Stage: "ripping", TaskID: 2, Attempt: 2},
		{Time: "2026-08-12T02:03:00Z", Type: "stage_complete", Stage: "ripping", TaskID: 2, Attempt: 2, DurationSeconds: 180},
	}
	r.Analysis.DecisionGroups = []DecisionGroup{{DecisionType: "selected", DecisionResult: "yes", Count: 1, Entries: []LogDecision{{TS: "2026-08-11T22:00:00-04:00", Extras: map[string]any{"title": 1}}}}}
	r.RipCache = &RipCacheReport{Path: "/cache", Found: true, Metadata: &ripCacheMetadata{DiscTitle: "Example", CachedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC), TitleCount: 2, TotalBytes: 1024}}
	r.Analysis.TitleSelection = &TitleSelectionSummary{SelectedID: 2, SelectedDurationSeconds: 3600, DecisionResult: "chosen", DecisionReason: "playlist", FeatureCandidateCount: 2, SimilarRuntimeCount: 1, Candidates: []TitleCandidate{{ID: 2, DurationSeconds: 3600, Chapters: 12, Playlist: "00800.mpls", SegmentCount: 3, Selected: true}}}
	r.Envelope.Episodes[0].EpisodeEnd = 4
	r.Envelope.Attributes.ContentID = &ripspec.ContentIDSummary{Method: "whisperx_jev_reference_choice", ReferenceSource: "opensubtitles", ReferenceEpisodes: 3, MatchedEpisodes: 2, ReviewEpisodes: 1, Completed: true}
	r.Analysis.CropAnalysis = &CropAnalysis{OutputWidth: 1920, OutputHeight: 800, AspectRatio: 2.4, StandardRatio: "2.40:1"}
	r.Encoding = &EncodingReport{Snapshot: encodingstate.Snapshot{Encoder: "av1", CropFilter: "crop=1920:800", CropRequired: true, OriginalSize: 2048, EncodedSize: 1024, SizeReductionPercent: 50, Warning: "slow"}}
	r.Analysis.FinalValidation.Entries = append(r.Analysis.FinalValidation.Entries, ripspec.FinalValidationEntry{OutputPath: "/library/good.mkv", AVSync: &ripspec.AVSyncCheck{Passed: true, DriftMilliseconds: 20}}, ripspec.FinalValidationEntry{EpisodeKey: "broken", Error: "no probe"})
	r.MediaOmitted = 2
	r.Analysis.OutputMedia = []MediaSummary{{EpisodeKey: "s01_001", Path: "/library/good.mkv", DurationSeconds: 3600, SizeBytes: 1024, Video: &VideoSummary{Codec: "av1", Width: 1920, Height: 1080, HDR: true, ColorTransfer: "pq", ColorPrimaries: "bt2020"}, Audio: []AudioStreamSummary{{Index: 1, Language: "eng", Codec: "aac", Channels: 2, Default: true, Commentary: true}}, Subtitles: []SubtitleStreamSummary{{Index: 2, Language: "eng", Codec: "subrip", Forced: true, Default: true}}}}
	r.Analysis.MediaStats = &MediaStats{FileCount: 2, DurationMinSec: 3500, DurationMaxSec: 3600, SizeMinBytes: 1024, SizeMaxBytes: 2048}
	r.Analysis.EpisodeConsistency = &EpisodeConsistency{MajorityCount: 1, TotalEpisodes: 2, MajorityProfile: ProfileSummary{VideoCodec: "av1", Width: 1920, Height: 1080}, Deviations: []ProfileDeviation{{EpisodeKey: "s01_002", Differences: []string{"audio missing"}}}}
	r.Analysis.AudioSummary = &AudioSummary{PrimaryTrackIndex: 1, PrimaryDescription: "English", OutputAudioTracks: 2, OutputCommentaryTracks: 1, ExcludedTracks: []ExcludedTrack{{Index: 3, Reason: "duplicate", Similarity: .98}}}
	r.Analysis.SubtitleSummary = &SubtitleSummary{ValidationPassed: 1, OutputSubtitleTracks: 1, Results: []SubtitleResultSummary{{EpisodeKey: "s01_001", ValidationResult: "passed", Source: "opensubtitles", Segments: 100, SevereIssues: []string{"bad timing"}, ReviewIssues: []string{"check"}, QCObservations: []string{"short"}}}}
	r.Analysis.RoutingSummary = &RoutingSummary{Entries: []RoutingEntry{{EpisodeKey: "s01_001", Path: "/review/good.mkv", Destination: "review", ExpectedReview: true, MatchesExpected: false}}}
	r.Analysis.AssetHealth = &AssetHealth{Ripped: &AssetCounts{Total: 2, OK: 1, Failed: 1}, Final: &AssetCounts{Total: 2, OK: 1, Muxed: 1}}
	out := RenderDigest(r, "/tmp/report.json")
	for _, want := range []string{"Media hint: movie", "attempts=2", "asset=s01_001", "error=retrying", "disk error", "ripping task=2 attempt=2: 08-12 02:00:00 -> COMPLETE 3m0s", "selected: yes @", "title=1", "cached 2026-08-11 12:00", "Selected title 2 (3600s) via chosen (playlist)", "00800.mpls, 3 segments", "S01E03-E04", "Content ID: method=whisperx_jev_reference_choice references=opensubtitles (3) | transcribed=0 matched=2 unresolved=0 review=1 | completed=true", "Encoding snapshot (last episode encoded", "1920x800 2.40:1", "Size: 2048 B -> 1024 B", "WARNING: slow", "good.mkv: PASSED", "broken: UNAVAILABLE (no probe)", "audio 20ms later", "2 clean probes omitted", "HDR (pq/bt2020)", "DEFAULT COMMENTARY", "DEFAULT FORCED", "Across 2 files", "DEVIATION s01_002: audio missing", "similarity 0.98", "SEVERE: bad timing", "review: check", "qc (telemetry): short", "MISMATCH", "ripped 1/2 ok (1 FAILED)", "final 1/2 ok (muxed 1)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in digest:\n%s", want, out)
		}
	}
}

func TestRenderDigestCacheStatesAndGrainFallbacks(t *testing.T) {
	for _, tc := range []struct {
		cache RipCacheReport
		want  string
	}{
		{RipCacheReport{Disabled: true}, "disabled in config"},
		{RipCacheReport{Path: "/pruned"}, "not found at /pruned"},
		{RipCacheReport{Path: "/unindexed", Found: true}, "found at /unindexed (no metadata)"},
	} {
		r := &Report{RipCache: &tc.cache}
		if got := RenderDigest(r, "report.json"); !strings.Contains(got, tc.want) {
			t.Errorf("missing %q in %s", tc.want, got)
		}
	}
	r := digestReport()
	r.Analysis.GrainTreatments = []GrainTreatmentEntry{{GrainTreatment: ripspec.GrainTreatment{Treated: true, ResolutionClass: "sd", Reused: true, GateStage: "tq_probe", Stage2MedianBPP: 0.1, Stage2Probes: 3}}, {GrainTreatment: ripspec.GrainTreatment{ResolutionClass: "1080p", Stage2Error: "timeout"}}}
	out := RenderDigest(r, "report.json")
	for _, want := range []string{"verdict reused from resume", "stage 2: ambiguous", "3 probes", "denoise ceiling: NOT MEASURED", "stage 2: not measured (timeout)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}
