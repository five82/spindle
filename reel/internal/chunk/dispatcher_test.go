package chunk

import (
	"sync"
	"testing"
)

func TestDispatcher_NoCompletions(t *testing.T) {
	chunks := []Chunk{
		{Idx: 2, Start: 200, End: 300},
		{Idx: 0, Start: 0, End: 100},
		{Idx: 1, Start: 100, End: 200},
	}

	d := NewDispatcher(chunks)

	// Should return lowest index first when no completions
	ch, ok := d.Next()
	if !ok || ch.Idx != 0 {
		t.Errorf("First Next() = %v, %v, want idx 0, true", ch.Idx, ok)
	}

	ch, ok = d.Next()
	if !ok || ch.Idx != 1 {
		t.Errorf("Second Next() = %v, %v, want idx 1, true", ch.Idx, ok)
	}

	ch, ok = d.Next()
	if !ok || ch.Idx != 2 {
		t.Errorf("Third Next() = %v, %v, want idx 2, true", ch.Idx, ok)
	}

	// No more chunks
	_, ok = d.Next()
	if ok {
		t.Error("Fourth Next() should return false")
	}
}

func TestDispatcher_WithCompletions(t *testing.T) {
	// Chunks 0-9, start with 5 completed
	chunks := []Chunk{}
	for i := range 10 {
		if i != 5 {
			chunks = append(chunks, Chunk{Idx: i, Start: i * 100, End: (i + 1) * 100})
		}
	}

	d := NewDispatcher(chunks)
	d.MarkComplete(5) // Chunk 5 is already done

	// First Next() should pick either 4 or 6 (distance 1 from 5)
	ch, ok := d.Next()
	if !ok {
		t.Fatal("Expected chunk, got none")
	}
	if ch.Idx != 4 && ch.Idx != 6 {
		t.Errorf("Next() = %v, want 4 or 6 (adjacent to completed 5)", ch.Idx)
	}

	// Mark first result as complete and get next
	d.MarkComplete(ch.Idx)

	ch, ok = d.Next()
	if !ok {
		t.Fatal("Expected chunk, got none")
	}
	// Should be adjacent to one of the completed chunks
	if ch.Idx < 3 || ch.Idx > 7 {
		t.Errorf("Next() = %v, expected something near 4-6", ch.Idx)
	}
}

func TestDispatcher_PicksAdjacent(t *testing.T) {
	// Create chunks 0, 1, 2, 3, 4 with only 2 completed
	chunks := []Chunk{
		{Idx: 0},
		{Idx: 1},
		{Idx: 3},
		{Idx: 4},
	}

	d := NewDispatcher(chunks)
	d.MarkComplete(2) // Chunk 2 is done

	// Should pick 1 or 3 (distance 1 from 2), prefer lower index
	ch, ok := d.Next()
	if !ok || ch.Idx != 1 {
		t.Errorf("Next() = %v, %v, want idx 1 (adjacent to 2, lower index preferred)", ch.Idx, ok)
	}
}

func TestDispatcher_MultipleCompleted(t *testing.T) {
	// Chunks 0, 3, 6, 9 with 1 and 5 completed
	chunks := []Chunk{
		{Idx: 0},
		{Idx: 3},
		{Idx: 6},
		{Idx: 9},
	}

	d := NewDispatcher(chunks)
	d.MarkComplete(1)
	d.MarkComplete(5)

	// Chunk 0 is distance 1 from 1
	// Chunk 3 is distance 2 from both 1 and 5
	// Chunk 6 is distance 1 from 5
	// Chunk 9 is distance 4 from 5
	// Should pick 0 or 6 (both distance 1), prefer lower index
	ch, ok := d.Next()
	if !ok || ch.Idx != 0 {
		t.Errorf("Next() = %v, %v, want idx 0 (distance 1 from 1)", ch.Idx, ok)
	}
}

func TestDispatcher_Remaining(t *testing.T) {
	chunks := []Chunk{{Idx: 0}, {Idx: 1}, {Idx: 2}}
	d := NewDispatcher(chunks)

	if r := d.Remaining(); r != 3 {
		t.Errorf("Remaining() = %d, want 3", r)
	}

	d.Next()
	if r := d.Remaining(); r != 2 {
		t.Errorf("Remaining() after Next() = %d, want 2", r)
	}
}

func TestDispatcher_Concurrent(t *testing.T) {
	chunks := make([]Chunk, 100)
	for i := range chunks {
		chunks[i] = Chunk{Idx: i}
	}

	d := NewDispatcher(chunks)

	var wg sync.WaitGroup
	seen := make(chan int, 100)

	// 10 concurrent workers
	for range 10 {
		wg.Go(func() {
			for {
				ch, ok := d.Next()
				if !ok {
					return
				}
				seen <- ch.Idx
				d.MarkComplete(ch.Idx)
			}
		})
	}

	wg.Wait()
	close(seen)

	// Verify all chunks were processed exactly once
	got := make(map[int]bool)
	for idx := range seen {
		if got[idx] {
			t.Errorf("Chunk %d processed more than once", idx)
		}
		got[idx] = true
	}

	if len(got) != 100 {
		t.Errorf("Processed %d chunks, want 100", len(got))
	}
}

func TestDispatcher_Empty(t *testing.T) {
	d := NewDispatcher(nil)

	_, ok := d.Next()
	if ok {
		t.Error("Next() on empty dispatcher should return false")
	}

	if r := d.Remaining(); r != 0 {
		t.Errorf("Remaining() = %d, want 0", r)
	}
}
