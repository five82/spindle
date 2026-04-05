package identify

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/ripspec"
)

const (
	tvClusterMinGapSec        = 8 * 60
	tvClusterRelativeGapRatio = 0.20
	tvDoubleMinRatio          = 1.80
	tvDoubleMaxRatio          = 2.40
)

type tvTitleDecision struct {
	Title        ripspec.Title
	Selected     bool
	Reason       string
	DuplicateOf  int
	ClusterID    int
	CandidateSec int
}

type tvTitleSelectionResult struct {
	Decisions          []tvTitleDecision
	SelectedTitles     []ripspec.Title
	DuplicateCount     int
	AmbiguousLongCount int
	ExtraCount         int
	Ambiguous          bool
	AmbiguityReasons   []string
}

type tvTitleCandidate struct {
	decisionIndex int
	title         ripspec.Title
	clusterID     int
}

type tvTitleCluster struct {
	id         int
	candidates []tvTitleCandidate
	median     int
	total      int
	minTitleID int
}

func selectTVEpisodeTitles(titles []ripspec.Title, minTitleLength int) tvTitleSelectionResult {
	result := tvTitleSelectionResult{Decisions: make([]tvTitleDecision, 0, len(titles))}
	candidates := make([]tvTitleCandidate, 0, len(titles))
	seen := make(map[string]int)

	for _, title := range titles {
		decision := tvTitleDecision{Title: title, CandidateSec: title.Duration}
		switch {
		case title.Duration < minTitleLength:
			decision.Reason = "below_min_title_length"
		case dedupKey(title) != "":
			key := dedupKey(title)
			if firstID, dup := seen[key]; dup {
				decision.Reason = "duplicate_title"
				decision.DuplicateOf = firstID
				result.DuplicateCount++
			} else {
				seen[key] = title.ID
				decision.Reason = "candidate"
				candidates = append(candidates, tvTitleCandidate{decisionIndex: len(result.Decisions), title: title})
			}
		default:
			decision.Reason = "candidate"
			candidates = append(candidates, tvTitleCandidate{decisionIndex: len(result.Decisions), title: title})
		}
		result.Decisions = append(result.Decisions, decision)
	}

	if len(candidates) == 0 {
		return result
	}

	sorted := append([]tvTitleCandidate(nil), candidates...)
	slices.SortFunc(sorted, func(a, b tvTitleCandidate) int {
		if a.title.Duration != b.title.Duration {
			return a.title.Duration - b.title.Duration
		}
		return a.title.ID - b.title.ID
	})

	clusters := buildTVTitleClusters(sorted)
	primary := choosePrimaryTVTitleCluster(clusters)
	selectedByIndex := make(map[int]string, len(primary.candidates))
	for _, candidate := range primary.candidates {
		selectedByIndex[candidate.decisionIndex] = "primary_runtime_cluster"
	}

	qualifyingDoubleCandidates := make([]tvTitleCandidate, 0)
	if len(primary.candidates) >= 2 && primary.median > 0 {
		minDur := int(float64(primary.median) * tvDoubleMinRatio)
		maxDur := int(float64(primary.median) * tvDoubleMaxRatio)
		for _, cluster := range clusters {
			if cluster.id == primary.id {
				continue
			}
			for _, candidate := range cluster.candidates {
				dur := candidate.title.Duration
				if dur >= minDur && dur <= maxDur {
					qualifyingDoubleCandidates = append(qualifyingDoubleCandidates, candidate)
				}
			}
		}
	}
	// Sort double candidates by rip-safety preference: fewer playlist
	// segments first (single-segment precomposed playlists avoid
	// seamless-branch key failures that silently corrupt composite
	// rips), then by playlist number (lower mpls is typically the
	// primary authoring), then by title ID and duration.
	slices.SortFunc(qualifyingDoubleCandidates, compareDoubleCandidatesByRipSafety)
	result.AmbiguousLongCount = len(qualifyingDoubleCandidates)
	combinedFamilyResolved := false
	// chooseCombinedDoubleEpisodeTitle proves "these halves and some
	// long candidate are the same pilot" via segment-union matching.
	// Its match is used only as a family-detection signal — the actual
	// rip target is the safest entry in the sorted double list, which
	// may be a different encoding of the same pilot (e.g. a
	// single-segment precomposed playlist sitting alongside a
	// multi-segment composite).
	if _, components, ok := chooseCombinedDoubleEpisodeTitle(primary.candidates, qualifyingDoubleCandidates); ok {
		ripTarget := qualifyingDoubleCandidates[0]
		selectedByIndex[ripTarget.decisionIndex] = "combined_double_episode_candidate"
		for _, component := range components {
			delete(selectedByIndex, component.decisionIndex)
		}
		combinedFamilyResolved = true
	} else if len(qualifyingDoubleCandidates) > 0 {
		selectedByIndex[qualifyingDoubleCandidates[0].decisionIndex] = "probable_double_episode_candidate"
	}

	for _, cluster := range clusters {
		for _, candidate := range cluster.candidates {
			decision := &result.Decisions[candidate.decisionIndex]
			decision.ClusterID = cluster.id
			if reason, ok := selectedByIndex[candidate.decisionIndex]; ok {
				decision.Selected = true
				decision.Reason = reason
				continue
			}
			decision.Reason = "runtime_cluster_extra"
			result.ExtraCount++
		}
	}

	for i := range result.Decisions {
		if result.Decisions[i].Selected {
			result.SelectedTitles = append(result.SelectedTitles, result.Decisions[i].Title)
		}
	}
	slices.SortFunc(result.SelectedTitles, func(a, b ripspec.Title) int {
		return a.ID - b.ID
	})

	if len(result.SelectedTitles) == 0 {
		fallback := longestCandidate(candidates)
		decision := &result.Decisions[fallback.decisionIndex]
		decision.Selected = true
		decision.Reason = "fallback_longest_candidate"
		result.SelectedTitles = append(result.SelectedTitles, decision.Title)
		result.ExtraCount = max(0, result.ExtraCount-1)
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "no_cluster_selection")
	}

	if len(primary.candidates) == 1 {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "primary_cluster_single_title")
	}
	if hasNearEqualPrimaryCluster(clusters, primary) {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "competing_runtime_clusters")
	}
	if len(candidates) > 0 && result.ExtraCount*2 > len(candidates) && !combinedFamilyResolved {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "extras_dominate_candidates")
	}
	// Only flag ambiguity when we could not resolve the combined family
	// via segment-union matching. When the combined match succeeded,
	// additional qualifying doubles are alternate encodings of the
	// same episode (e.g. a multi-segment composite alongside a
	// single-segment precomposed playlist) that were deliberately
	// deselected — not a real ambiguity.
	if len(qualifyingDoubleCandidates) >= 2 && !combinedFamilyResolved {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "multiple_double_episode_candidates")
	}

	return result
}

// compareDoubleCandidatesByRipSafety orders qualifying double-episode
// candidates so that the safest-to-rip variant comes first:
//  1. Fewer playlist segments (prefer single-segment precomposed
//     playlists over seamless-branched composites).
//  2. Lower playlist number (typically the primary authoring).
//  3. Lower title ID.
//  4. Shorter duration (stable final tiebreak).
func compareDoubleCandidatesByRipSafety(a, b tvTitleCandidate) int {
	aSegs := segmentCount(a.title)
	bSegs := segmentCount(b.title)
	if aSegs != bSegs {
		return aSegs - bSegs
	}
	if cmp := strings.Compare(a.title.Playlist, b.title.Playlist); cmp != 0 {
		return cmp
	}
	if a.title.ID != b.title.ID {
		return a.title.ID - b.title.ID
	}
	return a.title.Duration - b.title.Duration
}

// segmentCount returns the best available segment count for a title,
// falling back to parsing SegmentMap when SegmentCount is unset.
func segmentCount(title ripspec.Title) int {
	if title.SegmentCount > 0 {
		return title.SegmentCount
	}
	segs, ok := parseSegmentSet(title.SegmentMap)
	if !ok {
		return 0
	}
	return len(segs)
}

func buildTVTitleClusters(sorted []tvTitleCandidate) []tvTitleCluster {
	if len(sorted) == 0 {
		return nil
	}
	clusters := []tvTitleCluster{{id: 1, minTitleID: sorted[0].title.ID}}
	clusters[0].candidates = append(clusters[0].candidates, sorted[0])
	for i := 1; i < len(sorted); i++ {
		prev := sorted[i-1].title.Duration
		cur := sorted[i].title.Duration
		threshold := max(tvClusterMinGapSec, int(float64(prev)*tvClusterRelativeGapRatio))
		clusterIdx := len(clusters) - 1
		if cur-prev > threshold {
			clusters = append(clusters, tvTitleCluster{id: len(clusters) + 1, minTitleID: sorted[i].title.ID})
			clusterIdx++
		}
		clusters[clusterIdx].candidates = append(clusters[clusterIdx].candidates, sorted[i])
		if sorted[i].title.ID < clusters[clusterIdx].minTitleID {
			clusters[clusterIdx].minTitleID = sorted[i].title.ID
		}
	}
	for i := range clusters {
		durations := make([]int, 0, len(clusters[i].candidates))
		for j := range clusters[i].candidates {
			clusters[i].candidates[j].clusterID = clusters[i].id
			durations = append(durations, clusters[i].candidates[j].title.Duration)
			clusters[i].total += clusters[i].candidates[j].title.Duration
		}
		slices.Sort(durations)
		clusters[i].median = durations[len(durations)/2]
	}
	return clusters
}

func choosePrimaryTVTitleCluster(clusters []tvTitleCluster) tvTitleCluster {
	if paired, ok := choosePrimaryTVTitleClusterForDoublePattern(clusters); ok {
		return paired
	}

	maxTotal := 0
	for _, cluster := range clusters {
		if cluster.total > maxTotal {
			maxTotal = cluster.total
		}
	}

	eligible := make([]tvTitleCluster, 0, len(clusters))
	for _, cluster := range clusters {
		if cluster.total*2 >= maxTotal {
			eligible = append(eligible, cluster)
		}
	}
	if len(eligible) == 0 {
		eligible = clusters
	}

	best := eligible[0]
	for _, cluster := range eligible[1:] {
		if betterPrimaryTVCluster(cluster, best) {
			best = cluster
		}
	}
	return best
}

func choosePrimaryTVTitleClusterForDoublePattern(clusters []tvTitleCluster) (tvTitleCluster, bool) {
	var best tvTitleCluster
	found := false
	for _, shorter := range clusters {
		if len(shorter.candidates) < 2 || shorter.median <= 0 {
			continue
		}
		minDur := int(float64(shorter.median) * tvDoubleMinRatio)
		maxDur := int(float64(shorter.median) * tvDoubleMaxRatio)
		for _, longer := range clusters {
			if longer.id == shorter.id {
				continue
			}
			if longer.median < minDur || longer.median > maxDur {
				continue
			}
			if !found || betterDoublePatternPrimary(shorter, best) {
				best = shorter
				found = true
			}
		}
	}
	return best, found
}

func betterDoublePatternPrimary(candidate, best tvTitleCluster) bool {
	if len(candidate.candidates) != len(best.candidates) {
		return len(candidate.candidates) > len(best.candidates)
	}
	if candidate.median != best.median {
		return candidate.median < best.median
	}
	if candidate.total != best.total {
		return candidate.total > best.total
	}
	return candidate.minTitleID < best.minTitleID
}

func betterPrimaryTVCluster(candidate, best tvTitleCluster) bool {
	if len(candidate.candidates) != len(best.candidates) {
		return len(candidate.candidates) > len(best.candidates)
	}
	if candidate.median != best.median {
		return candidate.median > best.median
	}
	if candidate.total != best.total {
		return candidate.total > best.total
	}
	return candidate.minTitleID < best.minTitleID
}

func hasNearEqualPrimaryCluster(clusters []tvTitleCluster, primary tvTitleCluster) bool {
	for _, cluster := range clusters {
		if cluster.id == primary.id || len(cluster.candidates) != len(primary.candidates) {
			continue
		}
		threshold := max(tvClusterMinGapSec, int(float64(min(cluster.median, primary.median))*tvClusterRelativeGapRatio))
		if abs(cluster.median-primary.median) <= threshold {
			return true
		}
	}
	return false
}

func longestCandidate(candidates []tvTitleCandidate) tvTitleCandidate {
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.title.Duration > best.title.Duration ||
			(candidate.title.Duration == best.title.Duration && candidate.title.ID < best.title.ID) {
			best = candidate
		}
	}
	return best
}

func dedupKey(title ripspec.Title) string {
	key := strings.TrimSpace(title.SegmentMap)
	if key != "" {
		return key
	}
	return strings.TrimSpace(title.TitleHash)
}

func chooseCombinedDoubleEpisodeTitle(primary []tvTitleCandidate, doubles []tvTitleCandidate) (tvTitleCandidate, []tvTitleCandidate, bool) {
	for _, combined := range doubles {
		combinedSegs, ok := parseSegmentSet(combined.title.SegmentMap)
		if !ok {
			continue
		}
		for i := 0; i < len(primary); i++ {
			for j := i + 1; j < len(primary); j++ {
				a := primary[i]
				b := primary[j]
				segA, okA := parseSegmentSet(a.title.SegmentMap)
				segB, okB := parseSegmentSet(b.title.SegmentMap)
				if !okA || !okB {
					continue
				}
				if maps.Equal(segA, combinedSegs) || maps.Equal(segB, combinedSegs) {
					continue
				}
				union := maps.Clone(segA)
				maps.Copy(union, segB)
				if !maps.Equal(union, combinedSegs) {
					continue
				}
				if !durationsLookCombined(a.title.Duration, b.title.Duration, combined.title.Duration) {
					continue
				}
				return combined, []tvTitleCandidate{a, b}, true
			}
		}
	}
	return tvTitleCandidate{}, nil, false
}

func parseSegmentSet(segmentMap string) (map[int]struct{}, bool) {
	segmentMap = strings.TrimSpace(segmentMap)
	if segmentMap == "" {
		return nil, false
	}
	parts := strings.Split(segmentMap, ",")
	result := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		result[value] = struct{}{}
	}
	return result, len(result) > 0
}

func durationsLookCombined(a, b, combined int) bool {
	if a <= 0 || b <= 0 || combined <= 0 {
		return false
	}
	delta := abs((a + b) - combined)
	threshold := max(90, combined/20)
	return delta <= threshold
}

func summarizeAmbiguity(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	return strings.Join(reasons, ", ")
}

func describeTVSelection(result tvTitleSelectionResult) string {
	return fmt.Sprintf("%d selected from %d candidates", len(result.SelectedTitles), len(result.Decisions)-countBelowMin(result.Decisions))
}

func countBelowMin(decisions []tvTitleDecision) int {
	count := 0
	for _, decision := range decisions {
		if decision.Reason == "below_min_title_length" {
			count++
		}
	}
	return count
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
