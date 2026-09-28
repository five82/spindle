package spindle

import (
	"testing"
	"time"
)

func TestTaskTimestampsAndDuration(t *testing.T) {
	start := "2026-02-03T04:05:06Z"
	end := "2026-02-03T04:07:36Z"
	for _, tc := range []struct {
		name string
		task Task
		want time.Duration
	}{
		{"valid", Task{StartedAt: start, FinishedAt: end}, 150 * time.Second},
		{"missing start", Task{FinishedAt: end}, 0},
		{"invalid finish", Task{StartedAt: start, FinishedAt: "oops"}, 0},
		{"reversed", Task{StartedAt: end, FinishedAt: start}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.task.Duration(); got != tc.want {
				t.Fatalf("Duration() = %v, want %v", got, tc.want)
			}
		})
	}
	task := Task{StartedAt: start, FinishedAt: end}
	if task.ParsedStartedAt() != parseTime(start) || task.ParsedFinishedAt() != parseTime(end) {
		t.Fatal("parsed task timestamps differ from source")
	}
	if !(Task{State: "done"}).IsDone() || (Task{State: "pending"}).IsDone() {
		t.Fatal("IsDone must match only done")
	}
}

func TestQueueAndLogTimestamps(t *testing.T) {
	valid := "2026-02-03T04:05:06.123Z"
	item := QueueItem{CreatedAt: valid, UpdatedAt: "not a timestamp"}
	if item.ParsedCreatedAt() != parseTime(valid) || !item.ParsedUpdatedAt().IsZero() {
		t.Fatal("queue timestamps must parse valid values and reject invalid ones")
	}
	if (LogEvent{Timestamp: valid}).ParsedTime() != parseTime(valid) || !(LogEvent{}).ParsedTime().IsZero() {
		t.Fatal("log timestamps must parse valid values and default to zero")
	}
}

func TestTaskPriorityAndFailedEpisodeFiltering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks []Task
		want  string
	}{
		{"running before failed", []Task{{State: "pending"}, {State: "failed"}, {State: "running"}}, "running"},
		{"failed before pending", []Task{{State: "pending"}, {State: "failed"}}, "failed"},
		{"pending", []Task{{State: "done"}, {State: "pending"}}, "pending"},
		{"terminal", []Task{{State: "done"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (QueueItem{Tasks: tc.tasks}).PrimaryTask()
			if tc.want == "" {
				if got != nil {
					t.Fatalf("PrimaryTask() = %+v, want nil", got)
				}
			} else if got == nil || got.State != tc.want {
				t.Fatalf("PrimaryTask() = %+v, want %s", got, tc.want)
			}
		})
	}
	if got := (QueueItem{Tasks: []Task{{State: "done"}}}).FailedTask(); got != nil {
		t.Fatalf("FailedTask() = %+v, want nil", got)
	}
	episodes := []EpisodeStatus{{Key: "a", Status: " FAILED "}, {Key: "b", Status: "completed"}, {Key: "c", Status: "failed"}}
	failed := FilterFailed(episodes)
	if len(failed) != 2 || failed[0].Key != "a" || failed[1].Key != "c" {
		t.Fatalf("FilterFailed() = %+v", failed)
	}
	if (EpisodeStatus{Status: "completed"}).IsFailed() {
		t.Fatal("completed episode is not failed")
	}
}
