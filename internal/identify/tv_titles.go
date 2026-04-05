package identify

import (
	"fmt"
	"slices"
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
	slices.SortFunc(qualifyingDoubleCandidates, func(a, b tvTitleCandidate) int {
		if a.title.ID != b.title.ID {
			return a.title.ID - b.title.ID
		}
		return a.title.Duration - b.title.Duration
	})
	if len(qualifyingDoubleCandidates) > 0 {
		selectedByIndex[qualifyingDoubleCandidates[0].decisionIndex] = "probable_double_episode_candidate"
	}
	result.AmbiguousLongCount = len(qualifyingDoubleCandidates)

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
	if len(candidates) > 0 && result.ExtraCount*2 > len(candidates) {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "extras_dominate_candidates")
	}
	if len(qualifyingDoubleCandidates) >= 2 {
		result.Ambiguous = true
		result.AmbiguityReasons = append(result.AmbiguityReasons, "multiple_double_episode_candidates")
	}

	return result
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
