package contentid

import "github.com/five82/spindle/internal/config"

// Policy centralizes content ID thresholds.
type Policy struct {
	MinSimilarityScore           float64
	ClearMatchMargin             float64
	LowConfidenceReviewThreshold float64
	LLMVerifyThreshold           float64
}

// DefaultPolicy returns conservative defaults for the content-first TV matcher.
func DefaultPolicy() Policy {
	return Policy{
		MinSimilarityScore:           0.58,
		ClearMatchMargin:             0.05,
		LowConfidenceReviewThreshold: 0.70,
		LLMVerifyThreshold:           0.85,
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
	if cfg.ContentID.LLMVerifyThreshold > 0 {
		p.LLMVerifyThreshold = cfg.ContentID.LLMVerifyThreshold
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
	if p.LLMVerifyThreshold <= 0 || p.LLMVerifyThreshold >= 1 {
		p.LLMVerifyThreshold = d.LLMVerifyThreshold
	}
	return p
}
