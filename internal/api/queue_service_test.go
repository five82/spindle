package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"spindle/internal/queue"
)

type mockQueueReader struct {
	items    []*queue.Item
	stats    map[queue.Status]int
	itemErr  error
	statsErr error
}

func (m *mockQueueReader) List(context.Context, ...queue.Status) ([]*queue.Item, error) {
	return m.items, m.itemErr
}

func (m *mockQueueReader) Stats(context.Context) (map[queue.Status]int, error) {
	return m.stats, m.statsErr
}

func (m *mockQueueReader) GetByID(context.Context, int64) (*queue.Item, error) {
	if len(m.items) == 0 {
		return nil, m.itemErr
	}
	return m.items[0], m.itemErr
}

func TestQueueService_List(t *testing.T) {
	now := time.Now().UTC()
	reader := &mockQueueReader{
		items: []*queue.Item{{
			ID:        1,
			DiscTitle: "Example",
			Status:    queue.StatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}
	svc := NewQueueService(reader)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("unexpected item count: %d", len(got))
	}
	if got[0].DiscTitle != "Example" {
		t.Fatalf("unexpected disc title: %q", got[0].DiscTitle)
	}
	if got[0].Status != string(queue.StatusPending) {
		t.Fatalf("unexpected status: %q", got[0].Status)
	}
	if got[0].CreatedAt == "" || got[0].UpdatedAt == "" {
		t.Fatalf("expected timestamps to be formatted")
	}
}

func TestQueueService_ListError(t *testing.T) {
	errSentinel := errors.New("boom")
	svc := NewQueueService(&mockQueueReader{itemErr: errSentinel})
	_, err := svc.List(context.Background())
	if !errors.Is(err, errSentinel) {
		t.Fatalf("expected error %v, got %v", errSentinel, err)
	}
}

func TestQueueService_Stats(t *testing.T) {
	svc := NewQueueService(&mockQueueReader{stats: map[queue.Status]int{
		queue.StatusPending: 2,
		queue.StatusFailed:  1,
	}})
	got, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if got[string(queue.StatusPending)] != 2 {
		t.Fatalf("expected pending count 2, got %d", got[string(queue.StatusPending)])
	}
	if got[string(queue.StatusFailed)] != 1 {
		t.Fatalf("expected failed count 1, got %d", got[string(queue.StatusFailed)])
	}
}

func TestQueueService_Describe(t *testing.T) {
	svc := NewQueueService(&mockQueueReader{items: []*queue.Item{{ID: 7, DiscTitle: "Disc"}}})
	item, err := svc.Describe(context.Background(), 7)
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if item == nil {
		t.Fatal("Describe returned nil item")
		return
	}
	if item.ID != 7 {
		t.Fatalf("unexpected id: %d", item.ID)
	}
}
