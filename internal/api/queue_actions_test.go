package api

import (
	"context"
	"errors"
	"testing"
)

type queueActionStub struct {
	items map[int64]*QueueItem
}

func (s *queueActionStub) Describe(_ context.Context, id int64) (*QueueItem, error) {
	if item, ok := s.items[id]; ok {
		return item, nil
	}
	return nil, nil
}

func (s *queueActionStub) Retry(_ context.Context, ids []int64) (int64, error) {
	if len(ids) != 1 {
		return 0, errors.New("expected one id")
	}
	return 1, nil
}

func (s *queueActionStub) Stop(_ context.Context, ids []int64) (int64, error) {
	if len(ids) != 1 {
		return 0, errors.New("expected one id")
	}
	return 1, nil
}

func TestStopItemsByIDSetsWasProcessing(t *testing.T) {
	stub := &queueActionStub{
		items: map[int64]*QueueItem{
			1: {ID: 1, Status: "RIPPING"},
			2: {ID: 2, Status: "PENDING"},
		},
	}

	result, err := StopItemsByID(context.Background(), stub, []int64{1, 2})
	if err != nil {
		t.Fatalf("StopItemsByID: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(result.Items))
	}

	if result.Items[0].Outcome != StopItemUpdated {
		t.Fatalf("item 1 outcome = %s, want %s", result.Items[0].Outcome, StopItemUpdated)
	}
	if !result.Items[0].WasProcessing {
		t.Fatalf("item 1 WasProcessing = false, want true")
	}

	if result.Items[1].Outcome != StopItemUpdated {
		t.Fatalf("item 2 outcome = %s, want %s", result.Items[1].Outcome, StopItemUpdated)
	}
	if result.Items[1].WasProcessing {
		t.Fatalf("item 2 WasProcessing = true, want false")
	}
}
