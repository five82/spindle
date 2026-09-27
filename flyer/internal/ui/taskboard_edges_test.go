package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func TestTaskBoardStateRowsAndElapsed(t *testing.T) {
	m := newAppTestModel(t)
	styles := m.theme.Styles()
	now := time.Now()
	started := now.Add(-10 * time.Minute).Format(time.RFC3339)
	finished := now.Add(-5 * time.Minute).Format(time.RFC3339)
	for _, tc := range []struct {
		name string
		item spindle.QueueItem
		want []string
	}{
		{"pending without tasks", spindle.QueueItem{Stage: "ripping"}, []string{"tasks pending"}},
		{"failed without tasks", spindle.QueueItem{Stage: "failed"}, []string{"Failed"}},
		{"completed without tasks", spindle.QueueItem{Stage: "completed"}, []string{"Complete"}},
		{"concurrent finished tasks", spindle.QueueItem{Stage: "completed", CreatedAt: started, UpdatedAt: finished, Tasks: []spindle.Task{
			{Type: "ripping", State: "done", StartedAt: started, FinishedAt: finished, Attempts: 2},
			{Type: "encoding", State: "done", StartedAt: started, FinishedAt: finished},
		}}, []string{"stages overlap", "attempt 2"}},
		{"running and failed details", spindle.QueueItem{Stage: "encoding", Tasks: []spindle.Task{
			{Type: "copy", State: "running", Progress: spindle.TaskProgress{Message: "Copying", TotalBytes: 1024 * 1024, BytesCopied: 512 * 1024}, ActiveAssetKey: "episode-1"},
			{Type: "ripping", State: "failed", Error: "disc read error"},
			{Type: "encoding", State: "pending"},
		}}, []string{"Copying (episode-1)", "disc read error", "0.50 MiB"}},
		{"subsecond finish", spindle.QueueItem{Tasks: []spindle.Task{{Type: "copy", State: "done", StartedAt: started, FinishedAt: started}}}, []string{"<1s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			m.renderTaskBoard(&b, tc.item, styles, 100)
			got := stripANSI(b.String())
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("board %q missing %q", got, want)
				}
			}
		})
	}
}

func TestTaskETAAndExtrasFallback(t *testing.T) {
	now := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
	base := spindle.Task{Type: "copy", State: "running", StartedAt: now, Progress: spindle.TaskProgress{Percent: 50}}
	if got := taskETA(spindle.QueueItem{}, base, spindle.EpisodeTotals{}); !strings.HasPrefix(got, "ETA ") {
		t.Fatalf("derived ETA = %q", got)
	}
	base.StartedAt = time.Now().Add(time.Hour).Format(time.RFC3339)
	if got := taskETA(spindle.QueueItem{}, base, spindle.EpisodeTotals{}); got != "" {
		t.Fatalf("future ETA = %q", got)
	}
	base.StartedAt = ""
	if got := taskETA(spindle.QueueItem{}, base, spindle.EpisodeTotals{}); got != "" {
		t.Fatalf("no start ETA = %q", got)
	}
	for _, percent := range []float64{0, 100} {
		base.Progress.Percent = percent
		if got := taskETA(spindle.QueueItem{}, base, spindle.EpisodeTotals{}); got != "" {
			t.Fatalf("percent %v ETA = %q", percent, got)
		}
	}
	encode := spindle.Task{Type: "encoding", Progress: spindle.TaskProgress{Percent: 50}}
	item := spindle.QueueItem{Encoding: &spindle.EncodingStatus{ETASeconds: 75, FPS: 60, Substage: " pass 2 "}}
	if got := strings.Join(taskExtras(item, encode, spindle.EpisodeTotals{Planned: 1}), " "); !strings.Contains(got, "ETA 1m 15s") || !strings.Contains(got, "60 fps") || !strings.Contains(got, "pass 2") {
		t.Fatalf("encode extras = %q", got)
	}
	if got := runningTaskMessage(spindle.Task{ActiveAssetKey: "EP1"}); got != "EP1" {
		t.Fatalf("key-only message = %q", got)
	}
	if got := runningTaskMessage(spindle.Task{ActiveAssetKey: "EP1", Progress: spindle.TaskProgress{Message: "Processing ep1"}}); got != "Processing ep1" {
		t.Fatalf("duplicate key message = %q", got)
	}
	if count, ok := stageTaskCount("encoded", spindle.QueueItem{}, spindle.Task{State: "running", ActiveAssetKey: "missing"}, nil, spindle.EpisodeTotals{Planned: 3, Encoded: 1}); !ok || count != 1 {
		t.Fatalf("missing active count = %d, %v", count, ok)
	}
}
