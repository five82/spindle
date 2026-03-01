package contentid

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
	}
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

	return p
}
