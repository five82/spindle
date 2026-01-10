package ipc_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/daemon"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/stage"
	"spindle/internal/testsupport"
	"spindle/internal/workflow"
)

type noopStage struct{}

func (noopStage) Prepare(context.Context, *queue.Item) error { return nil }
func (noopStage) Execute(context.Context, *queue.Item) error { return nil }
func (noopStage) HealthCheck(context.Context) stage.Health {
	return stage.Healthy("noop")
}

// setupIPCTest creates the common test infrastructure for IPC tests.
// Returns the client, store, cleanup function, and any setup error.
func setupIPCTest(t *testing.T) (*ipc.Client, *queue.Store, context.Context, context.CancelFunc) {
	t.Helper()

	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.MakeMKV.OpticalDrive = ""
	cfg.Paths.APIBind = "127.0.0.1:0"
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.Paths.LogDir, "ipc-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socket := filepath.Join(cfg.Paths.LogDir, "spindle.sock")
	srv, err := ipc.NewServer(ctx, socket, d, logger)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping IPC server test: %v", err)
		}
		t.Fatalf("ipc.NewServer: %v", err)
	}
	srv.Serve()
	t.Cleanup(func() {
		srv.Close()
	})

	time.Sleep(50 * time.Millisecond)

	client, err := ipc.Dial(socket)
	if err != nil {
		t.Fatalf("ipc.Dial: %v", err)
	}
	t.Cleanup(func() {
		client.Close()
	})

	// Write initial log file for log tail tests
	if err := os.WriteFile(logPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	return client, store, ctx, cancel
}

func TestIPCDaemonStartStop(t *testing.T) {
	client, _, _, _ := setupIPCTest(t)

	t.Run("start daemon", func(t *testing.T) {
		startResp, err := client.Start()
		if err != nil {
			t.Fatalf("Start RPC failed: %v", err)
		}
		if !startResp.Started {
			t.Fatalf("expected Started=true, message=%s", startResp.Message)
		}
	})

	t.Run("status shows running", func(t *testing.T) {
		status, err := client.Status()
		if err != nil {
			t.Fatalf("Status RPC failed: %v", err)
		}
		if !status.Running {
			t.Fatal("expected daemon to be running")
		}
	})

	t.Run("stop daemon", func(t *testing.T) {
		stopResp, err := client.Stop()
		if err != nil {
			t.Fatalf("Stop RPC failed: %v", err)
		}
		if !stopResp.Stopped {
			t.Fatal("expected stop response to be true")
		}
	})

	t.Run("status shows stopped", func(t *testing.T) {
		status, err := client.Status()
		if err != nil {
			t.Fatalf("Status RPC failed: %v", err)
		}
		if status.Running {
			t.Fatal("expected daemon to be stopped")
		}
	})
}

func TestIPCQueueOperations(t *testing.T) {
	client, store, ctx, _ := setupIPCTest(t)

	// Create test items
	discA, err := store.NewDisc(ctx, "Disc A", "fp-a")
	if err != nil {
		t.Fatalf("NewDisc A: %v", err)
	}
	discB, err := store.NewDisc(ctx, "Disc B", "fp-b")
	if err != nil {
		t.Fatalf("NewDisc B: %v", err)
	}
	discB.Status = queue.StatusFailed
	if err := store.Update(ctx, discB); err != nil {
		t.Fatalf("Update discB: %v", err)
	}
	discC, err := store.NewDisc(ctx, "Disc C", "fp-c")
	if err != nil {
		t.Fatalf("NewDisc C: %v", err)
	}
	discC.Status = queue.StatusRipping
	if err := store.Update(ctx, discC); err != nil {
		t.Fatalf("Update discC: %v", err)
	}

	t.Run("list all items", func(t *testing.T) {
		listResp, err := client.QueueList(nil)
		if err != nil {
			t.Fatalf("QueueList failed: %v", err)
		}
		if len(listResp.Items) != 3 {
			t.Fatalf("expected 3 queue items, got %d", len(listResp.Items))
		}
	})

	t.Run("list filtered by status", func(t *testing.T) {
		failedResp, err := client.QueueList([]string{string(queue.StatusFailed)})
		if err != nil {
			t.Fatalf("QueueList failed filter: %v", err)
		}
		if len(failedResp.Items) != 1 || failedResp.Items[0].ID != discB.ID {
			t.Fatalf("expected failed item %d", discB.ID)
		}
	})

	t.Run("reset stuck items", func(t *testing.T) {
		resetResp, err := client.QueueReset()
		if err != nil {
			t.Fatalf("QueueReset failed: %v", err)
		}
		if resetResp.Updated != 1 {
			t.Fatalf("expected 1 item reset, got %d", resetResp.Updated)
		}
		updatedC, err := store.GetByID(ctx, discC.ID)
		if err != nil {
			t.Fatalf("GetByID discC: %v", err)
		}
		if updatedC.Status != queue.StatusIdentified {
			t.Fatalf("expected discC to resume at identification stage after reset, got %s", updatedC.Status)
		}
	})

	t.Run("stop queue item", func(t *testing.T) {
		queueStopResp, err := client.QueueStop([]int64{discC.ID})
		if err != nil {
			t.Fatalf("QueueStop failed: %v", err)
		}
		if queueStopResp.Updated != 1 {
			t.Fatalf("expected 1 item stopped, got %d", queueStopResp.Updated)
		}
		stoppedC, err := store.GetByID(ctx, discC.ID)
		if err != nil {
			t.Fatalf("GetByID stopped discC: %v", err)
		}
		if stoppedC.Status != queue.StatusFailed {
			t.Fatalf("expected discC to be failed after stop, got %s", stoppedC.Status)
		}
		if stoppedC.ReviewReason != queue.UserStopReason {
			t.Fatalf("expected review reason %q, got %q", queue.UserStopReason, stoppedC.ReviewReason)
		}
	})

	t.Run("clear failed items", func(t *testing.T) {
		// Mark discA as completed for later test
		discA.Status = queue.StatusCompleted
		if err := store.Update(ctx, discA); err != nil {
			t.Fatalf("Update discA: %v", err)
		}

		clearFailedResp, err := client.QueueClearFailed()
		if err != nil {
			t.Fatalf("QueueClearFailed failed: %v", err)
		}
		// discB (original failed) and discC (stopped -> failed)
		if clearFailedResp.Removed < 1 {
			t.Fatalf("expected at least 1 failed item removed, got %d", clearFailedResp.Removed)
		}
	})

	t.Run("clear completed items", func(t *testing.T) {
		clearCompletedResp, err := client.QueueClearCompleted()
		if err != nil {
			t.Fatalf("QueueClearCompleted failed: %v", err)
		}
		if clearCompletedResp.Removed != 1 {
			t.Fatalf("expected 1 completed item removed, got %d", clearCompletedResp.Removed)
		}
	})
}

func TestIPCQueueRetry(t *testing.T) {
	client, store, ctx, _ := setupIPCTest(t)

	// Create a failed item
	disc, err := store.NewDisc(ctx, "Failed Disc", "fp-failed")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	disc.Status = queue.StatusFailed
	disc.NeedsReview = true
	disc.ReviewReason = queue.UserStopReason
	if err := store.Update(ctx, disc); err != nil {
		t.Fatalf("Update: %v", err)
	}

	t.Run("retry failed item", func(t *testing.T) {
		retryResp, err := client.QueueRetry(nil)
		if err != nil {
			t.Fatalf("QueueRetry failed: %v", err)
		}
		if retryResp.Updated != 1 {
			t.Fatalf("expected 1 retried item, got %d", retryResp.Updated)
		}
	})

	t.Run("verify retry state", func(t *testing.T) {
		retried, err := store.GetByID(ctx, disc.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if retried.Status != queue.StatusPending {
			t.Fatalf("expected status pending after retry, got %s", retried.Status)
		}
		if retried.NeedsReview {
			t.Fatal("expected NeedsReview to be false after retry")
		}
	})
}

func TestIPCQueueHealth(t *testing.T) {
	client, store, ctx, _ := setupIPCTest(t)

	// Create items in various states
	pending, _ := store.NewDisc(ctx, "Pending", "fp-pending")
	_ = pending

	failed, _ := store.NewDisc(ctx, "Failed", "fp-failed-health")
	failed.Status = queue.StatusFailed
	_ = store.Update(ctx, failed)

	t.Run("health stats", func(t *testing.T) {
		healthResp, err := client.QueueHealth()
		if err != nil {
			t.Fatalf("QueueHealth failed: %v", err)
		}
		if healthResp.Total != 2 {
			t.Errorf("expected total 2, got %d", healthResp.Total)
		}
		if healthResp.Pending != 1 {
			t.Errorf("expected pending 1, got %d", healthResp.Pending)
		}
		if healthResp.Failed != 1 {
			t.Errorf("expected failed 1, got %d", healthResp.Failed)
		}
	})
}

func TestIPCDatabaseHealth(t *testing.T) {
	client, _, _, _ := setupIPCTest(t)

	t.Run("database health", func(t *testing.T) {
		dbHealth, err := client.DatabaseHealth()
		if err != nil {
			t.Fatalf("DatabaseHealth failed: %v", err)
		}
		if !strings.HasSuffix(dbHealth.DBPath, "queue.db") {
			t.Fatalf("unexpected db path: %s", dbHealth.DBPath)
		}
	})
}

func TestIPCTestNotification(t *testing.T) {
	client, _, _, _ := setupIPCTest(t)

	t.Run("test notification", func(t *testing.T) {
		notifyResp, err := client.TestNotification()
		if err != nil {
			t.Fatalf("TestNotification failed: %v", err)
		}
		if notifyResp == nil || notifyResp.Message == "" {
			t.Fatalf("expected notification message, got %#v", notifyResp)
		}
	})
}

func TestIPCLogTail(t *testing.T) {
	client, _, _, _ := setupIPCTest(t)

	t.Run("tail last lines", func(t *testing.T) {
		logResp, err := client.LogTail(ipc.LogTailRequest{Offset: -1, Limit: 2})
		if err != nil {
			t.Fatalf("LogTail initial failed: %v", err)
		}
		if len(logResp.Lines) != 2 || logResp.Lines[0] != "second" || logResp.Lines[1] != "third" {
			t.Fatalf("unexpected log tail response: %#v", logResp.Lines)
		}
	})
}

func TestIPCLogTailFollow(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.MakeMKV.OpticalDrive = ""
	cfg.Paths.APIBind = "127.0.0.1:0"
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.Paths.LogDir, "ipc-follow-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socket := filepath.Join(cfg.Paths.LogDir, "spindle-follow.sock")
	srv, err := ipc.NewServer(ctx, socket, d, logger)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping IPC server test: %v", err)
		}
		t.Fatalf("ipc.NewServer: %v", err)
	}
	srv.Serve()
	t.Cleanup(func() {
		srv.Close()
	})

	time.Sleep(50 * time.Millisecond)

	client, err := ipc.Dial(socket)
	if err != nil {
		t.Fatalf("ipc.Dial: %v", err)
	}
	t.Cleanup(func() {
		client.Close()
	})

	// Write initial content
	if err := os.WriteFile(logPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	// Get initial offset
	logResp, err := client.LogTail(ipc.LogTailRequest{Offset: -1, Limit: 2})
	if err != nil {
		t.Fatalf("LogTail initial failed: %v", err)
	}

	t.Run("follow mode", func(t *testing.T) {
		followDone := make(chan struct{})
		go func(offset int64) {
			resp, err := client.LogTail(ipc.LogTailRequest{Offset: offset, Follow: true, WaitMillis: 500})
			if err != nil {
				t.Errorf("LogTail follow error: %v", err)
				return
			}
			if len(resp.Lines) != 1 || resp.Lines[0] != "fourth" {
				t.Errorf("unexpected follow lines: %#v", resp.Lines)
			}
			close(followDone)
		}(logResp.Offset)

		time.Sleep(100 * time.Millisecond)
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString("fourth\n")
			_ = f.Close()
		} else {
			t.Fatalf("append log: %v", err)
		}

		select {
		case <-followDone:
		case <-time.After(5 * time.Second):
			t.Fatal("log tail follow timed out")
		}
	})
}

func TestIPCQueueClear(t *testing.T) {
	client, store, ctx, _ := setupIPCTest(t)

	// Create an item to clear
	_, err := store.NewDisc(ctx, "Clear Me", "fp-clear")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	t.Run("clear all items", func(t *testing.T) {
		clearResp, err := client.QueueClear()
		if err != nil {
			t.Fatalf("QueueClear failed: %v", err)
		}
		if clearResp.Removed != 1 {
			t.Fatalf("expected 1 item cleared, got %d", clearResp.Removed)
		}
	})

	t.Run("verify empty", func(t *testing.T) {
		items, err := store.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("expected empty queue, got %d items", len(items))
		}
	})
}
