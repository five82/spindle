package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestDiskWaitIsVisibleInFlyerTaskBoard(t *testing.T) {
	m := newAppTestModel(t)
	for _, state := range []string{"pending", "running"} {
		item := spindle.QueueItem{Tasks: []spindle.Task{{Type: "ripping", State: state, Activities: []spindle.Activity{{Operation: "disk_space", State: "waiting", Message: "Waiting for disk space: need 188 GiB, available 90 GiB"}}}}}
		var b strings.Builder
		m.renderTaskBoard(&b, item, m.theme.Styles(), 100)
		got := stripANSI(b.String())
		if !strings.Contains(got, "Waiting for disk space") || !strings.Contains(got, "188 GiB") || !strings.Contains(got, "90 GiB") {
			t.Fatalf("%s disk warning missing: %s", state, got)
		}
	}
}

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
	if got := taskETA(task, time.Now()); got != "~2m" {
		t.Fatal(got)
	}
	task.Encoding.Calibrating = true
	if taskETA(task, time.Now()) != "" || !strings.Contains(strings.Join(taskExtras(task), " "), "Calibrating quality") {
		t.Fatal("warmup exposed ETA or lost its explanation")
	}
	task.Encoding.Calibrating = false
	if got := strings.Join(taskExtras(task), " "); !strings.Contains(got, "10/20 chunks accepted") || !strings.Contains(got, "2 probing") {
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

// A measured video encode reads as percent + count + ETA on the bar line,
// with encoder internals grouped into short lines instead of one sentence.
func TestVideoProgressGroupsEncoderDetail(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	m := newAppTestModel(t)
	m.now = func() time.Time { return now }
	started := now.Add(-time.Minute).Format(time.RFC3339)
	item := spindle.QueueItem{Episodes: []spindle.EpisodeStatus{{Key: "a"}, {Key: "b"}}, Tasks: []spindle.Task{
		{Type: "ripping", State: "done", StartedAt: started, FinishedAt: now.Format(time.RFC3339)},
		{Type: "encoding", State: "running", ActiveAssetKey: "a",
			Activities: []spindle.Activity{{ID: "video", Operation: "encoding", AssetKey: "a", State: "running", Message: "Video frame progress", StartedAt: started, UpdatedAt: now.Format(time.RFC3339), Completed: 250, Total: 1000, Unit: "frames"}},
			Encoding:   &spindle.EncodingStatus{ETASeconds: 75, ChunksComplete: 10, ChunksTotal: 20, Probing: 2, InFlight: 3, FPS: 40, RecentSpeed: 1.5, ActiveWorkers: 4, TargetWorkers: 4, MaxWorkers: 8}},
	}}
	var b strings.Builder
	m.renderTaskBoard(&b, item, m.theme.Styles(), 116)
	got := stripANSI(b.String())
	for _, want := range []string{
		"25% · 250/1000 frames · ~2m\u00a0left\u00a0(this\u00a0file)",
		"10/20 chunks accepted · 2 probing / 0 scoring / 0 finishing · 3 in flight\n",
		"40.0 fps average · 1.50x recent · workers 4/4 (limit 8)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	// Status, stage, and files columns align so durations share a column.
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "  ✓ Done    Ripping      0/2 files 1m 0s") || !strings.HasPrefix(lines[1], "  ◉ Running Encoding     0/2 files") {
		t.Fatalf("misaligned rows:\n%s", got)
	}
}

// At narrow widths the progress measure wraps beside the bar instead of
// running past the panel edge.
func TestVideoProgressMeasureWraps(t *testing.T) {
	now := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)
	m := newAppTestModel(t)
	m.now = func() time.Time { return now }
	started := now.Add(-time.Minute).Format(time.RFC3339)
	item := spindle.QueueItem{Tasks: []spindle.Task{{Type: "encoding", State: "running", ActiveAssetKey: "a",
		Activities: []spindle.Activity{{ID: "video", Operation: "encoding", AssetKey: "a", State: "running", StartedAt: started, UpdatedAt: now.Format(time.RFC3339), Completed: 24831, Total: 69169, Unit: "accepted frames"}},
		Encoding:   &spindle.EncodingStatus{ETASeconds: 480}}}}
	var b strings.Builder
	m.renderTaskBoard(&b, item, m.theme.Styles(), 76)
	got := stripANSI(b.String())
	for _, line := range strings.Split(got, "\n") {
		if lipgloss.Width(line) > 76 {
			t.Fatalf("line overflows: %q", line)
		}
	}
	if !strings.Contains(got, "~8m\u00a0left\u00a0(this\u00a0file)") {
		t.Fatalf("ETA lost when wrapping:\n%s", got)
	}
}
