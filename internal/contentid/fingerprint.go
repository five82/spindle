package contentid

import (
	"errors"
	"os"
	"strings"

	"spindle/internal/subtitles"
	"spindle/internal/textutil"
)

type ripFingerprint struct {
	EpisodeKey string
	TitleID    int
	Path       string
	Vector     *textutil.Fingerprint
	RawVector  *textutil.Fingerprint // pre-IDF vector for expansion retries
}

type referenceFingerprint struct {
	EpisodeNumber int
	Title         string
	Vector        *textutil.Fingerprint
	RawVector     *textutil.Fingerprint // pre-IDF vector for expansion retries
	FileID        int64
	Language      string
	CachePath     string
}

type matchResult struct {
	EpisodeKey        string
	TitleID           int
	TargetEpisode     int
	Score             float64
	SubtitleFileID    int64
	SubtitleLanguage  string
	SubtitleCachePath string
}

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

// newFingerprint creates a fingerprint from text using the textutil package.
func newFingerprint(text string) *textutil.Fingerprint {
	return textutil.NewFingerprint(text)
}

// cosineSimilarity computes similarity between two fingerprints using the textutil package.
func cosineSimilarity(a, b *textutil.Fingerprint) float64 {
	return textutil.CosineSimilarity(a, b)
}
