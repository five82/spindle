package contentid

import "github.com/five82/spindle/internal/config"

const (
	ConfidenceQualityClear                 = "clear"
	ConfidenceQualityDecisiveLowSimilarity = "decisive_low_similarity"
	ConfidenceQualityAmbiguous             = "ambiguous"
	ConfidenceQualityContested             = "contested"
)

// Policy centralizes content ID thresholds.
type Policy struct {
	MinSimilarityScore           float64
	ClearMatchMargin             float64
	LowConfidenceReviewThreshold float64
	DecisiveAutoAcceptThreshold  float64
	ClearConfidenceThreshold     float64
}

// DefaultPolicy returns conservative defaults for the content-first TV matcher.
func DefaultPolicy() Policy {
	return Policy{
		MinSimilarityScore:           0.58,
		ClearMatchMargin:             0.05,
		LowConfidenceReviewThreshold: 0.70,
		DecisiveAutoAcceptThreshold:  0.80,
		ClearConfidenceThreshold:     0.85,
	}
}

func policyFromConfig(cfg *config.Config) Policy {
	p := DefaultPolicy()
	if cfg == nil {
		return p
	}
	if cfg.ContentID.MinSimilarityScore > 0 {
		p.MinSimilarityScore = cfg.ContentID.MinSimilarityScore
	}
	if cfg.ContentID.ClearMatchMargin > 0 {
		p.ClearMatchMargin = cfg.ContentID.ClearMatchMargin
	}
	if cfg.ContentID.LowConfidenceReviewThreshold > 0 {
		p.LowConfidenceReviewThreshold = cfg.ContentID.LowConfidenceReviewThreshold
	}
	if cfg.ContentID.DecisiveAutoAcceptThreshold > 0 {
		p.DecisiveAutoAcceptThreshold = cfg.ContentID.DecisiveAutoAcceptThreshold
	}
	if cfg.ContentID.ClearConfidenceThreshold > 0 {
		p.ClearConfidenceThreshold = cfg.ContentID.ClearConfidenceThreshold
	}
	return p.normalized()
}

func (p Policy) normalized() Policy {
	d := DefaultPolicy()
	if p.MinSimilarityScore <= 0 || p.MinSimilarityScore >= 1 {
		p.MinSimilarityScore = d.MinSimilarityScore
	}
	if p.ClearMatchMargin <= 0 || p.ClearMatchMargin >= 1 {
		p.ClearMatchMargin = d.ClearMatchMargin
	}
	if p.LowConfidenceReviewThreshold <= 0 || p.LowConfidenceReviewThreshold >= 1 {
		p.LowConfidenceReviewThreshold = d.LowConfidenceReviewThreshold
	}
	if p.DecisiveAutoAcceptThreshold <= 0 || p.DecisiveAutoAcceptThreshold >= 1 {
		p.DecisiveAutoAcceptThreshold = d.DecisiveAutoAcceptThreshold
	}
	if p.ClearConfidenceThreshold <= 0 || p.ClearConfidenceThreshold >= 1 {
		p.ClearConfidenceThreshold = d.ClearConfidenceThreshold
	}
	if p.DecisiveAutoAcceptThreshold <= p.LowConfidenceReviewThreshold || p.DecisiveAutoAcceptThreshold > p.ClearConfidenceThreshold {
		p.LowConfidenceReviewThreshold = d.LowConfidenceReviewThreshold
		p.DecisiveAutoAcceptThreshold = d.DecisiveAutoAcceptThreshold
		p.ClearConfidenceThreshold = d.ClearConfidenceThreshold
	}
	return p
}

// ClassifyConfidenceQuality classifies an episode match confidence using the supplied policy.
func ClassifyConfidenceQuality(confidence, ripMargin, episodeMargin, neighborMargin float64, referenceSuspect bool, policy Policy) string {
	return classifyDerivedConfidence(confidence, ripMargin, episodeMargin, neighborMargin, referenceSuspect, policy)
}
