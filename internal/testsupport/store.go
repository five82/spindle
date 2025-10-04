package testsupport

import (
	"context"
	"testing"

	"spindle/internal/config"
	"spindle/internal/queue"
)

// MustOpenStore opens a queue.Store for tests and registers cleanup.
func MustOpenStore(t testing.TB, cfg *config.Config) *queue.Store {
	t.Helper()

	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
	})
	return store
}

// NewDisc creates a new disc item for tests using the provided store.
func NewDisc(t testing.TB, store *queue.Store, title, fingerprint string) *queue.Item {
	t.Helper()

	item, err := store.NewDisc(context.Background(), title, fingerprint)
	if err != nil {
		t.Fatalf("store.NewDisc: %v", err)
	}
	return item
}
