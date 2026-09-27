package state

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/five82/flyer/internal/spindle"
)

func TestStore_UpdateAndSnapshotClone(t *testing.T) {
	var s Store

	status := &spindle.StatusResponse{Running: true, PID: 123}
	queue := []spindle.QueueItem{{ID: 1}, {ID: 2}}

	before := time.Now()
	s.Update(status, queue, nil)

	snap := s.Snapshot()
	if !snap.HasStatus || snap.Status.PID != 123 {
		t.Fatalf("snapshot status = %#v, want pid=123 HasStatus=true", snap.Status)
	}
	if len(snap.Queue) != 2 || snap.Queue[0].ID != 1 {
		t.Fatalf("snapshot queue = %#v, want 2 items", snap.Queue)
	}
	if snap.LastUpdated.Before(before) {
		t.Fatalf("LastUpdated = %v, want >= %v", snap.LastUpdated, before)
	}
	if snap.LastError != nil {
		t.Fatalf("LastError = %v, want nil", snap.LastError)
	}

	// Returned snapshot should be independent of the stored one.
	snap.Queue[0].ID = 999
	snap2 := s.Snapshot()
	if snap2.Queue[0].ID != 1 {
		t.Fatalf("Snapshot should clone queue; got id %d want 1", snap2.Queue[0].ID)
	}
}

func TestStore_UpdateErrorKeepsPreviousData(t *testing.T) {
	var s Store

	s.Update(&spindle.StatusResponse{PID: 1}, []spindle.QueueItem{{ID: 1}}, nil)
	prev := s.Snapshot()

	before := time.Now()
	origErr := errors.New("boom")
	s.Update(nil, nil, origErr)

	snap := s.Snapshot()
	if snap.HasStatus != prev.HasStatus || snap.Status.PID != prev.Status.PID {
		t.Fatalf("status changed on error: got %#v want %#v", snap.Status, prev.Status)
	}
	if len(snap.Queue) != 1 || snap.Queue[0].ID != 1 {
		t.Fatalf("queue changed on error: got %#v want %#v", snap.Queue, prev.Queue)
	}
	if snap.LastUpdated.Before(before) {
		t.Fatalf("LastUpdated = %v, want >= %v", snap.LastUpdated, before)
	}
	if snap.LastError == nil || snap.LastError.Error() != "boom" {
		t.Fatalf("LastError = %v, want boom", snap.LastError)
	}
	// Verify error chain is preserved (can use errors.Is)
	if !errors.Is(snap.LastError, origErr) {
		t.Fatalf("Snapshot should preserve error chain; errors.Is failed")
	}
	// But instances should be different
	if snap.LastError == origErr {
		t.Fatalf("Snapshot should wrap error, not return same instance")
	}
}

func TestStore_ConsecutiveFailures(t *testing.T) {
	var s Store

	// Initially zero failures
	snap := s.Snapshot()
	if snap.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0", snap.ConsecutiveFailures)
	}
	if snap.IsOffline() {
		t.Fatal("IsOffline() = true, want false with 0 failures")
	}

	// First failure
	s.Update(nil, nil, errors.New("fail 1"))
	snap = s.Snapshot()
	if snap.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1", snap.ConsecutiveFailures)
	}
	if snap.IsOffline() {
		t.Fatal("IsOffline() = true, want false with 1 failure")
	}

	// Second failure - now offline
	s.Update(nil, nil, errors.New("fail 2"))
	snap = s.Snapshot()
	if snap.ConsecutiveFailures != 2 {
		t.Fatalf("ConsecutiveFailures = %d, want 2", snap.ConsecutiveFailures)
	}
	if !snap.IsOffline() {
		t.Fatal("IsOffline() = false, want true with 2 failures")
	}

	// Third failure - still offline
	s.Update(nil, nil, errors.New("fail 3"))
	snap = s.Snapshot()
	if snap.ConsecutiveFailures != 3 {
		t.Fatalf("ConsecutiveFailures = %d, want 3", snap.ConsecutiveFailures)
	}
	if !snap.IsOffline() {
		t.Fatal("IsOffline() = false, want true with 3 failures")
	}

	// Success resets counter
	s.Update(&spindle.StatusResponse{Running: true}, nil, nil)
	snap = s.Snapshot()
	if snap.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0 after success", snap.ConsecutiveFailures)
	}
	if snap.IsOffline() {
		t.Fatal("IsOffline() = true, want false after success")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	var s Store
	var wg sync.WaitGroup

	// Run concurrent updates and snapshots
	for i := range 10 {
		wg.Add(2)

		// Writer goroutine
		go func(id int) {
			defer wg.Done()
			for j := range 100 {
				if j%2 == 0 {
					s.Update(&spindle.StatusResponse{PID: id},
						[]spindle.QueueItem{{ID: int64(j)}}, nil)
				} else {
					s.Update(nil, nil, errors.New("test error"))
				}
			}
		}(i)

		// Reader goroutine
		go func() {
			defer wg.Done()
			for range 100 {
				snap := s.Snapshot()
				// Access fields to ensure no race on read
				_ = snap.HasStatus
				_ = snap.IsOffline()
				_ = len(snap.Queue)
				if snap.LastError != nil {
					_ = snap.LastError.Error()
				}
			}
		}()
	}

	wg.Wait()
}
