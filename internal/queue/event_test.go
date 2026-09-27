package queue

import (
	"path/filepath"
	"testing"
)

func TestItemEventsSurviveRestartAndFollowItemLifetime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.NewDisc("disc", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.NewDisc("other", "other")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Event{
		{ItemID: item.ID, Type: "stage_start", Stage: StageIdentification},
		{ItemID: other.ID, Type: "stage_start", Stage: StageIdentification},
		{ItemID: item.ID, Type: "stage_complete", Stage: StageIdentification, DurationSeconds: 1.25},
	} {
		if err := store.RecordEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	first, cursor, err := store.Events(item.ID, 0, 1)
	if err != nil || len(first) != 1 || first[0].Type != "stage_start" || first[0].Time == "" {
		t.Fatalf("first page: %+v cursor=%d err=%v", first, cursor, err)
	}
	last, next, err := store.Events(item.ID, cursor, 10)
	if err != nil || len(last) != 1 || last[0].Type != "stage_complete" || last[0].DurationSeconds != 1.25 || next != last[0].ID {
		t.Fatalf("second page: %+v next=%d err=%v", last, next, err)
	}
	if err := store.Remove(item.ID); err != nil {
		t.Fatal(err)
	}
	gone, _, err := store.Events(item.ID, 0, 10)
	if err != nil || len(gone) != 0 {
		t.Fatalf("events survived deletion: %+v, %v", gone, err)
	}
	if err := store.RecordEvent(Event{ItemID: item.ID, Type: "stage_start"}); err == nil {
		t.Fatal("orphaned event accepted")
	}
	if _, err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	gone, _, err = store.Events(other.ID, 0, 10)
	if err != nil || len(gone) != 0 {
		t.Fatalf("events survived clear: %+v, %v", gone, err)
	}
}
