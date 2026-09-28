package stage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/five82/spindle/internal/queue"
)

func TestActivityClocksLanesAndTransitions(t *testing.T) {
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
	if err = store.StartTask(task); err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(context.Background(), store, item, task)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	sess.now = func() time.Time { return now }
	video := queue.Activity{ID: "video", Operation: "encode", AssetKey: "title03", Message: "Accepted frames", Total: 100, Unit: "frames"}
	sess.Activity(video)
	start := task.Activities[0].StartedAt
	now = now.Add(20 * time.Second)
	video.Completed = 10
	sess.Activity(video)
	advance := task.Activities[0].AdvancedAt
	now = now.Add(10 * time.Second)
	sess.Activity(video)
	if a := task.Activities[0]; a.StartedAt != start || a.AdvancedAt != advance || a.UpdatedAt == advance {
		t.Fatalf("clocks conflated: %+v", a)
	}
	video.Completed = 5
	sess.Activity(video)
	if a := task.Activities[0]; a.Completed != 10 || a.AdvancedAt != advance {
		t.Fatalf("late observation regressed units: %+v", a)
	}
	video.Completed = 10
	sess.Activity(queue.Activity{ID: "audio", Operation: "audio", AssetKey: "title03", Message: "Audio transcoding"})
	if len(task.Activities) != 2 {
		t.Fatal(task.Activities)
	}
	now = now.Add(40 * time.Second)
	sess.Activity(queue.Activity{ID: "video", Operation: "encode", AssetKey: "title03", State: "ended", Message: "Video ended"})
	if a := task.Activities[0]; a.Completed != 10 || a.Total != 100 || a.StartedAt != start {
		t.Fatalf("terminal observation lost measured work: %+v", a)
	}
	now = now.Add(time.Second)
	sess.Activity(video)
	if task.Activities[0].StartedAt == start {
		t.Fatal("restarted operation reused prior clock")
	}
	events, _, err := store.Events(item.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("counter heartbeats became history events: %+v", events)
	}
	for _, e := range events {
		if e.TaskID != task.ID || e.Attempt != 1 {
			t.Fatalf("unscoped event: %+v", e)
		}
	}
	if events[2].DurationSeconds != 70 {
		t.Fatalf("operation duration: %+v", events[2])
	}
	sess.Progress(0, "next file", WithActiveEpisode("title01"))
	if len(task.Activities) != 0 || task.EncodingDetailsJSON != "" {
		t.Fatal("cross-file telemetry retained")
	}
}

func TestConcurrentActivitiesRemainBounded(t *testing.T) {
	store, err := queue.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.NewDisc("disc", "fp")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(context.Background(), store, item, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, lane := range []string{"transcription", "references", "audio"} {
		wg.Go(func() {
			for n := range 50 {
				sess.Activity(queue.Activity{ID: lane, Operation: lane, Completed: int64(n), Total: 50, Unit: "files"})
			}
		})
	}
	wg.Wait()
	if len(sess.Task.Activities) != 3 {
		t.Fatal(sess.Task.Activities)
	}
	for _, a := range sess.Task.Activities {
		if a.Completed != 49 {
			t.Fatal(a)
		}
	}
}
