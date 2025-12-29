package subtitles

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"spindle/internal/subtitles/opensubtitles"
)

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
