package contentid

import "github.com/five82/spindle/internal/config"

// Policy centralizes content ID thresholds and strategy rules.
type Policy struct {
	MinSimilarityScore           float64
	LowConfidenceReviewThreshold float64
	LLMVerifyThreshold           float64
	AnchorMinScore               float64
	AnchorMinScoreMargin         float64
	BlockHighConfidenceDelta     float64
	BlockHighConfidenceTopRatio  float64
	DiscBlockPaddingMin          int
	DiscBlockPaddingDivisor      int
	Disc1MustStartAtEpisode1     bool
	Disc2PlusMinStartEpisode     int
	ReferenceSkipPenalty         float64
	UnresolvedPenalty            float64
	WindowMaxSlack               int
	ScoreMarginTarget            float64
	ReverseMarginTarget          float64
	NeighborMarginTarget         float64
	PathMarginTarget             float64
	VerifyNeighborMargin         float64
	VerifyPathMargin             float64
}

// DefaultPolicy returns conservative defaults tuned for multi-episode TV discs.
func DefaultPolicy() Policy {
	return Policy{
		MinSimilarityScore:           0.58,
		LowConfidenceReviewThreshold: 0.70,
		LLMVerifyThreshold:           0.85,
		AnchorMinScore:               0.63,
		AnchorMinScoreMargin:         0.03,
		BlockHighConfidenceDelta:     0.05,
		BlockHighConfidenceTopRatio:  0.70,
		DiscBlockPaddingMin:          2,
		DiscBlockPaddingDivisor:      4,
		Disc1MustStartAtEpisode1:     true,
		Disc2PlusMinStartEpisode:     2,
		ReferenceSkipPenalty:         0.12,
		UnresolvedPenalty:            0.08,
		WindowMaxSlack:               2,
		ScoreMarginTarget:            0.05,
		ReverseMarginTarget:          0.05,
		NeighborMarginTarget:         0.03,
		PathMarginTarget:             0.12,
		VerifyNeighborMargin:         0.02,
		VerifyPathMargin:             0.08,
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
	if cfg.ContentID.LowConfidenceReviewThreshold > 0 {
		p.LowConfidenceReviewThreshold = cfg.ContentID.LowConfidenceReviewThreshold
	}
	if cfg.ContentID.LLMVerifyThreshold > 0 {
		p.LLMVerifyThreshold = cfg.ContentID.LLMVerifyThreshold
	}
	if cfg.ContentID.AnchorMinScore > 0 {
		p.AnchorMinScore = cfg.ContentID.AnchorMinScore
	}
	if cfg.ContentID.AnchorMinScoreMargin > 0 {
		p.AnchorMinScoreMargin = cfg.ContentID.AnchorMinScoreMargin
	}
	if cfg.ContentID.BlockHighConfidenceDelta > 0 {
		p.BlockHighConfidenceDelta = cfg.ContentID.BlockHighConfidenceDelta
	}
	if cfg.ContentID.BlockHighConfidenceTopRatio > 0 {
		p.BlockHighConfidenceTopRatio = cfg.ContentID.BlockHighConfidenceTopRatio
	}
	if cfg.ContentID.DiscBlockPaddingMin > 0 {
		p.DiscBlockPaddingMin = cfg.ContentID.DiscBlockPaddingMin
	}
	if cfg.ContentID.DiscBlockPaddingDivisor > 0 {
		p.DiscBlockPaddingDivisor = cfg.ContentID.DiscBlockPaddingDivisor
	}
	p.Disc1MustStartAtEpisode1 = cfg.ContentID.Disc1MustStartAtEpisode1
	if cfg.ContentID.Disc2PlusMinStartEpisode > 0 {
		p.Disc2PlusMinStartEpisode = cfg.ContentID.Disc2PlusMinStartEpisode
	}
	return p.normalized()
}

func (p Policy) normalized() Policy {
	d := DefaultPolicy()
	if p.MinSimilarityScore <= 0 || p.MinSimilarityScore >= 1 {
		p.MinSimilarityScore = d.MinSimilarityScore
	}
	if p.LowConfidenceReviewThreshold <= 0 || p.LowConfidenceReviewThreshold >= 1 {
		p.LowConfidenceReviewThreshold = d.LowConfidenceReviewThreshold
	}
	if p.LLMVerifyThreshold <= 0 || p.LLMVerifyThreshold >= 1 {
		p.LLMVerifyThreshold = d.LLMVerifyThreshold
	}
	if p.AnchorMinScore <= 0 || p.AnchorMinScore >= 1 {
		p.AnchorMinScore = d.AnchorMinScore
	}
	if p.AnchorMinScoreMargin <= 0 || p.AnchorMinScoreMargin >= 1 {
		p.AnchorMinScoreMargin = d.AnchorMinScoreMargin
	}
	if p.BlockHighConfidenceDelta <= 0 || p.BlockHighConfidenceDelta >= 1 {
		p.BlockHighConfidenceDelta = d.BlockHighConfidenceDelta
	}
	if p.BlockHighConfidenceTopRatio <= 0 || p.BlockHighConfidenceTopRatio > 1 {
		p.BlockHighConfidenceTopRatio = d.BlockHighConfidenceTopRatio
	}
	if p.DiscBlockPaddingMin <= 0 {
		p.DiscBlockPaddingMin = d.DiscBlockPaddingMin
	}
	if p.DiscBlockPaddingDivisor <= 0 {
		p.DiscBlockPaddingDivisor = d.DiscBlockPaddingDivisor
	}
	if p.Disc2PlusMinStartEpisode <= 0 {
		p.Disc2PlusMinStartEpisode = d.Disc2PlusMinStartEpisode
	}
	if p.ReferenceSkipPenalty <= 0 {
		p.ReferenceSkipPenalty = d.ReferenceSkipPenalty
	}
	if p.UnresolvedPenalty <= 0 {
		p.UnresolvedPenalty = d.UnresolvedPenalty
	}
	if p.WindowMaxSlack <= 0 {
		p.WindowMaxSlack = d.WindowMaxSlack
	}
	if p.ScoreMarginTarget <= 0 {
		p.ScoreMarginTarget = d.ScoreMarginTarget
	}
	if p.ReverseMarginTarget <= 0 {
		p.ReverseMarginTarget = d.ReverseMarginTarget
	}
	if p.NeighborMarginTarget <= 0 {
		p.NeighborMarginTarget = d.NeighborMarginTarget
	}
	if p.PathMarginTarget <= 0 {
		p.PathMarginTarget = d.PathMarginTarget
	}
	if p.VerifyNeighborMargin <= 0 {
		p.VerifyNeighborMargin = d.VerifyNeighborMargin
	}
	if p.VerifyPathMargin <= 0 {
		p.VerifyPathMargin = d.VerifyPathMargin
	}
	return p
}
