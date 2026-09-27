package httpapi

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/five82/spindle/internal/queue"
)

func TestTasksForDegradesWhenStoreUnavailable(t *testing.T) {
	store, err := queue.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if tasks := srv.tasksFor(1); tasks != nil {
		t.Fatalf("closed store returned tasks: %+v", tasks)
	}
}
