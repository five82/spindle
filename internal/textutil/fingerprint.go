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
