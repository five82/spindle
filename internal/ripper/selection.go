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

// partitionValidTitles splits titles into valid candidates (ID >= 0, Duration > 0) and rejects.
func partitionValidTitles(titles []ripspec.Title) (valid, rejected []ripspec.Title) {
	valid = make([]ripspec.Title, 0, len(titles))
	for _, t := range titles {
		if t.ID < 0 || t.Duration <= 0 {
			rejected = append(rejected, t)
			continue
		}
		valid = append(valid, t)
	}
	return valid, rejected
}

// funnelStep records one narrowing stage of the primary-title funnel: the
// rule that ran, how many candidates it kept, which title IDs it eliminated,
// and the numeric evidence behind the cut.
type funnelStep struct {
	Rule       string
	Before     int
	After      int
	Eliminated []int
	Detail     string
}

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
	selection, ok, _ := choosePrimaryTitleTraced(titles)
	return selection, ok
}

// choosePrimaryTitleTraced runs the funnel and records every stage that
// narrowed the field (or declined to, with a reason) so the ripper can log
// the evidence behind the final pick.
func choosePrimaryTitleTraced(titles []ripspec.Title) (ripspec.Title, bool, []funnelStep) {
	candidates, _ := partitionValidTitles(titles)
	if len(candidates) == 0 {
		return ripspec.Title{}, false, nil
	}

	var steps []funnelStep
	record := func(rule string, before, after []ripspec.Title, detail string) {
		if len(after) == len(before) && detail == "" {
			return
		}
		steps = append(steps, funnelStep{
			Rule:       rule,
			Before:     len(before),
			After:      len(after),
			Eliminated: eliminatedTitleIDs(before, after),
			Detail:     detail,
		})
	}

	// Early check for Disney/Pixar multi-language discs.
	// These have 00800.mpls (English), 00801.mpls (Spanish), 00802.mpls (French) with
	// the English version often being slightly shorter due to language-specific credits.
	// If we find feature-length 800-series playlists, prefer 00800.mpls before duration filtering.
	preferred, note := filterPreferredPlaylistFeatureLength(candidates, minPrimaryRuntimeSeconds)
	if len(preferred) > 0 {
		record("disney_800_series", candidates, preferred, note)
		candidates = preferred
	} else if note != "" {
		record("disney_800_series", candidates, candidates, note)
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
	record("duration_window", candidates, featureLength,
		fmt.Sprintf("max_duration=%ds tolerance=%ds", maxDuration, durationToleranceSeconds))

	featureOnly := make([]ripspec.Title, 0, len(featureLength))
	for _, t := range featureLength {
		if t.Duration >= minPrimaryRuntimeSeconds {
			featureOnly = append(featureOnly, t)
		}
	}
	if len(featureOnly) > 0 {
		record("feature_length_gate", featureLength, featureOnly,
			fmt.Sprintf("min_runtime=%ds", minPrimaryRuntimeSeconds))
		featureLength = featureOnly
	}

	// Prefer titles with chapter metadata.
	withChapters := bestByInt(featureLength, func(t ripspec.Title) int { return t.Chapters })
	if len(withChapters) > 0 {
		record("chapter_preference", featureLength, withChapters,
			fmt.Sprintf("max_chapters=%d", withChapters[0].Chapters))
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
		record("mpls_preference", featureLength, mplsOnly, "")
		featureLength = mplsOnly
	}

	// Prefer playlists with more segments (helps dodge dummy/short playlists).
	withSegments := bestByInt(featureLength, func(t ripspec.Title) int { return t.SegmentCount })
	if len(withSegments) > 0 {
		record("segment_count_preference", featureLength, withSegments,
			fmt.Sprintf("max_segments=%d", withSegments[0].SegmentCount))
		featureLength = withSegments
	}

	// Prefer the most common fingerprint if duplicates exist.
	fingerprintFreq := make(map[string]int)
	trimmedHashes := make([]string, len(featureLength))
	for i, t := range featureLength {
		fp := strings.TrimSpace(t.TitleHash)
		trimmedHashes[i] = fp
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
		for i, t := range featureLength {
			if fingerprintFreq[trimmedHashes[i]] == bestFreq {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			record("titlehash_frequency", featureLength, filtered,
				fmt.Sprintf("shared_hash_count=%d", bestFreq))
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
	return featureLength[0], true, steps
}

// eliminatedTitleIDs returns the IDs present in before but not in after.
func eliminatedTitleIDs(before, after []ripspec.Title) []int {
	kept := make(map[int]struct{}, len(after))
	for _, t := range after {
		kept[t.ID] = struct{}{}
	}
	var out []int
	for _, t := range before {
		if _, ok := kept[t.ID]; !ok {
			out = append(out, t.ID)
		}
	}
	return out
}

// PrimaryTitleDecisionSummary returns the primary selection plus candidate and
// rejection summaries and the funnel steps that produced the pick.
func PrimaryTitleDecisionSummary(titles []ripspec.Title) (ripspec.Title, bool, []string, []string, []funnelStep) {
	selection, ok, steps := choosePrimaryTitleTraced(titles)
	valid, rejected := partitionValidTitles(titles)
	candidateStrs := make([]string, 0, len(valid))
	for _, t := range valid {
		candidateStrs = append(candidateStrs, fmt.Sprintf("%d:%ds ch=%d seg=%d playlist=%s",
			t.ID, t.Duration, t.Chapters, t.SegmentCount, strings.TrimSpace(t.Playlist)))
	}
	rejectStrs := make([]string, 0, len(rejected))
	for _, t := range rejected {
		reason := "invalid_id"
		if t.ID >= 0 {
			reason = "duration<=0"
		}
		rejectStrs = append(rejectStrs, fmt.Sprintf("%d:%s", t.ID, reason))
	}
	sort.Strings(candidateStrs)
	sort.Strings(rejectStrs)
	return selection, ok, candidateStrs, rejectStrs, steps
}

// parse800SeriesNum extracts the 800-series number from an MPLS filename like "00800.mpls".
// Returns the number (e.g. 800) and true if the string matches the 008XX.mpls pattern
// with a value in [800, 900); otherwise returns 0, false.
func parse800SeriesNum(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "008") || !strings.HasSuffix(s, ".mpls") {
		return 0, false
	}
	numStr := strings.TrimPrefix(s, "00")
	numStr = strings.TrimSuffix(numStr, ".mpls")
	num, err := strconv.Atoi(numStr)
	if err != nil || num < 800 || num >= 900 {
		return 0, false
	}
	return num, true
}

// filterPreferredPlaylistFeatureLength returns feature-length titles with the preferred 800-series playlist.
// Disney/Pixar discs use 00800.mpls for English, 00801 for Spanish, 00802 for French.
// The English version is often slightly shorter due to language-specific credits.
// Only applies when multiple feature-length 800-series playlists exist with similar runtimes
// (indicating a multi-language disc). If runtimes differ by more than 30 seconds, assumes
// different cuts (e.g., theatrical vs director's cut) and returns empty to let normal
// selection prefer the longer version.
// Returns an empty slice if not a multi-language disc pattern; the note
// explains the decision either way so the funnel trace can surface it.
func filterPreferredPlaylistFeatureLength(titles []ripspec.Title, minRuntime int) ([]ripspec.Title, string) {
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
		// Try Playlist first (standard BD: "00800.mpls").
		if num, ok := parse800SeriesNum(t.Playlist); ok {
			candidates = append(candidates, scored{title: t, num: num})
			continue
		}
		// Fallback for UHD: Playlist is a numeric index, MPLS name is in SegmentMap.
		// Only use SegmentMap when it holds a single value (not comma-separated).
		sm := strings.TrimSpace(t.SegmentMap)
		if sm != "" && !strings.Contains(sm, ",") {
			if num, ok := parse800SeriesNum(sm); ok {
				candidates = append(candidates, scored{title: t, num: num})
			}
		}
	}

	// Only apply if we have multiple 800-series playlists (indicating multi-language disc).
	if len(candidates) < 2 {
		return nil, ""
	}

	// Check if we have different playlist numbers (e.g., 800, 801, 802).
	playlistNums := make(map[int]bool)
	for _, c := range candidates {
		playlistNums[c.num] = true
	}
	if len(playlistNums) < 2 {
		return nil, ""
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
		return nil, fmt.Sprintf("800-series runtime spread %ds exceeds %ds; treating playlists as different cuts, not language variants",
			maxDuration-minDuration, maxLanguageVariantRuntimeDiff)
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
	return result, fmt.Sprintf("multi-language 800-series disc; preferring playlist 00%d (runtime spread %ds within %ds)",
		minNum, maxDuration-minDuration, maxLanguageVariantRuntimeDiff)
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
