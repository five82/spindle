package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestOperationAgeControlsDisclosure(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{100 * time.Millisecond, 999 * time.Millisecond, time.Second, 4 * time.Second, 9999 * time.Millisecond, 10 * time.Second, 10 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			m := newAppTestModel(t)
			m.now = func() time.Time { return now }
			item := spindle.QueueItem{Tasks: []spindle.Task{{Type: "encoding", State: "running", ActiveAssetKey: "main", Activities: []spindle.Activity{{ID: "video", Operation: "encoding", AssetKey: "main", State: "running", Message: "Accepted video", StartedAt: now.Add(-age).Format(time.RFC3339Nano), Completed: 0, Total: 100, Unit: "frames"}}}}}
			var b strings.Builder
			m.renderTaskBoard(&b, item, m.theme.Styles(), 76)
			got := stripANSI(b.String())
			if strings.Contains(got, "0/100 frames") != (age >= 10*time.Second) {
				t.Fatalf("age %s: %s", age, got)
			}
			if strings.Contains(got, "Accepted video") != (age >= time.Second) {
				t.Fatalf("compact disclosure at %s: %s", age, got)
			}
		})
	}
}

func TestUnknownMeasurementAndStaleContactAreNotZeroProgress(t *testing.T) {
	m := newAppTestModel(t)
	a := spindle.Activity{Operation: "transcribing", State: "running", Message: "Batch of 3 tracks", StartedAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339), UpdatedAt: time.Now().Add(-4 * time.Minute).Format(time.RFC3339)}
	item := spindle.QueueItem{Tasks: []spindle.Task{{Type: "analysis", State: "running", Activities: []spindle.Activity{a}}}}
	for _, stale := range []bool{false, true} {
		if stale {
			m.snapshot.LastError = errors.New("offline")
		}
		var b strings.Builder
		m.renderTaskBoard(&b, item, m.theme.Styles(), 76)
		got := stripANSI(b.String())
		if strings.Contains(got, "0%") || strings.Contains(got, "█") || !strings.Contains(got, "No within-operation percentage") {
			t.Fatal(got)
		}
		if stale && !strings.Contains(got, "Stale snapshot") {
			t.Fatal(got)
		}
	}
}

func TestOnlyScopedFreshProducerETAIsShown(t *testing.T) {
	task := spindle.Task{Type: "apply", State: "running", StartedAt: time.Now().Add(-10 * time.Minute).Format(time.RFC3339), Progress: spindle.TaskProgress{Percent: 50}}
	if got := taskETA(task, time.Now()); got != "" {
		t.Fatal("extrapolated milestone", got)
	}
	task.Type = "encoding"
	task.ActiveAssetKey = "main"
	task.Encoding = &spindle.EncodingStatus{ETASeconds: 75, ChunksComplete: 10, ChunksTotal: 20, Probing: 2}
	task.Activities = []spindle.Activity{{ID: "video", State: "running", UpdatedAt: time.Now().Format(time.RFC3339)}}
	if got := taskETA(task, time.Now()); got != "~2m remaining for this file's video" {
		t.Fatal(got)
	}
	task.Encoding.Calibrating = true
	if taskETA(task, time.Now()) != "" || !strings.Contains(strings.Join(taskExtras(task, time.Now()), " "), "Calibrating quality") {
		t.Fatal("warmup exposed ETA or lost its explanation")
	}
	task.Encoding.Calibrating = false
	if got := strings.Join(taskExtras(task, time.Now()), " "); !strings.Contains(got, "10/20 chunks accepted") || !strings.Contains(got, "2 probing") {
		t.Fatal(got)
	}
	task.Activities[0].UpdatedAt = time.Now().Add(-time.Minute).Format(time.RFC3339)
	if got := taskETA(task, time.Now()); got != "" {
		t.Fatal("stale ETA", got)
	}
	task.Activities[0].State = "done"
	if got := taskETA(task, time.Now()); got != "" {
		t.Fatal("completed video ETA", got)
	}
}

func TestTaskDependenciesWaitsAndStoppedState(t *testing.T) {
	item := spindle.QueueItem{Tasks: []spindle.Task{{Type: "encoding", State: "done"}, {Type: "subtitling", State: "running"}, {Type: "apply", State: "pending", DependsOn: []string{"encoding", "subtitling"}}}}
	got := overviewFor(t, item)
	if !strings.Contains(got, "Needs Subtitling") || strings.Contains(got, "Needs Encoding") {
		t.Fatal(got)
	}
	item.UserStopped = true
	got = overviewFor(t, item)
	if !strings.Contains(got, "Stopped by operator") || strings.Contains(got, "Running Subtitling") {
		t.Fatal(got)
	}
	item.UserStopped = false
	item.Tasks[1].Type = "future-stage"
	if got = overviewFor(t, item); !strings.Contains(got, "future-stage") {
		t.Fatal(got)
	}
}
