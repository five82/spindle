package subtitles

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"spindle/internal/subtitles/opensubtitles"
)

var titleNormalizeRe = regexp.MustCompile(`[^a-z0-9]+`)

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
		default:
			score -= 1.0
			reasons = append(reasons, "year=far")
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

// titleMatchScore compares the expected title against the candidate's feature title.
// Returns a score adjustment and reason string.
func titleMatchScore(expected, candidate string) (float64, string) {
	expectedNorm := normalizeTitle(expected)
	candidateNorm := normalizeTitle(candidate)

	if expectedNorm == "" || candidateNorm == "" {
		return 0, ""
	}

	// Exact match after normalization
	if expectedNorm == candidateNorm {
		return 1.0, "title=exact"
	}

	// Check if one contains the other (handles "Toy Story 3" vs "Toy Story 3 3D")
	if strings.Contains(candidateNorm, expectedNorm) || strings.Contains(expectedNorm, candidateNorm) {
		return 0.5, "title=contains"
	}

	// Check for significant word overlap (at least 50% of words match)
	expectedWords := normalizeTitleWords(expected)
	candidateWords := normalizeTitleWords(candidate)
	if len(expectedWords) > 0 && len(candidateWords) > 0 {
		matches := 0
		for _, ew := range expectedWords {
			for _, cw := range candidateWords {
				if ew == cw {
					matches++
					break
				}
			}
		}
		overlap := float64(matches) / float64(len(expectedWords))
		if overlap >= 0.5 {
			return 0, "title=partial"
		}
	}

	// No meaningful match - this is likely wrong content
	// Apply heavy penalty to push this candidate to the bottom
	return -10.0, "title=mismatch"
}

// normalizeTitle converts a title to lowercase and removes non-alphanumeric characters.
func normalizeTitle(title string) string {
	return titleNormalizeRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(title)), "")
}

// isTitleMismatch returns true if the candidate title clearly doesn't match
// the expected title. Used to hard-reject wrongly labeled subtitles.
func isTitleMismatch(expected, candidate string) bool {
	expectedNorm := normalizeTitle(expected)
	candidateNorm := normalizeTitle(candidate)

	// If either is empty, don't reject (can't determine mismatch)
	if expectedNorm == "" || candidateNorm == "" {
		return false
	}

	// Exact match
	if expectedNorm == candidateNorm {
		return false
	}

	// One contains the other (handles variants like "Toy Story 3" vs "Toy Story 3 3D")
	if strings.Contains(candidateNorm, expectedNorm) || strings.Contains(expectedNorm, candidateNorm) {
		return false
	}

	// Check for significant word overlap (at least 50% of expected words present)
	expectedWords := normalizeTitleWords(expected)
	candidateWords := normalizeTitleWords(candidate)
	if len(expectedWords) > 0 && len(candidateWords) > 0 {
		matches := 0
		for _, ew := range expectedWords {
			for _, cw := range candidateWords {
				if ew == cw {
					matches++
					break
				}
			}
		}
		overlap := float64(matches) / float64(len(expectedWords))
		if overlap >= 0.5 {
			return false
		}
	}

	// No meaningful match - reject this candidate
	return true
}

// normalizeTitleWords splits a title into normalized words for overlap comparison.
func normalizeTitleWords(title string) []string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(title)))
	result := make([]string, 0, len(words))
	for _, w := range words {
		normalized := titleNormalizeRe.ReplaceAllString(w, "")
		if normalized != "" {
			result = append(result, normalized)
		}
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
