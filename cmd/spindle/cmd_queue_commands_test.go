package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/queueaccess"
)

// Exercise the stopped-daemon path through the user-facing commands, not just
// the direct DB helpers. In particular, reads must not create a missing DB.
func TestQueueCommandsWithoutDaemon(t *testing.T) {
	oldCfg, oldSocket, oldVerbose := cfg, flagSocket, flagVerbose
	t.Cleanup(func() { cfg, flagSocket, flagVerbose = oldCfg, oldSocket, oldVerbose })
	state := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: state}}
	flagSocket = filepath.Join(state, "missing.sock")
	flagVerbose = false

	list := newQueueListCmd()
	if out := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	}); !strings.Contains(out, "No queue items") {
		t.Fatalf("empty list = %q", out)
	}
	if _, err := os.Stat(cfg.QueueDBPath()); !os.IsNotExist(err) {
		t.Fatalf("empty read created database: %v", err)
	}

	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := store.NewDisc("Alpha", "fp-alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.NewDisc("Beta", "fp-beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveToStage(beta, queue.StageEncoding); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := list.Flags().Set("stage", string(queue.StageEncoding)); err != nil {
		t.Fatal(err)
	}
	if err := list.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	var items []queueaccess.Item
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("list JSON %q: %v", out, err)
	}
	if len(items) != 1 || items[0].DiscTitle != "Beta" {
		t.Fatalf("filtered list = %+v", items)
	}
	if err := list.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	list = newQueueListCmd()
	out = captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Fatalf("table list = %q", out)
	}

	show := newQueueShowCmd()
	id := strconv.FormatInt(alpha.ID, 10)
	out = captureStdout(t, func() {
		if err := show.RunE(show, []string{id}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Title:") || !strings.Contains(out, "Alpha") || !strings.Contains(out, "fp-alpha") {
		t.Fatalf("show = %q", out)
	}
	if err := show.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if err := show.RunE(show, []string{id}); err != nil {
			t.Fatal(err)
		}
	})
	var item queueaccess.Item
	if err := json.Unmarshal([]byte(out), &item); err != nil || item.ID != alpha.ID {
		t.Fatalf("show JSON = %q, %v", out, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"bogus"}, "invalid item ID"},
		{[]string{"9999"}, "not found"},
	} {
		if err := show.RunE(show, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("show %v = %v, want %q", tc.args, err, tc.want)
		}
	}

	clear := newQueueClearCmd()
	for _, tc := range []struct {
		args  []string
		flags []string
		want  string
	}{
		{nil, nil, "provide item IDs"},
		{[]string{"1"}, []string{"all"}, "cannot combine IDs"},
		{nil, []string{"all", "completed"}, "cannot combine --all"},
	} {
		cmd := newQueueClearCmd()
		for _, f := range tc.flags {
			if err := cmd.Flags().Set(f, "true"); err != nil {
				t.Fatal(err)
			}
		}
		if err := cmd.RunE(cmd, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("clear %v %v = %v, want %q", tc.args, tc.flags, err, tc.want)
		}
	}
	if err := clear.Flags().Set("all", "true"); err != nil {
		t.Fatal(err)
	}
	if err := clear.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if err := clear.RunE(clear, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Queue database files removed") {
		t.Fatalf("clear = %q", out)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(cfg.QueueDBPath() + suffix); !os.IsNotExist(err) {
			t.Errorf("queue file %q remains: %v", suffix, err)
		}
	}
}
