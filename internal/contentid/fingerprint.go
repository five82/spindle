package contentid

import (
	"errors"
	"math"
	"os"
	"regexp"
	"strings"

	"spindle/internal/subtitles"
)

type tokenFingerprint struct {
	tokens map[string]float64
	norm   float64
}

type ripFingerprint struct {
	EpisodeKey string
	TitleID    int
	Path       string
	Vector     *tokenFingerprint
}

type referenceFingerprint struct {
	EpisodeNumber int
	Title         string
	Vector        *tokenFingerprint
}

type matchResult struct {
	EpisodeKey    string
	TitleID       int
	TargetEpisode int
	Score         float64
}

var tokenSplitPattern = regexp.MustCompile(`[^a-z0-9]+`)

func loadPlainText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return normalizeSubtitlePayload(data)
}

func normalizeSubtitlePayload(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("subtitle payload empty")
	}
	cleaned, _ := subtitles.CleanSRT(data)
	text := strings.TrimSpace(subtitles.PlainTextFromSRT(cleaned))
	if text == "" {
		return "", errors.New("subtitle payload contained no text")
	}
	return text, nil
}

func newFingerprint(text string) *tokenFingerprint {
	tokens := tokenize(text)
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
	return &tokenFingerprint{
		tokens: counts,
		norm:   math.Sqrt(norm),
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

func cosineSimilarity(a, b *tokenFingerprint) float64 {
	if a == nil || b == nil || a.norm == 0 || b.norm == 0 {
		return 0
	}
	var dot float64
	for token, count := range a.tokens {
		if other, ok := b.tokens[token]; ok {
			dot += count * other
		}
	}
	if dot == 0 {
		return 0
	}
	return dot / (a.norm * b.norm)
}
