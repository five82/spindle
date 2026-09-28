package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestQueueDisplayStage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage string
		tasks []httpapi.TaskResponse
		want  string
	}{
		{"no tasks", "identification", nil, "identification"},
		{"pending", "episode_identification", []httpapi.TaskResponse{{Type: "encoding", State: "pending"}}, "waiting"},
		{"idle encode", "ripping", []httpapi.TaskResponse{{Type: "encoding", State: "running"}}, "waiting"},
		{"rip with reserved encoder", "identification", []httpapi.TaskResponse{
			{Type: "ripping", State: "running"}, {Type: "encoding", State: "running"},
		}, "ripping"},
		{"overlap", "ripping", []httpapi.TaskResponse{
			{Type: "ripping", State: "running"}, {Type: "encoding", State: "running", ActiveAssetKey: "s05_001"},
		}, "ripping + encoding"},
		{"item 2 stale stage", "episode_identification", []httpapi.TaskResponse{
			{Type: "ripping", State: "done"}, {Type: "episode_identification", State: "done"},
			{Type: "encoding", State: "running", ActiveAssetKey: "s05_004"}, {Type: "analysis", State: "done"},
		}, "encoding"},
		{"analysis overlap", "episode_identification", []httpapi.TaskResponse{
			{Type: "encoding", State: "running", ActiveAssetKey: "s05_004"}, {Type: "analysis", State: "running"},
		}, "encoding + analysis"},
		{"failed overrides running", "failed", []httpapi.TaskResponse{{Type: "ripping", State: "running"}}, "failed"},
		{"completed overrides pending", "completed", []httpapi.TaskResponse{{Type: "encoding", State: "pending"}}, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := httpapi.ItemResponse{Stage: tc.stage, Tasks: tc.tasks}
			if got := queueDisplayStage(item); got != tc.want {
				t.Fatalf("queueDisplayStage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPrintTaskLinesOverlapAndIdle(t *testing.T) {
	tasks := []httpapi.TaskResponse{
		{Type: "ripping", State: "running", Progress: httpapi.ProgressResponse{Percent: 25, Message: "Ripping second title"}},
		{Type: "encoding", State: "running", ActiveAssetKey: "s05_001", Progress: httpapi.ProgressResponse{Percent: 60, Message: "Encoding first title"}},
	}
	tasks[0].Activities = []queue.Activity{{State: "running", Message: "Ripping second title", Completed: 25, Total: 100, Unit: "MakeMKV units"}}
	tasks[1].Activities = []queue.Activity{{State: "running", AssetKey: "s05_001", Message: "Encoding first title", Completed: 60, Total: 100, Unit: "frames"}}
	out := captureStdout(t, func() { printTaskLines("", tasks, false) })
	for _, want := range []string{"Progress (ripping): Ripping second title (25/100 MakeMKV units)", "Progress (encoding): s05_001: Encoding first title (60/100 frames)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	tasks[1].ActiveAssetKey = ""
	tasks[1].Activities = []queue.Activity{{State: "waiting", Operation: "input", Message: "waiting for a ripped asset"}}
	out = captureStdout(t, func() { printTaskLines("", tasks, false) })
	if !strings.Contains(out, "Waiting (encoding): waiting for a ripped asset") || strings.Contains(out, "Encoding first title") || strings.Contains(out, "60%") {
		t.Fatalf("idle encoder showed stale progress: %q", out)
	}
}

// Verify every human-readable command uses tasks, without rewriting the API's
// scheduler stage. Exercise the real daemon socket response consumed by Flyer.
func TestQueueCommandsDisplayActivity(t *testing.T) {
	oldCfg, oldSocket, oldVerbose := cfg, flagSocket, flagVerbose
	t.Cleanup(func() { cfg, flagSocket, flagVerbose = oldCfg, oldSocket, oldVerbose })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "api.sock")
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	item, err := store.NewDisc("South Park", "fp")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureTasks(item, []queue.TaskSpec{{Type: queue.StageRipping}, {Type: queue.StageEncoding}}); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.TasksForItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if err := store.StartTask(task); err != nil {
			t.Fatal(err)
		}
		task.ActiveAssetKey = "s05_001"
		if err := store.UpdateTaskProgress(task); err != nil {
			t.Fatal(err)
		}
	}
	for _, verbose := range []bool{false, true} {
		flagVerbose = verbose
		list := newQueueListCmd()
		out := captureStdout(t, func() {
			if err := list.RunE(list, nil); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "ripping + encoding") {
			t.Fatalf("list verbose=%v: %q", verbose, out)
		}
		show := newQueueShowCmd()
		out = captureStdout(t, func() {
			if err := show.RunE(show, []string{strconv.FormatInt(item.ID, 10)}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "Stage:       ripping + encoding") {
			t.Fatalf("show verbose=%v: %q", verbose, out)
		}
	}
	got, err := fetchQueueItem(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != string(item.Stage) {
		t.Fatalf("API scheduler stage changed: %q, want %q", got.Stage, item.Stage)
	}
}
