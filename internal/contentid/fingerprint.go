package contentid

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/textutil"
)

type ripFingerprint struct {
	EpisodeKey string
	TitleID    int
	Path       string
	Vector     *textutil.Fingerprint
	RawVector  *textutil.Fingerprint
}

type referenceFingerprint struct {
	EpisodeNumber  int
	Title          string
	Vector         *textutil.Fingerprint
	RawVector      *textutil.Fingerprint
	FileID         int
	Language       string
	CachePath      string
	Suspect        bool
	SuspectReason  string
	CandidateScore float64
}

type matchResult struct {
	EpisodeKey              string
	TitleID                 int
	TargetEpisode           int
	Score                   float64
	Confidence              float64
	ConfidenceQuality       string
	Strength                float64
	RunnerUpEpisode         int
	RunnerUpScore           float64
	ScoreMargin             float64
	EpisodeRunnerUpKey      string
	EpisodeRunnerUpScore    float64
	EpisodeScoreMargin      float64
	NeighborRunnerUpEpisode int
	NeighborRunnerUpScore   float64
	NeighborScoreMargin     float64
	AcceptedBy              string
	NeedsVerification       bool
	VerificationReason      string
	SubtitleFileID          int
	SubtitleLanguage        string
	SubtitlePath            string
	ReferenceSuspect        bool
	ReferenceSuspectReason  string
}

// readSRTText reads an SRT file and returns the concatenated cue text,
// suitable for TF-IDF fingerprinting. Returns "" on I/O error.
func readSRTText(path string) string {
	cues, err := srtutil.ParseFile(path)
	if err != nil {
		return ""
	}
	return srtutil.PlainText(cues)
}

func loadPlainText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return normalizeSubtitlePayload(string(data))
}

func normalizeSubtitlePayload(content string) (string, error) {
	cleaned := opensubtitles.CleanSRT(content)
	trimmed := strings.TrimSpace(srtutil.PlainText(srtutil.Parse(cleaned)))
	if trimmed == "" {
		return "", fmt.Errorf("subtitle payload contained no text")
	}
	return trimmed, nil
}

// cloneRipFingerprints and cloneReferenceFingerprints return shallow copies so
// weighting can be applied without mutating the original fingerprints.
func cloneRipFingerprints(in []ripFingerprint) []ripFingerprint {
	return slices.Clone(in)
}

func cloneReferenceFingerprints(in []referenceFingerprint) []referenceFingerprint {
	return slices.Clone(in)
}
