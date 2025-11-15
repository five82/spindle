package contentid

import (
	"math"
)

const minSimilarityScore = 0.58

// resolveEpisodeMatches computes the maximum-weight bipartite matching between ripped
// episode transcripts and OpenSubtitles references using the Hungarian algorithm.
// This avoids greedy mis-assignments when multiple pairs have very similar scores.
func resolveEpisodeMatches(rips []ripFingerprint, refs []referenceFingerprint) []matchResult {
	if len(rips) == 0 || len(refs) == 0 {
		return nil
	}

	n := len(rips)
	m := len(refs)
	size := n
	if m > size {
		size = m
	}

	// Build cost matrix for minimization; cost = 1 - similarity (bounded to [0,1]).
	// Padded rows/cols use a high cost so they won't be chosen unless necessary.
	const padCost = 2.0
	cost := make([][]float64, size)
	scores := make([][]float64, size)
	for i := 0; i < size; i++ {
		cost[i] = make([]float64, size)
		scores[i] = make([]float64, size)
		for j := 0; j < size; j++ {
			cost[i][j] = padCost
		}
	}
	for i, rip := range rips {
		for j, ref := range refs {
			score := cosineSimilarity(rip.Vector, ref.Vector)
			scores[i][j] = score
			if score <= 0 {
				continue
			}
			c := 1.0 - score
			if c < 0 {
				c = 0
			}
			cost[i][j] = c
		}
	}

	assign := hungarian(cost)

	results := make([]matchResult, 0, minValue(len(rips), len(refs)))
	for i, j := range assign {
		if i >= n || j < 0 || j >= m {
			continue
		}
		score := scores[i][j]
		if score < minSimilarityScore {
			continue
		}
		results = append(results, matchResult{
			EpisodeKey:        rips[i].EpisodeKey,
			TitleID:           rips[i].TitleID,
			TargetEpisode:     refs[j].EpisodeNumber,
			Score:             score,
			SubtitleFileID:    refs[j].FileID,
			SubtitleLanguage:  refs[j].Language,
			SubtitleCachePath: refs[j].CachePath,
		})
	}
	return results
}

func minValue(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// hungarian solves the assignment problem for a square cost matrix (minimization).
// Returns a slice assignment[i] = column index chosen for row i, or -1 if unassigned.
func hungarian(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}
	m := len(cost[0])
	if m != n {
		return nil
	}

	u := make([]float64, n+1)
	v := make([]float64, n+1)
	p := make([]int, n+1)
	way := make([]int, n+1)

	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := 0; j <= n; j++ {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}

	assign := make([]int, n)
	for i := range assign {
		assign[i] = -1
	}
	for j := 1; j <= n; j++ {
		if p[j] > 0 && p[j]-1 < n {
			assign[p[j]-1] = j - 1
		}
	}
	return assign
}
