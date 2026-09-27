package contentid

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/opensubtitles"
)

func TestReferenceChoiceLogsFallbackAndSuspectReason(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logReferenceSelection(nil, 1, 2, candidateChoice{})
	logReferenceSelection(logger, 1, 2, candidateChoice{})
	result := &opensubtitles.SubtitleResult{ID: "sub1"}
	for _, tc := range []struct {
		name   string
		choice candidateChoice
		want   string
	}{
		{"clean", candidateChoice{Result: result}, "decision_result=selected"},
		{"fallback", candidateChoice{Result: result, Fallback: true, Reason: "fallback_to_non_suspect_candidate"}, "decision_result=fallback_selected"},
		{"suspect", candidateChoice{Result: result, Suspect: true, Reason: "conflicting title"}, "decision_result=selected_suspect"},
	} {
		output.Reset()
		logReferenceSelection(logger, 1, 2, tc.choice)
		if !strings.Contains(output.String(), tc.want) || !strings.Contains(output.String(), "subtitle_result_id=sub1") {
			t.Fatalf("%s: %s", tc.name, output.String())
		}
	}
}
