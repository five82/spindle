package textutil

import (
	"regexp"
	"strings"
)

var splitRe = regexp.MustCompile(`[^a-z0-9]+`)

// Tokenize splits text into lowercase tokens, filtering tokens shorter than 3 characters.
func Tokenize(text string) []string {
	lower := strings.ToLower(text)
	parts := splitRe.Split(lower, -1)
	var tokens []string
	for _, p := range parts {
		if len(p) >= 3 {
			tokens = append(tokens, p)
		}
	}
	return tokens
}
