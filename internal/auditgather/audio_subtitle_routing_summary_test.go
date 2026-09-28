package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/ripspec"
)

func TestSubtitleAndRoutingSummariesSurfaceMissingDelivery(t *testing.T) {
	r := &Report{Paths: AuditPaths{ReviewDir: "/review", LibraryDir: "/library"}, Envelope: &ripspec.Envelope{
		Metadata:   ripspec.Metadata{MediaType: "tv"},
		Attributes: ripspec.EnvelopeAttributes{SubtitleGenerationResults: []ripspec.SubtitleGenRecord{{EpisodeKey: "one", Source: "none", ValidationResult: "failed"}}},
		Episodes:   []ripspec.Episode{{Key: "one", Episode: 1, NeedsReview: true}, {Key: "unresolved"}},
		Assets:     ripspec.Assets{Final: []ripspec.Asset{{EpisodeKey: "one", Path: "/library/one.mkv", Status: ripspec.AssetStatusCompleted}, {EpisodeKey: "unresolved", Path: "/other/unresolved.mkv", Status: ripspec.AssetStatusCompleted}, {EpisodeKey: "incomplete", Path: "/review/incomplete.mkv", Status: ripspec.AssetStatusFailed}}},
	}}
	sub := computeSubtitleSummary(r, []MediaSummary{{Subtitles: []SubtitleStreamSummary{{Index: 2}}}})
	if sub == nil || sub.ValidationFailed != 1 || sub.Skipped != 1 || sub.OutputSubtitleTracks != 1 {
		t.Fatalf("subtitle summary: %+v", sub)
	}
	routing := computeRoutingSummary(r)
	if routing == nil || len(routing.Entries) != 2 || routing.Entries[0].MatchesExpected || routing.Entries[1].Destination != "other" || !routing.Entries[1].ExpectedReview {
		t.Fatalf("routing: %+v", routing)
	}
}

func TestAudioSummaryPreservesCommentaryEvidence(t *testing.T) {
	r := &Report{Envelope: &ripspec.Envelope{}}
	r.Envelope.Attributes.AudioAnalysis = &ripspec.AudioAnalysisData{PrimaryDescription: "English 5.1", PrimaryTrack: ripspec.AudioTrackRef{Index: 2}, ExcludedTracks: []ripspec.ExcludedTrackRef{{Index: 3, Reason: "stereo duplicate", Similarity: .99}}}
	r.Logs = &LogAnalysis{Decisions: []LogDecision{{DecisionType: logs.DecisionCommentaryClassification, DecisionResult: "commentary"}, {DecisionType: logs.DecisionCommentaryDisposition, DecisionResult: "kept"}, {DecisionType: logs.DecisionTitleSelection, DecisionResult: "selected"}}}
	out := []MediaSummary{{Audio: []AudioStreamSummary{{Index: 0}, {Index: 1, Commentary: true}}}}
	got := computeAudioSummary(r, out)
	if got == nil || got.PrimaryDescription != "English 5.1" || got.PrimaryTrackIndex != 2 || got.OutputAudioTracks != 2 || got.OutputCommentaryTracks != 1 || len(got.ExcludedTracks) != 1 || len(got.CommentaryDecisions) != 2 {
		t.Fatalf("audio summary: %+v", got)
	}
}
