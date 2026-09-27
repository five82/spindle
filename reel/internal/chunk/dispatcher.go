package chunk

import (
	"sync"
)

// Dispatcher tracks chunk state and picks optimal next chunk based on completed chunks.
// It prioritizes chunks adjacent to already-completed chunks for better CRF prediction.
type Dispatcher struct {
	mu        sync.Mutex
	ready     map[int]Chunk // chunks not yet started
	completed map[int]bool  // completed chunk indices
}

// NewDispatcher creates a new dispatcher with the given chunks.
func NewDispatcher(chunks []Chunk) *Dispatcher {
	ready := make(map[int]Chunk, len(chunks))
	for _, ch := range chunks {
		ready[ch.Idx] = ch
	}
	return &Dispatcher{
		ready:     ready,
		completed: make(map[int]bool),
	}
}

// Next returns the next chunk to process.
// It picks the chunk nearest to any completed chunk, or the lowest index if none completed.
// Returns false if no chunks remain.
func (d *Dispatcher) Next() (Chunk, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.ready) == 0 {
		return Chunk{}, false
	}

	// If no completions yet, return lowest index (sequential fallback)
	if len(d.completed) == 0 {
		return d.pickLowest(), true
	}

	// Find chunk with minimum distance to any completed chunk
	var bestChunk Chunk
	bestDist := -1

	for _, ch := range d.ready {
		minDist := d.minDistToCompleted(ch.Idx)
		if bestDist < 0 || minDist < bestDist || (minDist == bestDist && ch.Idx < bestChunk.Idx) {
			bestChunk = ch
			bestDist = minDist
		}
	}

	delete(d.ready, bestChunk.Idx)
	return bestChunk, true
}

// MarkComplete records a chunk as completed.
func (d *Dispatcher) MarkComplete(idx int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completed[idx] = true
}

// Remaining returns the count of unstarted chunks.
func (d *Dispatcher) Remaining() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.ready)
}

// pickLowest returns and removes the chunk with the lowest index.
func (d *Dispatcher) pickLowest() Chunk {
	lowestIdx := -1
	var lowestChunk Chunk

	for idx, ch := range d.ready {
		if lowestIdx < 0 || idx < lowestIdx {
			lowestIdx = idx
			lowestChunk = ch
		}
	}

	delete(d.ready, lowestIdx)
	return lowestChunk
}

// minDistToCompleted returns the minimum distance from idx to any completed chunk.
func (d *Dispatcher) minDistToCompleted(idx int) int {
	minDist := -1
	for c := range d.completed {
		dist := idx - c
		if dist < 0 {
			dist = -dist
		}
		if minDist < 0 || dist < minDist {
			minDist = dist
		}
	}
	return minDist
}
