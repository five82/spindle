package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestResourceWaitSnapshotAndJournalShareTaskScope(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureTasks(item, []queue.TaskSpec{{Type: queue.StageEncoding}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0]
	m := New(store, nil, nil, nil)
	claims := map[string]int{"encoder": 1}
	m.ConfigureStages([]PipelineStage{{Stage: queue.StageEncoding, Claims: claims}})
	holder := httpapi.ResourceHolder{ItemID: 99, Task: "encoding"}
	if !m.reserve(claims, holder) {
		t.Fatal("initial reservation failed")
	}
	m.noteTaskBlocked(task, claims)
	m.noteTaskBlocked(task, claims)
	saved, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved[0].Activities) != 1 || saved[0].Activities[0].State != "waiting" || !strings.Contains(saved[0].Activities[0].Message, "encoder held by #99/encoding") {
		t.Fatalf("missing authoritative holder: %+v", saved[0])
	}
	m.blocked[task.ID] = time.Now().Add(-30 * time.Second)
	m.release(claims, holder)
	m.noteTaskGranted(task, claims)
	if err = store.StartTask(task); err != nil {
		t.Fatal(err)
	}
	if len(task.Activities) != 0 {
		t.Fatal("grant retained resource wait")
	}
	events, _, err := store.Events(item.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "resource_wait" || events[1].Type != "resource_granted" || events[1].DurationSeconds < 30 {
		t.Fatalf("wait spam or duration loss: %+v", events)
	}
	for _, e := range events {
		if e.TaskID != task.ID || e.Attempt != task.Attempts {
			t.Fatalf("wait/run mismatch: %+v", e)
		}
	}
}
