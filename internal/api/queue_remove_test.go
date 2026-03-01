package api

import (
	"context"
	"errors"
	"testing"
)

type queueRemoveStub struct {
	removed map[int64]bool
	errAt   int64
}

func (s *queueRemoveStub) Remove(_ context.Context, ids []int64) (int64, error) {
	if len(ids) != 1 {
		return 0, errors.New("expected one id")
	}
	id := ids[0]
	if id == s.errAt {
		return 0, errors.New("remove failed")
	}
	if s.removed[id] {
		return 1, nil
	}
	return 0, nil
}

func TestRemoveItemsByID(t *testing.T) {
	stub := &queueRemoveStub{removed: map[int64]bool{1: true, 3: true}}

	result, err := RemoveItemsByID(context.Background(), stub, []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("RemoveItemsByID: %v", err)
	}
	if result.RemovedCount != 2 {
		t.Fatalf("RemovedCount = %d, want 2", result.RemovedCount)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len(Items) = %d, want 3", len(result.Items))
	}
	if result.Items[0].Outcome != RemoveItemRemoved {
		t.Fatalf("item 1 outcome = %s, want %s", result.Items[0].Outcome, RemoveItemRemoved)
	}
	if result.Items[1].Outcome != RemoveItemNotFound {
		t.Fatalf("item 2 outcome = %s, want %s", result.Items[1].Outcome, RemoveItemNotFound)
	}
	if result.Items[2].Outcome != RemoveItemRemoved {
		t.Fatalf("item 3 outcome = %s, want %s", result.Items[2].Outcome, RemoveItemRemoved)
	}
}

func TestRemoveItemsByIDError(t *testing.T) {
	stub := &queueRemoveStub{removed: map[int64]bool{1: true}, errAt: 2}

	_, err := RemoveItemsByID(context.Background(), stub, []int64{1, 2})
	if err == nil {
		t.Fatal("expected error")
	}
}
