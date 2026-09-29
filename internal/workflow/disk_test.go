package workflow

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
)

func TestDiskWaitPersistsAndNotifiesOnceUntilRecovery(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureTasks(item, []queue.TaskSpec{{Type: queue.StageIdentification}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		if r.Header.Get("Tags") != "warning" || r.Header.Get("Title") == "" {
			t.Errorf("missing warning notification headers: %v", r.Header)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	m := New(store, notify.New(server.URL, 1), nil, nil)
	m.SetDiskSpacePath(filepath.Join(t.TempDir(), "unavailable"), nil)
	m.diskFree = func(string) (int64, error) { return 0, errors.New("statfs failed") }
	for i := 0; i < 2; i++ {
		if !m.waitForDisk(context.Background(), tasks[0], item) {
			t.Fatal("expected pending disk wait")
		}
	}
	if sent.Load() != 1 {
		t.Fatalf("sent %d notifications, want one", sent.Load())
	}
	saved, err := store.TasksForItem(item.ID)
	if err != nil || len(saved[0].Activities) != 1 || saved[0].Activities[0].Operation != "disk_space" || !strings.Contains(saved[0].Activities[0].Message, "cannot check") {
		t.Fatalf("missing persisted Flyer warning: %+v, %v", saved, err)
	}
	// A daemon restart re-reads the persisted activity and does not re-notify.
	m = New(store, notify.New(server.URL, 1), nil, nil)
	m.SetDiskSpacePath(filepath.Join(t.TempDir(), "unavailable"), nil)
	m.diskFree = func(string) (int64, error) { return 0, errors.New("statfs failed") }
	if !m.waitForDisk(context.Background(), saved[0], item) || sent.Load() != 1 {
		t.Fatal("repeat notification after restart")
	}
	m.diskFree = func(string) (int64, error) { return 200 * gib, nil }
	if m.waitForDisk(context.Background(), saved[0], item) {
		t.Fatal("task did not recover")
	}
	saved, err = store.TasksForItem(item.ID)
	if err != nil || len(saved[0].Activities) != 0 {
		t.Fatalf("warning remained after recovery: %+v, %v", saved, err)
	}
	events, _, err := store.Events(item.ID, 0, 10)
	if err != nil || len(events) != 2 || events[0].Type != "disk_space_wait" || events[1].Type != "disk_space_available" {
		t.Fatalf("disk wait lifecycle = %+v, %v", events, err)
	}
}

func TestDiskRequirementUsesSelectedTitlesNotWholeDisc(t *testing.T) {
	m := New(nil, nil, nil, nil)
	item := &queue.Item{}
	env := ripspec.Envelope{
		Version:  ripspec.CurrentVersion,
		Metadata: ripspec.Metadata{MediaType: "tv"},
		Titles:   []ripspec.Title{{ID: 1, SizeBytes: 80 << 30}, {ID: 2, SizeBytes: 200 << 30}},
		Episodes: []ripspec.Episode{{TitleID: 1}},
	}
	var err error
	item.RipSpecData, err = env.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.diskRequirement(&queue.Task{Type: queue.StageRipping}, item), int64(188)<<30; got != want {
		t.Fatalf("rip requirement = %d, want %d", got, want)
	}
	if got := m.diskRequirement(&queue.Task{Type: queue.StageIdentification}, item); got != 150*gib {
		t.Fatalf("start floor = %d", got)
	}
	if got := m.diskRequirement(&queue.Task{Type: queue.StageEncoding}, item); got != 100*gib {
		t.Fatalf("encode floor = %d", got)
	}
}
