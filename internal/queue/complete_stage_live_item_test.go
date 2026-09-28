package queue

import (
	"path/filepath"
	"testing"
)

func TestCompleteStageUpdatesLiveItem(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	item, err := store.NewDisc("Example", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteStage(item, StageRipping); err != nil {
		t.Fatal(err)
	}
	if item.Stage != StageRipping {
		t.Fatalf("in-memory stage: %s", item.Stage)
	}
	loaded, err := store.GetByID(item.ID)
	if err != nil || loaded.Stage != StageRipping {
		t.Fatalf("persisted stage: %+v %v", loaded, err)
	}
}
