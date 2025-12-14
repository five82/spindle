package commentaryid

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

const (
	minTokensPerWindow        = 20
	sameAsPrimaryCosine       = 0.93
	sameAsPrimaryPurity       = 0.92
	sameAsPrimaryCoverage     = 0.92
	minTextForNonMusicOverall = 40
)

var tokenSplitPattern = regexp.MustCompile(`[^a-z0-9]+`)

type tokenVector struct {
	counts map[string]float64
	norm   float64
	total  float64
}

func newTokenVector(text string) *tokenVector {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return nil
	}
	counts := make(map[string]float64, len(tokens))
	for _, token := range tokens {
		counts[token]++
	}
	var norm float64
	var total float64
	for _, count := range counts {
		total += count
		norm += count * count
	}
	if total <= 0 {
		return nil
	}
	return &tokenVector{
		counts: counts,
		norm:   math.Sqrt(norm),
		total:  total,
	}
}

func tokenize(text string) []string {
	lowered := strings.ToLower(text)
	raw := tokenSplitPattern.Split(lowered, -1)
	terms := make([]string, 0, len(raw))
	for _, token := range raw {
		token = strings.TrimSpace(token)
		if len(token) < 3 {
			continue
		}
		terms = append(terms, token)
	}
	return terms
}

func cosineSimilarity(a, b *tokenVector) float64 {
	if a == nil || b == nil || a.norm == 0 || b.norm == 0 {
		return 0
	}
	var dot float64
	for token, count := range a.counts {
		if other, ok := b.counts[token]; ok {
			dot += count * other
		}
	}
	if dot == 0 {
		return 0
	}
	return dot / (a.norm * b.norm)
}

func overlapWeighted(a, b *tokenVector) float64 {
	if a == nil || b == nil {
		return 0
	}
	var overlap float64
	for token, count := range a.counts {
		if other, ok := b.counts[token]; ok {
			if other < count {
				overlap += other
			} else {
				overlap += count
			}
		}
	}
	return overlap
}

type similarityWindow struct {
	cosine   float64
	purity   float64
	coverage float64
	valid    bool
}

func compareWindow(primaryText, candidateText string) similarityWindow {
	pv := newTokenVector(primaryText)
	cv := newTokenVector(candidateText)
	if pv == nil || cv == nil || pv.total < minTokensPerWindow || cv.total < minTokensPerWindow {
		return similarityWindow{valid: false}
	}
	overlap := overlapWeighted(pv, cv)
	purity := 0.0
	coverage := 0.0
	if cv.total > 0 {
		purity = overlap / cv.total
	}
	if pv.total > 0 {
		coverage = overlap / pv.total
	}
	return similarityWindow{
		cosine:   cosineSimilarity(pv, cv),
		purity:   purity,
		coverage: coverage,
		valid:    true,
	}
}

type similaritySummary struct {
	cosineMedian   float64
	purityMedian   float64
	coverageMedian float64
	validWindows   int
}

func summarizeSimilarity(windows []similarityWindow) similaritySummary {
	cos := make([]float64, 0, len(windows))
	pur := make([]float64, 0, len(windows))
	cov := make([]float64, 0, len(windows))
	valid := 0
	for _, w := range windows {
		if !w.valid {
			continue
		}
		valid++
		cos = append(cos, w.cosine)
		pur = append(pur, w.purity)
		cov = append(cov, w.coverage)
	}
	return similaritySummary{
		cosineMedian:   median(cos),
		purityMedian:   median(pur),
		coverageMedian: median(cov),
		validWindows:   valid,
	}
}

func isSameAsPrimary(windows []similarityWindow) bool {
	passes := 0
	valid := 0
	for _, w := range windows {
		if !w.valid {
			continue
		}
		valid++
		if w.cosine >= sameAsPrimaryCosine && w.purity >= sameAsPrimaryPurity && w.coverage >= sameAsPrimaryCoverage {
			passes++
		}
	}
	if valid == 0 {
		return false
	}
	// Require a majority of windows to pass to avoid dropping mixed/commentary tracks.
	return passes >= (valid/2)+1
}

func likelyMusicOnly(allCandidateText string) bool {
	return len(tokenize(allCandidateText)) < minTextForNonMusicOverall
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}
