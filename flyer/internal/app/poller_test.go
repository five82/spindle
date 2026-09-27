package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
	"github.com/five82/spindle/flyer/internal/state"
)

func TestStartPollerRefreshesImmediately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(spindle.StatusResponse{PID: 77})
		case "/api/queue":
			_ = json.NewEncoder(w).Encode(spindle.QueueListResponse{Items: []spindle.QueueItem{{ID: 88}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var store state.Store
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A long interval proves the first refresh is immediate, not ticker-driven.
	StartPoller(ctx, &store, newTestClient(t, server.URL), time.Hour)
	deadline := time.After(2 * time.Second)
	for {
		snap := store.Snapshot()
		if snap.HasStatus && snap.Status.PID == 77 && len(snap.Queue) == 1 && snap.Queue[0].ID == 88 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("poller did not populate store: %+v", snap)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestCombineFetchErrors(t *testing.T) {
	statusErr := errors.New("status unavailable")
	queueErr := errors.New("queue unavailable")
	if err := combineFetchErrors(statusErr, nil); !errors.Is(err, statusErr) {
		t.Fatalf("status-only error = %v", err)
	}
	if err := combineFetchErrors(nil, queueErr); !errors.Is(err, queueErr) {
		t.Fatalf("queue-only error = %v", err)
	}
	if err := combineFetchErrors(statusErr, queueErr); !errors.Is(err, statusErr) || !strings.Contains(err.Error(), queueErr.Error()) {
		t.Fatalf("combined error = %v", err)
	}
}

func TestCalculateBackoff(t *testing.T) {
	baseInterval := 2 * time.Second

	tests := []struct {
		name     string
		failures int
		want     time.Duration
	}{
		{"zero failures", 0, 2 * time.Second},
		{"negative failures", -1, 2 * time.Second},
		{"one failure", 1, 4 * time.Second},
		{"two failures", 2, 8 * time.Second},
		{"three failures", 3, 16 * time.Second},
		{"four failures capped", 4, 30 * time.Second}, // Would be 32s, capped to 30s
		{"many failures capped", 10, 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateBackoff(tt.failures, baseInterval)
			if got != tt.want {
				t.Errorf("calculateBackoff(%d, %v) = %v, want %v", tt.failures, baseInterval, got, tt.want)
			}
		})
	}
}

func TestCalculateBackoff_MaxCap(t *testing.T) {
	// Verify that backoff never exceeds maxBackoff regardless of input
	baseInterval := 2 * time.Second
	for failures := 0; failures <= 20; failures++ {
		got := calculateBackoff(failures, baseInterval)
		if got > maxBackoff {
			t.Errorf("calculateBackoff(%d, %v) = %v, exceeds maxBackoff %v", failures, baseInterval, got, maxBackoff)
		}
	}
}

// newTestClient builds a spindle.Client pointed at the given test server.
func newTestClient(t *testing.T, url string) *spindle.Client {
	t.Helper()
	c, err := spindle.NewClient(url)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	return c
}

func TestRefresh_BothSucceedUpdatesStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(spindle.StatusResponse{Running: true, PID: 123})
		case "/api/queue":
			_ = json.NewEncoder(w).Encode(spindle.QueueListResponse{Items: []spindle.QueueItem{{ID: 42}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var store state.Store
	client := newTestClient(t, server.URL)

	if err := refresh(context.Background(), &store, client); err != nil {
		t.Fatalf("refresh() error = %v, want nil", err)
	}

	snap := store.Snapshot()
	if !snap.HasStatus || snap.Status.PID != 123 {
		t.Fatalf("snapshot status = %#v, want pid=123", snap.Status)
	}
	if len(snap.Queue) != 1 || snap.Queue[0].ID != 42 {
		t.Fatalf("snapshot queue = %#v, want 1 item id=42", snap.Queue)
	}
}

// TestRefresh_OneFailureLeavesStoreUntouched verifies that when only one of
// the two concurrent fetches fails, the store keeps its previous data (no
// partial update) and the failure is recorded.
func TestRefresh_OneFailureLeavesStoreUntouched(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_ = json.NewEncoder(w).Encode(spindle.StatusResponse{Running: true, PID: 123})
		case "/api/queue":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var store state.Store
	// Seed the store with prior data so we can verify it is untouched.
	store.Update(&spindle.StatusResponse{PID: 1}, []spindle.QueueItem{{ID: 1}}, nil)

	client := newTestClient(t, server.URL)
	err := refresh(context.Background(), &store, client)
	if err == nil {
		t.Fatalf("refresh() error = nil, want error from queue fetch")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("refresh() error = %v, want it to mention the queue fetch failure", err)
	}

	snap := store.Snapshot()
	if snap.Status.PID != 1 || len(snap.Queue) != 1 || snap.Queue[0].ID != 1 {
		t.Fatalf("store should keep previous data on partial failure, got status=%#v queue=%#v", snap.Status, snap.Queue)
	}
	if snap.LastError == nil {
		t.Fatalf("snapshot LastError = nil, want recorded failure")
	}
}

// TestRefresh_BothFailCombinesErrorMessages verifies that when both fetches
// fail, the combined error mentions both failures rather than dropping one.
func TestRefresh_BothFailCombinesErrorMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	var store state.Store
	client := newTestClient(t, server.URL)

	err := refresh(context.Background(), &store, client)
	if err == nil {
		t.Fatalf("refresh() error = nil, want combined error")
	}
	if !strings.Contains(err.Error(), "status") || !strings.Contains(err.Error(), "queue") {
		t.Fatalf("refresh() error = %v, want it to mention both status and queue failures", err)
	}
}
