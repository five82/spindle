package contentid

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/five82/spindle/internal/opensubtitles"
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
	EpisodeNumber int
	Title         string
	Vector        *textutil.Fingerprint
	RawVector     *textutil.Fingerprint
	FileID        int
	Language      string
	CachePath     string
}

type matchResult struct {
	EpisodeKey           string
	TitleID              int
	TargetEpisode        int
	Score                float64
	ConfidenceQuality    string
	RunnerUpEpisode      int
	RunnerUpScore        float64
	ScoreMargin          float64
	ReverseRunnerUpKey   string
	ReverseRunnerUpScore float64
	ReverseScoreMargin   float64
	SubtitleFileID       int
	SubtitleLanguage     string
	SubtitlePath         string
}

var srtTimestampRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2},\d{3}\s*-->`)

// readSRTText reads an SRT file and extracts only the text lines,
// skipping sequence numbers, timestamps, and empty lines.
func readSRTText(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || isDigitsOnly(line) || srtTimestampRe.MatchString(line) {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(line)
	}
	return sb.String()
}

func isDigitsOnly(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
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
	trimmed := strings.TrimSpace(extractPlainText(cleaned))
	if trimmed == "" {
		return "", fmt.Errorf("subtitle payload contained no text")
	}
	return trimmed, nil
}

func extractPlainText(content string) string {
	var sb strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || isDigitsOnly(line) || srtTimestampRe.MatchString(line) {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(line)
	}
	return sb.String()
}

func cloneRipFingerprints(in []ripFingerprint) []ripFingerprint {
	out := make([]ripFingerprint, len(in))
	copy(out, in)
	return out
}

func cloneReferenceFingerprints(in []referenceFingerprint) []referenceFingerprint {
	out := make([]referenceFingerprint, len(in))
	copy(out, in)
	return out
}
