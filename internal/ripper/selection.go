package ripper

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/ripspec"
)

const (
	// minPrimaryRuntimeSeconds is the minimum duration for a title to be considered feature-length.
	minPrimaryRuntimeSeconds = 20 * 60
	// durationToleranceSeconds is the window within which titles are considered the same duration.
	durationToleranceSeconds = 2
	// maxLanguageVariantRuntimeDiff is the maximum runtime difference (in seconds) between
	// 800-series playlists that indicates language variants vs different cuts.
	// Disney multi-language discs differ by only a few seconds (localized title cards/credits).
	// Differences greater than this threshold indicate different cuts (theatrical vs director's).
	maxLanguageVariantRuntimeDiff = 30
)

// ChoosePrimaryTitle selects the best title for a movie rip using multi-stage filtering:
//  1. Validate candidates (ID >= 0, Duration > 0)
//  2. Disney 800-series multi-language playlist detection
//  3. Duration window (within 2s of max)
//  4. Feature-length filter (>= 20 min)
//  5. Chapter preference (most chapters)
//  6. MPLS preference over raw M2TS
//  7. Segment count preference (most segments)
//  8. TitleHash fingerprint frequency (most common hash)
//  9. Sort by duration desc, ID asc
func ChoosePrimaryTitle(titles []ripspec.Title) (ripspec.Title, bool) {
	if len(titles) == 0 {
		return ripspec.Title{}, false
	}

	candidates := make([]ripspec.Title, 0, len(titles))
	for _, t := range titles {
		if t.ID < 0 || t.Duration <= 0 {
			continue
		}
		candidates = append(candidates, t)
	}
	if len(candidates) == 0 {
		return ripspec.Title{}, false
	}

	// Early check for Disney/Pixar multi-language discs.
	// These have 00800.mpls (English), 00801.mpls (Spanish), 00802.mpls (French) with
	// the English version often being slightly shorter due to language-specific credits.
	// If we find feature-length 800-series playlists, prefer 00800.mpls before duration filtering.
	if preferred := filterPreferredPlaylistFeatureLength(candidates, minPrimaryRuntimeSeconds); len(preferred) > 0 {
		candidates = preferred
	}

	// Prefer feature-length runtimes within a small tolerance window.
	maxDuration := 0
	for _, t := range candidates {
		if t.Duration > maxDuration {
			maxDuration = t.Duration
		}
	}
	featureLength := make([]ripspec.Title, 0, len(candidates))
	for _, t := range candidates {
		if t.Duration >= maxDuration-durationToleranceSeconds {
			featureLength = append(featureLength, t)
		}
	}
	featureOnly := make([]ripspec.Title, 0, len(featureLength))
	for _, t := range featureLength {
		if t.Duration >= minPrimaryRuntimeSeconds {
			featureOnly = append(featureOnly, t)
		}
	}
	if len(featureOnly) > 0 {
		featureLength = featureOnly
	}

	// Prefer titles with chapter metadata.
	withChapters := bestByInt(featureLength, func(t ripspec.Title) int { return t.Chapters })
	if len(withChapters) > 0 {
		featureLength = withChapters
	}

	// Prefer MPLS playlists over raw M2TS entries.
	mplsOnly := make([]ripspec.Title, 0, len(featureLength))
	for _, t := range featureLength {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(t.Playlist)), ".mpls") {
			mplsOnly = append(mplsOnly, t)
		}
	}
	if len(mplsOnly) > 0 {
		featureLength = mplsOnly
	}

	// Prefer playlists with more segments (helps dodge dummy/short playlists).
	withSegments := bestByInt(featureLength, func(t ripspec.Title) int { return t.SegmentCount })
	if len(withSegments) > 0 {
		featureLength = withSegments
	}

	// Prefer the most common fingerprint if duplicates exist.
	fingerprintFreq := make(map[string]int)
	for _, t := range featureLength {
		fp := strings.TrimSpace(t.TitleHash)
		if fp != "" {
			fingerprintFreq[fp]++
		}
	}
	bestFreq := 0
	for _, freq := range fingerprintFreq {
		if freq > bestFreq {
			bestFreq = freq
		}
	}
	if bestFreq > 1 {
		filtered := make([]ripspec.Title, 0, len(featureLength))
		for _, t := range featureLength {
			if fingerprintFreq[strings.TrimSpace(t.TitleHash)] == bestFreq {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			featureLength = filtered
		}
	}

	sort.Slice(featureLength, func(i, j int) bool {
		left := featureLength[i]
		right := featureLength[j]
		if left.Duration == right.Duration {
			return left.ID < right.ID
		}
		return left.Duration > right.Duration
	})
	return featureLength[0], true
}

// PrimaryTitleDecisionSummary returns the primary selection plus candidate and rejection summaries.
func PrimaryTitleDecisionSummary(titles []ripspec.Title) (ripspec.Title, bool, []string, []string) {
	selection, ok := ChoosePrimaryTitle(titles)
	candidates := make([]string, 0, len(titles))
	rejects := make([]string, 0)
	for _, t := range titles {
		if t.ID < 0 {
			rejects = append(rejects, fmt.Sprintf("%d:invalid_id", t.ID))
			continue
		}
		if t.Duration <= 0 {
			rejects = append(rejects, fmt.Sprintf("%d:duration<=0", t.ID))
			continue
		}
		candidates = append(candidates, fmt.Sprintf("%d:%ds ch=%d seg=%d playlist=%s",
			t.ID, t.Duration, t.Chapters, t.SegmentCount, strings.TrimSpace(t.Playlist)))
	}
	sort.Strings(candidates)
	sort.Strings(rejects)
	return selection, ok, candidates, rejects
}

// filterPreferredPlaylistFeatureLength returns feature-length titles with the preferred 800-series playlist.
// Disney/Pixar discs use 00800.mpls for English, 00801 for Spanish, 00802 for French.
// The English version is often slightly shorter due to language-specific credits.
// Only applies when multiple feature-length 800-series playlists exist with similar runtimes
// (indicating a multi-language disc). If runtimes differ by more than 30 seconds, assumes
// different cuts (e.g., theatrical vs director's cut) and returns empty to let normal
// selection prefer the longer version.
// Returns empty slice if not a multi-language disc pattern.
func filterPreferredPlaylistFeatureLength(titles []ripspec.Title, minRuntime int) []ripspec.Title {
	// Collect feature-length titles with 800-series playlists.
	type scored struct {
		title ripspec.Title
		num   int
	}
	var candidates []scored
	for _, t := range titles {
		if t.Duration < minRuntime {
			continue
		}
		pl := strings.ToLower(strings.TrimSpace(t.Playlist))
		// Match 008XX.mpls pattern (800-899 range).
		if strings.HasPrefix(pl, "008") && strings.HasSuffix(pl, ".mpls") {
			numStr := strings.TrimPrefix(pl, "00")
			numStr = strings.TrimSuffix(numStr, ".mpls")
			if num, err := strconv.Atoi(numStr); err == nil && num >= 800 && num < 900 {
				candidates = append(candidates, scored{title: t, num: num})
			}
		}
	}

	// Only apply if we have multiple 800-series playlists (indicating multi-language disc).
	if len(candidates) < 2 {
		return nil
	}

	// Check if we have different playlist numbers (e.g., 800, 801, 802).
	playlistNums := make(map[int]bool)
	for _, c := range candidates {
		playlistNums[c.num] = true
	}
	if len(playlistNums) < 2 {
		return nil
	}

	// Check runtime variance - if playlists differ by more than the threshold,
	// they are likely different cuts (theatrical vs director's), not language variants.
	minDuration := candidates[0].title.Duration
	maxDuration := minDuration
	for _, c := range candidates[1:] {
		dur := c.title.Duration
		if dur < minDuration {
			minDuration = dur
		}
		if dur > maxDuration {
			maxDuration = dur
		}
	}
	if maxDuration-minDuration > maxLanguageVariantRuntimeDiff {
		return nil
	}

	// Find minimum playlist number (00800 = English).
	minNum := candidates[0].num
	for _, c := range candidates[1:] {
		if c.num < minNum {
			minNum = c.num
		}
	}

	// Return all titles with that minimum playlist number.
	result := make([]ripspec.Title, 0, 1)
	for _, c := range candidates {
		if c.num == minNum {
			result = append(result, c.title)
		}
	}
	return result
}

func bestByInt(list []ripspec.Title, score func(ripspec.Title) int) []ripspec.Title {
	if len(list) == 0 {
		return nil
	}
	scores := make([]int, len(list))
	best := 0
	for i, t := range list {
		v := score(t)
		scores[i] = v
		if v > best {
			best = v
		}
	}
	if best == 0 {
		return nil
	}
	out := make([]ripspec.Title, 0, len(list))
	for i, t := range list {
		if scores[i] == best {
			out = append(out, t)
		}
	}
	return out
}
