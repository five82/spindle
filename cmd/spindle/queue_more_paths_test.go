package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/queue"
)

func TestQueueVerboseDisplaysReviewFailureAndWorkProducts(t *testing.T) {
	oldCfg, oldSocket, oldVerbose := cfg, flagSocket, flagVerbose
	t.Cleanup(func() { cfg, flagSocket, flagVerbose = oldCfg, oldSocket, oldVerbose })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "missing.sock")
	flagVerbose = true
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.NewDisc("Disc", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	item.RipSpecData = `{"version":1}`
	item.MetadataJSON = `{"title":"Disc"}`
	item.EncodingDetailsJSON = `{"progress":1}`
	item.AppendReviewReason("check disc")
	if err := store.UpdateWorkState(item); err != nil {
		t.Fatal(err)
	}
	if err := store.FailStage(item, queue.StageRipping, "failed read"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	list := newQueueListCmd()
	output := captureStdout(t, func() {
		if err := list.RunE(list, nil); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Disc", "fingerprint", "failed read"} {
		if !strings.Contains(output, want) {
			t.Errorf("list missing %q: %s", want, output)
		}
	}
	show := newQueueShowCmd()
	output = captureStdout(t, func() {
		if err := show.RunE(show, []string{strconv.FormatInt(item.ID, 10)}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"FailedAt:", "Review:", "check disc", "Error:", "Metadata:", "RipSpec:", "Encoding:"} {
		if !strings.Contains(output, want) {
			t.Errorf("show missing %q: %s", want, output)
		}
	}
}

func TestQueueMutationPreflightWithoutDaemon(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "missing.sock")
	cases := []struct {
		name    string
		command func() error
		want    string
	}{
		{"retry", func() error { c := newQueueRetryCmd(); return c.RunE(c, nil) }, "daemon"},
		{"cancel", func() error { c := newQueueCancelCmd(); return c.RunE(c, []string{"1"}) }, "daemon"},
		{"clear completed", func() error {
			c := newQueueClearCmd()
			if err := c.Flags().Set("completed", "true"); err != nil {
				return err
			}
			return c.RunE(c, nil)
		}, "daemon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.command()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestQueueErrorPathsThroughClosedDaemonStore(t *testing.T) {
	oldCfg, oldSocket := cfg, flagSocket
	t.Cleanup(func() { cfg, flagSocket = oldCfg, oldSocket })
	dir := t.TempDir()
	cfg = &config.Config{Paths: config.PathsConfig{StateDir: dir}}
	flagSocket = filepath.Join(dir, "api.sock")
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		t.Fatal(err)
	}
	srv := httpapi.New(httpapi.Params{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.ListenUnix(flagSocket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"list", func() error { c := newQueueListCmd(); return c.RunE(c, nil) }},
		{"show", func() error { c := newQueueShowCmd(); return c.RunE(c, []string{"1"}) }},
		{"retry", func() error { c := newQueueRetryCmd(); return c.RunE(c, nil) }},
		{"clear", func() error {
			c := newQueueClearCmd()
			if err := c.Flags().Set("completed", "true"); err != nil {
				return err
			}
			return c.RunE(c, nil)
		}},
		{"cancel", func() error { c := newQueueCancelCmd(); return c.RunE(c, []string{"1"}) }},
		{"remove", func() error { c := newQueueClearCmd(); return c.RunE(c, []string{"1"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("expected API error")
			}
		})
	}
	if _, err := os.Stat(cfg.QueueDBPath()); err != nil {
		t.Fatal(err)
	}
}
