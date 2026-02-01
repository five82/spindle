package subtitles

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"spindle/internal/subtitles/opensubtitles"
)

var titleNormalizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// titleStopWords are common articles excluded from word overlap comparison
// to prevent false matches like "The Freshman" matching "The Godfather".
var titleStopWords = map[string]bool{
	"the": true,
	"a":   true,
	"an":  true,
}

type scoredSubtitle struct {
	subtitle opensubtitles.Subtitle
	score    float64
	reasons  []string
}

func rankSubtitleCandidates(subs []opensubtitles.Subtitle, preferred []string, ctx SubtitleContext) []scoredSubtitle {
	preferred = normalizeLanguageList(preferred)
	if len(subs) == 0 {
		return nil
	}
	var (
		preferredHuman []scoredSubtitle
		preferredAI    []scoredSubtitle
		fallbackHuman  []scoredSubtitle
		fallbackAI     []scoredSubtitle
	)
	for _, sub := range subs {
		if sub.FileID == 0 {
			continue
		}
		// Hard reject candidates with mismatched feature titles to prevent
		// using subtitles from wrong movies that happen to pass duration checks
		if isTitleMismatch(ctx.Title, sub.FeatureTitle) {
			continue
		}
		entry := scoredSubtitle{
			subtitle: sub,
		}
		entry.score, entry.reasons = scoreSubtitleCandidate(sub, ctx)
		if len(preferred) == 0 {
			if sub.AITranslated {
				fallbackAI = append(fallbackAI, entry)
				continue
			}
			fallbackHuman = append(fallbackHuman, entry)
			continue
		}
		if languageMatches(sub.Language, preferred) {
			if sub.AITranslated {
				preferredAI = append(preferredAI, entry)
			} else {
				preferredHuman = append(preferredHuman, entry)
			}
			continue
		}
		if sub.AITranslated {
			fallbackAI = append(fallbackAI, entry)
		} else {
			fallbackHuman = append(fallbackHuman, entry)
		}
	}
	ordered := make([]scoredSubtitle, 0, len(subs))
	for _, bucket := range [][]scoredSubtitle{preferredHuman, preferredAI, fallbackHuman, fallbackAI} {
		if len(bucket) == 0 {
			continue
		}
		sort.Slice(bucket, func(i, j int) bool {
			if bucket[i].score == bucket[j].score {
				if bucket[i].subtitle.Downloads == bucket[j].subtitle.Downloads {
					return bucket[i].subtitle.FileID < bucket[j].subtitle.FileID
				}
				return bucket[i].subtitle.Downloads > bucket[j].subtitle.Downloads
			}
			return bucket[i].score > bucket[j].score
		})
		ordered = append(ordered, bucket...)
	}
	return ordered
}

func scoreSubtitleCandidate(sub opensubtitles.Subtitle, ctx SubtitleContext) (float64, []string) {
	var reasons []string
	base := math.Log1p(math.Max(0, float64(sub.Downloads)))
	score := base
	reasons = append(reasons, fmt.Sprintf("downloads=%.2f", base))

	releaseScore, releaseReasons := releaseMatchScore(sub.Release)
	score += releaseScore
	reasons = append(reasons, releaseReasons...)

	// Title matching - reject candidates with mismatched feature titles
	titleScore, titleReason := titleMatchScore(ctx.Title, sub.FeatureTitle)
	score += titleScore
	if titleReason != "" {
		reasons = append(reasons, titleReason)
	}

	if ctxYear := parseContextYear(ctx.Year); ctxYear > 0 && sub.FeatureYear > 0 {
		delta := math.Abs(float64(ctxYear - sub.FeatureYear))
		switch {
		case delta == 0:
			score += 1.5
			reasons = append(reasons, "year=exact")
		case delta <= 1:
			score += 1.0
			reasons = append(reasons, "year=close")
		case delta <= 3:
			score -= 0.5
			reasons = append(reasons, "year=off")
		case delta <= 5:
			score -= 1.5
			reasons = append(reasons, "year=far")
		default:
			// Large year differences (>5 years) strongly suggest wrong movie,
			// especially in franchises with similar titles
			score -= 4.0
			reasons = append(reasons, "year=wrong")
		}
	}

	ctxType := canonicalMediaType(ctx.MediaType)
	candidateType := canonicalMediaType(sub.FeatureType)
	if ctxType != "" && candidateType != "" && ctxType != candidateType {
		score -= 1.0
		reasons = append(reasons, "media_type=mismatch")
	}

	if sub.HD {
		score += 0.5
		reasons = append(reasons, "flag=hd")
	}
	if sub.HearingImpaired {
		score -= 0.5
		reasons = append(reasons, "flag=hi")
	}
	if sub.AITranslated {
		score -= 4.0
		reasons = append(reasons, "flag=ai")
	}

	return score, reasons
}

// titleMatchLevel indicates how closely two titles match.
type titleMatchLevel int

const (
	titleMatchNone    titleMatchLevel = iota // No meaningful match
	titleMatchPartial                        // At least 50% word overlap
	titleMatchContain                        // One title contains the other
	titleMatchExact                          // Exact match after normalization
)

// compareTitles determines how closely two titles match after normalization.
func compareTitles(expected, candidate string) titleMatchLevel {
	expectedNorm := normalizeTitle(expected)
	candidateNorm := normalizeTitle(candidate)

	if expectedNorm == "" || candidateNorm == "" {
		return titleMatchExact // Can't determine mismatch, treat as match
	}

	if expectedNorm == candidateNorm {
		return titleMatchExact
	}

	if strings.Contains(candidateNorm, expectedNorm) || strings.Contains(expectedNorm, candidateNorm) {
		return titleMatchContain
	}

	expectedWords := normalizeTitleWords(expected)
	candidateWords := normalizeTitleWords(candidate)
	if len(expectedWords) > 0 && len(candidateWords) > 0 {
		matches := 0
		for _, ew := range expectedWords {
			if slices.Contains(candidateWords, ew) {
				matches++
			}
		}
		if float64(matches)/float64(len(expectedWords)) >= 0.5 {
			return titleMatchPartial
		}
	}

	return titleMatchNone
}

// titleMatchScore compares the expected title against the candidate's feature title.
// Returns a score adjustment and reason string.
func titleMatchScore(expected, candidate string) (float64, string) {
	level := compareTitles(expected, candidate)
	// compareTitles returns titleMatchExact when either title is empty (can't determine mismatch)
	if level == titleMatchExact && (normalizeTitle(expected) == "" || normalizeTitle(candidate) == "") {
		return 0, ""
	}
	switch level {
	case titleMatchExact:
		return 1.0, "title=exact"
	case titleMatchContain:
		return 0.5, "title=contains"
	case titleMatchPartial:
		return 0, "title=partial"
	default:
		return -10.0, "title=mismatch"
	}
}

// normalizeTitle converts a title to lowercase and removes non-alphanumeric characters.
func normalizeTitle(title string) string {
	return titleNormalizeRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "")
}

// isTitleMismatch returns true if the candidate title clearly doesn't match
// the expected title. Used to hard-reject wrongly labeled subtitles.
func isTitleMismatch(expected, candidate string) bool {
	return compareTitles(expected, candidate) == titleMatchNone
}

// isTitleStrictMismatch returns true if the candidate title doesn't meet strict
// matching criteria (must contain or exactly match the expected title).
// Used for forced subtitles where partial word overlap is insufficient.
func isTitleStrictMismatch(expected, candidate string) bool {
	level := compareTitles(expected, candidate)
	// Only accept titleMatchExact or titleMatchContain
	return level != titleMatchExact && level != titleMatchContain
}

// normalizeTitleWords splits a title into normalized words for overlap comparison.
// Filters out common stop words (the, a, an) to prevent false matches.
func normalizeTitleWords(title string) []string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(title)))
	result := make([]string, 0, len(words))
	for _, w := range words {
		normalized := titleNormalizeRe.ReplaceAllString(w, "")
		if normalized == "" {
			continue
		}
		// Skip common articles that cause false matches
		if titleStopWords[normalized] {
			continue
		}
		result = append(result, normalized)
	}
	return result
}

func releaseMatchScore(release string) (float64, []string) {
	release = strings.ToLower(strings.TrimSpace(release))
	if release == "" {
		return 0, nil
	}
	var (
		score   float64
		reasons []string
	)
	apply := func(delta float64, label string, patterns ...string) {
		for _, pattern := range patterns {
			if strings.Contains(release, pattern) {
				score += delta
				reasons = append(reasons, label)
				return
			}
		}
	}
	apply(3.0, "release=bluray", "bluray", "blu-ray", "bdrip", "brrip")
	apply(2.5, "release=remux", "remux")
	apply(1.5, "release=uhd", "2160p", "uhd", "4k")
	apply(1.0, "release=1080p", "1080p")
	apply(0.5, "release=720p", "720p")
	apply(-2.0, "release=web", "webrip", "web-dl", "webdl")
	apply(-1.0, "release=sd", "hdrip", "dvdrip", "tvrip", "hdtv")
	apply(-4.0, "release=cam", "cam", "telesync", "telecine", "ts", "tc", "scr", "screener")
	apply(-1.5, "release=hardcoded", "hcsub", "hardcoded")
	return score, reasons
}

func parseContextYear(value string) int {
	value = strings.TrimSpace(value)
	if len(value) >= 4 {
		year, err := strconv.Atoi(value[:4])
		if err == nil {
			return year
		}
	}
	return 0
}

func canonicalMediaType(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "movie", "film":
		return "movie"
	case "episode", "tv", "series", "tv_show", "television":
		return "episode"
	default:
		return ""
	}
}

func languageMatches(language string, preferred []string) bool {
	if len(preferred) == 0 {
		return true
	}
	for _, lang := range preferred {
		if strings.EqualFold(lang, language) {
			return true
		}
	}
	return false
}
