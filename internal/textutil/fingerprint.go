package textutil

import (
	"math"
	"regexp"
	"strings"
)

// tokenSplitPattern matches non-alphanumeric character sequences for tokenization.
var tokenSplitPattern = regexp.MustCompile(`[^a-z0-9]+`)

// Fingerprint represents a term-frequency vector for text similarity comparison.
type Fingerprint struct {
	tokens map[string]float64
	norm   float64
}

// NewFingerprint creates a fingerprint from the provided text.
// Returns nil if the text produces no valid tokens.
func NewFingerprint(text string) *Fingerprint {
	tokens := Tokenize(text)
	if len(tokens) == 0 {
		return nil
	}
	counts := make(map[string]float64, len(tokens))
	for _, token := range tokens {
		counts[token]++
	}
	var norm float64
	for _, count := range counts {
		norm += count * count
	}
	return &Fingerprint{
		tokens: counts,
		norm:   math.Sqrt(norm),
	}
}

// Tokenize splits text into lowercase tokens, filtering short tokens.
func Tokenize(text string) []string {
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

// TokenCount returns the number of unique tokens in the fingerprint.
func (f *Fingerprint) TokenCount() int {
	if f == nil {
		return 0
	}
	return len(f.tokens)
}

// WithIDF returns a new Fingerprint with TF-IDF weights applied.
// Each term's count is multiplied by its IDF weight. The norm is recomputed.
// Terms absent from the IDF map retain their original weight.
func (f *Fingerprint) WithIDF(idf map[string]float64) *Fingerprint {
	if f == nil || len(idf) == 0 {
		return f
	}
	weighted := make(map[string]float64, len(f.tokens))
	var norm float64
	for token, count := range f.tokens {
		w := count
		if idfVal, ok := idf[token]; ok {
			w *= idfVal
		}
		if w == 0 {
			continue
		}
		weighted[token] = w
		norm += w * w
	}
	if len(weighted) == 0 {
		return nil
	}
	return &Fingerprint{
		tokens: weighted,
		norm:   math.Sqrt(norm),
	}
}

// Corpus collects document frequency statistics for IDF computation.
type Corpus struct {
	docCount int
	docFreq  map[string]int
}

// NewCorpus creates an empty corpus.
func NewCorpus() *Corpus {
	return &Corpus{docFreq: make(map[string]int)}
}

// Add registers a fingerprint's unique terms in the corpus.
func (c *Corpus) Add(fp *Fingerprint) {
	if c == nil || fp == nil {
		return
	}
	c.docCount++
	for token := range fp.tokens {
		c.docFreq[token]++
	}
}

// IDF computes inverse document frequency weights: log((N+1)/(1+df)) for each term.
func (c *Corpus) IDF() map[string]float64 {
	if c == nil || c.docCount == 0 {
		return nil
	}
	idf := make(map[string]float64, len(c.docFreq))
	n := float64(c.docCount)
	for term, df := range c.docFreq {
		idf[term] = math.Log((n + 1) / (1 + float64(df)))
	}
	return idf
}
