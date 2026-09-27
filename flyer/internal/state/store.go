package state

import (
	"fmt"
	"sync"
	"time"

	"github.com/five82/flyer/internal/spindle"
)

// Snapshot represents the latest data available to the UI.
type Snapshot struct {
	Status              spindle.StatusResponse
	HasStatus           bool
	Queue               []spindle.QueueItem
	LastUpdated         time.Time
	LastError           error
	ConsecutiveFailures int // Number of consecutive poll failures
}

// IsOffline returns true when the API has been unreachable for multiple polls.
func (s Snapshot) IsOffline() bool {
	return s.ConsecutiveFailures >= 2
}

// Store coordinates concurrent updates to the snapshot.
type Store struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

// Update replaces the stored snapshot. When err is non-nil the previous data is
// kept but the error is recorded for visibility.
func (s *Store) Update(status *spindle.StatusResponse, queue []spindle.QueueItem, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.snapshot.LastError = err
		s.snapshot.LastUpdated = time.Now()
		s.snapshot.ConsecutiveFailures++
		return
	}

	s.snapshot.Queue = cloneQueue(queue)
	if status != nil {
		s.snapshot.Status = *status
		s.snapshot.HasStatus = true
	} else {
		s.snapshot.HasStatus = false
	}
	s.snapshot.LastError = nil
	s.snapshot.LastUpdated = time.Now()
	s.snapshot.ConsecutiveFailures = 0
}

// Snapshot returns a copy of the current snapshot.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := s.snapshot
	snap.Queue = cloneQueue(s.snapshot.Queue)
	if s.snapshot.LastError != nil {
		snap.LastError = fmt.Errorf("%w", s.snapshot.LastError)
	}
	return snap
}

func cloneQueue(items []spindle.QueueItem) []spindle.QueueItem {
	if len(items) == 0 {
		return nil
	}
	dup := make([]spindle.QueueItem, len(items))
	copy(dup, items)
	return dup
}
