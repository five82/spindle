package contentid

import "sort"

// Match represents a disc-to-episode assignment.
type Match struct {
	DiscKey    string
	EpisodeNum int
	Score      float64
}

// hungarian solves the assignment problem for a similarity matrix.
// Returns assignments[row] = col for each row, or -1 if unassigned.
//
// Uses greedy assignment with conflict resolution, which is adequate
// for the typical disc case (4-8 episodes per disc). Matrix sizes are
// small (typically < 30 episodes per season).
func hungarian(scores [][]float64) []int {
	n := len(scores)
	if n == 0 {
		return nil
	}

	m := len(scores[0])
	assignments := make([]int, n)
	for i := range assignments {
		assignments[i] = -1
	}

	type candidate struct {
		row, col int
		score    float64
	}
	var candidates []candidate
	for i := range n {
		for j := range m {
			candidates = append(candidates, candidate{i, j, scores[i][j]})
		}
	}
	// Sort by score descending (highest similarity first).
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].score > candidates[b].score
	})

	rowUsed := make([]bool, n)
	colUsed := make([]bool, m)
	for _, c := range candidates {
		if rowUsed[c.row] || colUsed[c.col] {
			continue
		}
		if c.score < minSimilarityScore {
			continue
		}
		assignments[c.row] = c.col
		rowUsed[c.row] = true
		colUsed[c.col] = true
	}

	return assignments
}
