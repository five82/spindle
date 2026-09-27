package ui

import (
	"strings"
	"testing"

	"github.com/five82/flyer/internal/spindle"
)

func TestContentIDCompletedAndPending(t *testing.T) {
	styles := GetTheme("Slate").Styles()
	for _, tc := range []struct {
		name   string
		cid    *spindle.ContentID
		want   []string
		absent []string
	}{
		{"missing", nil, nil, []string{"ID", "Ref"}},
		{"empty method", &spindle.ContentID{Method: "  "}, nil, []string{"Ref"}},
		{"pending", &spindle.ContentID{Method: "audio", MatchedEpisodes: 3, UnresolvedEpisodes: 2, LowConfidenceCount: 1, ReferenceSource: "catalog", ReferenceEpisodes: 5}, []string{"audio", "3 matched", "2 unresolved", "1 low confidence", "catalog", "5 reference episodes"}, []string{"not contiguous", "not synchronized"}},
		{"completed", &spindle.ContentID{Method: "fingerprint", Completed: true}, []string{"fingerprint", "not contiguous", "not synchronized"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			renderContentID(fieldWriter{b: &b, styles: styles, width: 100}, spindle.QueueItem{ContentID: tc.cid})
			got := stripANSI(b.String())
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%q missing %q", got, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Fatalf("%q contains %q", got, absent)
				}
			}
		})
	}
}

func TestAttentionIncludesRecoveryDetails(t *testing.T) {
	m := New(Options{})
	m.width = 100
	item := spindle.QueueItem{ID: 4, Stage: "failed", NeedsReview: true, FailedAtStage: "encoding", Encoding: &spindle.EncodingStatus{
		Error:   &spindle.EncodingIssue{Title: "Disk full", Context: "output drive", Suggestion: "free space"},
		Warning: "fallback encoder", Validation: &spindle.EncodingValidation{Steps: []spindle.EncodingValidationStep{{Name: "checksum", Passed: true}, {Details: "mismatch", Passed: false}}},
	}}
	got := stripANSI(m.renderDetailContent(item, 100))
	for _, want := range []string{"Attention", "Needs operator review", "Disk full", "output drive", "free space", "fallback encoder", "checksum", "Check", "mismatch", "Failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("attention missing %q in %q", want, got)
		}
	}
}

func TestValidationAndIdentificationOutput(t *testing.T) {
	styles := GetTheme("Slate").Styles()
	for _, tc := range []struct {
		name       string
		validation *spindle.EncodingValidation
		want       string
	}{
		{"empty", nil, ""},
		{"no steps", &spindle.EncodingValidation{Passed: true}, ""},
		{"failed", &spindle.EncodingValidation{Steps: []spindle.EncodingValidationStep{{Name: "checksum"}}}, "Failed"},
		{"passed", &spindle.EncodingValidation{Passed: true, Steps: []spindle.EncodingValidationStep{{Name: "checksum", Passed: true, Details: "verified"}, {Name: "", Passed: true}, {Name: "codec", Passed: false}}}, "verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			renderValidationSummary(fieldWriter{b: &b, styles: styles, width: 100}, spindle.QueueItem{Encoding: &spindle.EncodingStatus{Validation: tc.validation}})
			if got := stripANSI(b.String()); !strings.Contains(got, tc.want) {
				t.Fatalf("validation = %q, want %q", got, tc.want)
			}
		})
	}
}
