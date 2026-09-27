package contentid

import (
	"testing"

	"github.com/five82/spindle/internal/config"
)

func TestPolicyOverridesAndNormalization(t *testing.T) {
	defaults := DefaultPolicy()
	if got := policyFromConfig(nil); got != defaults {
		t.Fatalf("nil config: %+v", got)
	}
	cfg := &config.Config{}
	cfg.ContentID.MinSimilarityScore = .6
	cfg.ContentID.ClearMatchMargin = .04
	cfg.ContentID.LowConfidenceReviewThreshold = .65
	cfg.ContentID.DecisiveAutoAcceptThreshold = .78
	cfg.ContentID.ClearConfidenceThreshold = .9
	want := Policy{.6, .04, .65, .78, .9}
	if got := policyFromConfig(cfg); got != want {
		t.Fatalf("overrides: %+v, want %+v", got, want)
	}
	for _, tc := range []struct {
		name   string
		policy Policy
		want   Policy
	}{
		{"invalid bounds", Policy{1, -1, 1, -1, 1}, defaults},
		{"inverted thresholds", Policy{.6, .04, .82, .8, .9}, Policy{.6, .04, defaults.LowConfidenceReviewThreshold, defaults.DecisiveAutoAcceptThreshold, defaults.ClearConfidenceThreshold}},
		{"decisive above clear", Policy{.6, .04, .65, .95, .9}, Policy{.6, .04, defaults.LowConfidenceReviewThreshold, defaults.DecisiveAutoAcceptThreshold, defaults.ClearConfidenceThreshold}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.normalized(); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
