package auditgather

import (
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestReferenceProvenanceAnomaliesAreStageGated(t *testing.T) {
	for _, tt := range []struct {
		name, source string
		gate         bool
		resolved     bool
		references   int
		want         string
	}{
		{"accepted without reference", "opensubtitles", true, true, 0, "critical"},
		{"safe missing reference abstention", "opensubtitles", true, false, 0, ""},
		{"accepted with reference", "opensubtitles", true, true, 1, ""},
		{"not yet identified", "opensubtitles", false, true, 0, ""},
		{"wrong provenance", "tmdb", true, true, 1, "warning"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ep := ripspec.Episode{Key: "one", NeedsReview: !tt.resolved}
			if tt.resolved {
				ep.Episode, ep.MatchProbability = 1, .95
			}
			r := &Report{
				StageGate: StageGate{PhaseEpisodeID: tt.gate},
				Envelope: &ripspec.Envelope{
					Metadata: ripspec.Metadata{MediaType: "tv"}, Episodes: []ripspec.Episode{ep},
					Attributes: ripspec.EnvelopeAttributes{ContentID: &ripspec.ContentIDSummary{
						Method: "whisperx_jev_reference_choice", ReferenceSource: tt.source, ReferenceEpisodes: tt.references,
					}},
				},
			}
			severity := ""
			for _, a := range computeAnalysis(r).Anomalies {
				if a.Message == "resolved episode identities have no usable dialogue references" || a.Message == "episode identification provenance does not describe the dialogue-reference classifier" {
					severity = a.Severity
				}
			}
			if severity != tt.want {
				t.Fatalf("severity = %q, want %q", severity, tt.want)
			}
		})
	}
}
