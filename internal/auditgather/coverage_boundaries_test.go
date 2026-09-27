package auditgather

import (
	"strings"
	"testing"

	"github.com/five82/spindle/internal/ripspec"
)

func TestAuditPathBoundariesAndMissingAnalysis(t *testing.T) {
	if normalizeAuditPath("") != "" {
		t.Fatal("empty path changed")
	}
	for _, tc := range []struct {
		path, root string
		want       bool
	}{
		{"/library/season/../movie.mkv", "/library", true},
		{"/library", "/library", true},
		{"/library-other/movie.mkv", "/library", false},
		{"/library/movie.mkv", "", false},
	} {
		if got := pathWithinRoot(tc.path, tc.root); got != tc.want {
			t.Errorf("pathWithinRoot(%q, %q) = %v", tc.path, tc.root, got)
		}
	}
	if got := computeAudioSummary(nil, nil); got != nil {
		t.Fatalf("nil report: %+v", got)
	}
	r := &Report{Envelope: &ripspec.Envelope{}}
	if got := computeAudioSummary(r, nil); got != nil {
		t.Fatalf("empty audio: %+v", got)
	}
	if got := computeSubtitleSummary(r, nil); got != nil {
		t.Fatalf("empty subtitles: %+v", got)
	}
	if got := computeRoutingSummary(r); got != nil {
		t.Fatalf("empty final assets: %+v", got)
	}
	if got := detectFinalValidationAnomalies(r); len(got) != 0 {
		t.Fatalf("no final assets: %+v", got)
	}
	r.Envelope.Assets.Final = []ripspec.Asset{{EpisodeKey: "main", Path: "/library/movie.mkv", Status: ripspec.AssetStatusCompleted}}
	anomalies := detectFinalValidationAnomalies(r)
	if len(anomalies) != 1 || !strings.Contains(anomalies[0].Message, "without a persisted") {
		t.Fatalf("missing verdict: %+v", anomalies)
	}
}
