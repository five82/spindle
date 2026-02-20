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
	"spindle/internal/notifications"
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

// ipcTestEnv holds the test infrastructure for IPC tests.
type ipcTestEnv struct {
	Client  *ipc.Client
	Store   *queue.Store
	Ctx     context.Context
	Cancel  context.CancelFunc
	LogPath string
}

// setupIPCTest creates the common test infrastructure for IPC tests.
func setupIPCTest(t *testing.T) *ipcTestEnv {
	t.Helper()

	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.MakeMKV.OpticalDrive = ""
	cfg.Paths.APIBind = "127.0.0.1:0"
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.Paths.LogDir, "ipc-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128), nil, notifications.NewService(cfg))
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

	return &ipcTestEnv{
		Client:  client,
		Store:   store,
		Ctx:     ctx,
		Cancel:  cancel,
		LogPath: logPath,
	}
}

func TestIPCDaemonStartStop(t *testing.T) {
	env := setupIPCTest(t)

	t.Run("start daemon", func(t *testing.T) {
		startResp, err := env.Client.Start()
		if err != nil {
			t.Fatalf("Start RPC failed: %v", err)
		}
		if !startResp.Started {
			t.Fatalf("expected Started=true, message=%s", startResp.Message)
		}
	})

	t.Run("status shows running", func(t *testing.T) {
		status, err := env.Client.Status()
		if err != nil {
			t.Fatalf("Status RPC failed: %v", err)
		}
		if !status.Running {
			t.Fatal("expected daemon to be running")
		}
	})

	t.Run("stop daemon", func(t *testing.T) {
		stopResp, err := env.Client.Stop()
		if err != nil {
			t.Fatalf("Stop RPC failed: %v", err)
		}
		if !stopResp.Stopped {
			t.Fatal("expected stop response to be true")
		}
	})

	t.Run("status shows stopped", func(t *testing.T) {
		status, err := env.Client.Status()
		if err != nil {
			t.Fatalf("Status RPC failed: %v", err)
		}
		if status.Running {
			t.Fatal("expected daemon to be stopped")
		}
	})
}

func TestIPCQueueOperations(t *testing.T) {
	env := setupIPCTest(t)

	// Create test items
	discA, err := env.Store.NewDisc(env.Ctx, "Disc A", "fp-a")
	if err != nil {
		t.Fatalf("NewDisc A: %v", err)
	}
	discB, err := env.Store.NewDisc(env.Ctx, "Disc B", "fp-b")
	if err != nil {
		t.Fatalf("NewDisc B: %v", err)
	}
	discB.Status = queue.StatusFailed
	if err := env.Store.Update(env.Ctx, discB); err != nil {
		t.Fatalf("Update discB: %v", err)
	}
	discC, err := env.Store.NewDisc(env.Ctx, "Disc C", "fp-c")
	if err != nil {
		t.Fatalf("NewDisc C: %v", err)
	}
	discC.Status = queue.StatusRipping
	if err := env.Store.Update(env.Ctx, discC); err != nil {
		t.Fatalf("Update discC: %v", err)
	}

	t.Run("list all items", func(t *testing.T) {
		listResp, err := env.Client.QueueList(nil)
		if err != nil {
			t.Fatalf("QueueList failed: %v", err)
		}
		if len(listResp.Items) != 3 {
			t.Fatalf("expected 3 queue items, got %d", len(listResp.Items))
		}
	})

	t.Run("list filtered by status", func(t *testing.T) {
		failedResp, err := env.Client.QueueList([]string{string(queue.StatusFailed)})
		if err != nil {
			t.Fatalf("QueueList failed filter: %v", err)
		}
		if len(failedResp.Items) != 1 || failedResp.Items[0].ID != discB.ID {
			t.Fatalf("expected failed item %d", discB.ID)
		}
	})

	t.Run("reset stuck items", func(t *testing.T) {
		resetResp, err := env.Client.QueueReset()
		if err != nil {
			t.Fatalf("QueueReset failed: %v", err)
		}
		if resetResp.Updated != 1 {
			t.Fatalf("expected 1 item reset, got %d", resetResp.Updated)
		}
		updatedC, err := env.Store.GetByID(env.Ctx, discC.ID)
		if err != nil {
			t.Fatalf("GetByID discC: %v", err)
		}
		if updatedC.Status != queue.StatusIdentified {
			t.Fatalf("expected discC to resume at identification stage after reset, got %s", updatedC.Status)
		}
	})

	t.Run("stop queue item", func(t *testing.T) {
		queueStopResp, err := env.Client.QueueStop([]int64{discC.ID})
		if err != nil {
			t.Fatalf("QueueStop failed: %v", err)
		}
		if queueStopResp.Updated != 1 {
			t.Fatalf("expected 1 item stopped, got %d", queueStopResp.Updated)
		}
		stoppedC, err := env.Store.GetByID(env.Ctx, discC.ID)
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
		if err := env.Store.Update(env.Ctx, discA); err != nil {
			t.Fatalf("Update discA: %v", err)
		}

		clearFailedResp, err := env.Client.QueueClearFailed()
		if err != nil {
			t.Fatalf("QueueClearFailed failed: %v", err)
		}
		// discB (original failed) and discC (stopped -> failed)
		if clearFailedResp.Removed < 1 {
			t.Fatalf("expected at least 1 failed item removed, got %d", clearFailedResp.Removed)
		}
	})

	t.Run("clear completed items", func(t *testing.T) {
		clearCompletedResp, err := env.Client.QueueClearCompleted()
		if err != nil {
			t.Fatalf("QueueClearCompleted failed: %v", err)
		}
		if clearCompletedResp.Removed != 1 {
			t.Fatalf("expected 1 completed item removed, got %d", clearCompletedResp.Removed)
		}
	})
}

func TestIPCQueueRetry(t *testing.T) {
	env := setupIPCTest(t)

	// Create a failed item
	disc, err := env.Store.NewDisc(env.Ctx, "Failed Disc", "fp-failed")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}
	disc.Status = queue.StatusFailed
	disc.NeedsReview = true
	disc.ReviewReason = queue.UserStopReason
	if err := env.Store.Update(env.Ctx, disc); err != nil {
		t.Fatalf("Update: %v", err)
	}

	t.Run("retry failed item", func(t *testing.T) {
		retryResp, err := env.Client.QueueRetry(nil)
		if err != nil {
			t.Fatalf("QueueRetry failed: %v", err)
		}
		if retryResp.Updated != 1 {
			t.Fatalf("expected 1 retried item, got %d", retryResp.Updated)
		}
	})

	t.Run("verify retry state", func(t *testing.T) {
		retried, err := env.Store.GetByID(env.Ctx, disc.ID)
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
	env := setupIPCTest(t)

	// Create items in various states
	_, _ = env.Store.NewDisc(env.Ctx, "Pending", "fp-pending")

	failed, _ := env.Store.NewDisc(env.Ctx, "Failed", "fp-failed-health")
	failed.Status = queue.StatusFailed
	_ = env.Store.Update(env.Ctx, failed)

	t.Run("health stats", func(t *testing.T) {
		healthResp, err := env.Client.QueueHealth()
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
	env := setupIPCTest(t)

	t.Run("database health", func(t *testing.T) {
		dbHealth, err := env.Client.DatabaseHealth()
		if err != nil {
			t.Fatalf("DatabaseHealth failed: %v", err)
		}
		if !strings.HasSuffix(dbHealth.DBPath, "queue.db") {
			t.Fatalf("unexpected db path: %s", dbHealth.DBPath)
		}
	})
}

func TestIPCTestNotification(t *testing.T) {
	env := setupIPCTest(t)

	t.Run("test notification", func(t *testing.T) {
		notifyResp, err := env.Client.TestNotification()
		if err != nil {
			t.Fatalf("TestNotification failed: %v", err)
		}
		if notifyResp == nil || notifyResp.Message == "" {
			t.Fatalf("expected notification message, got %#v", notifyResp)
		}
	})
}

func TestIPCLogTail(t *testing.T) {
	env := setupIPCTest(t)

	t.Run("tail last lines", func(t *testing.T) {
		logResp, err := env.Client.LogTail(ipc.LogTailRequest{Offset: -1, Limit: 2})
		if err != nil {
			t.Fatalf("LogTail initial failed: %v", err)
		}
		if len(logResp.Lines) != 2 || logResp.Lines[0] != "second" || logResp.Lines[1] != "third" {
			t.Fatalf("unexpected log tail response: %#v", logResp.Lines)
		}
	})
}

func TestIPCLogTailFollow(t *testing.T) {
	env := setupIPCTest(t)

	// Get initial offset
	logResp, err := env.Client.LogTail(ipc.LogTailRequest{Offset: -1, Limit: 2})
	if err != nil {
		t.Fatalf("LogTail initial failed: %v", err)
	}

	t.Run("follow mode", func(t *testing.T) {
		followDone := make(chan struct{})
		go func(offset int64) {
			resp, err := env.Client.LogTail(ipc.LogTailRequest{Offset: offset, Follow: true, WaitMillis: 500})
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
		if f, err := os.OpenFile(env.LogPath, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
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
	env := setupIPCTest(t)

	// Create an item to clear
	_, err := env.Store.NewDisc(env.Ctx, "Clear Me", "fp-clear")
	if err != nil {
		t.Fatalf("NewDisc: %v", err)
	}

	t.Run("clear all items", func(t *testing.T) {
		clearResp, err := env.Client.QueueClear()
		if err != nil {
			t.Fatalf("QueueClear failed: %v", err)
		}
		if clearResp.Removed != 1 {
			t.Fatalf("expected 1 item cleared, got %d", clearResp.Removed)
		}
	})

	t.Run("verify empty", func(t *testing.T) {
		items, err := env.Store.List(env.Ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("expected empty queue, got %d items", len(items))
		}
	})
}

func TestIPCDiscPauseResume(t *testing.T) {
	env := setupIPCTest(t)

	// Start the daemon first
	_, err := env.Client.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	t.Run("initial status shows not paused", func(t *testing.T) {
		status, err := env.Client.Status()
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		if status.DiscPaused {
			t.Fatal("expected DiscPaused to be false initially")
		}
	})

	t.Run("pause disc processing", func(t *testing.T) {
		resp, err := env.Client.DiscPause()
		if err != nil {
			t.Fatalf("DiscPause failed: %v", err)
		}
		if !resp.Paused {
			t.Fatal("expected Paused to be true")
		}
		if resp.Message != "disc processing paused" {
			t.Fatalf("unexpected message: %s", resp.Message)
		}
	})

	t.Run("status shows paused", func(t *testing.T) {
		status, err := env.Client.Status()
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		if !status.DiscPaused {
			t.Fatal("expected DiscPaused to be true after pause")
		}
	})

	t.Run("pause again is idempotent", func(t *testing.T) {
		resp, err := env.Client.DiscPause()
		if err != nil {
			t.Fatalf("DiscPause failed: %v", err)
		}
		if !resp.Paused {
			t.Fatal("expected Paused to be true")
		}
		if resp.Message != "disc processing already paused" {
			t.Fatalf("unexpected message: %s", resp.Message)
		}
	})

	t.Run("resume disc processing", func(t *testing.T) {
		resp, err := env.Client.DiscResume()
		if err != nil {
			t.Fatalf("DiscResume failed: %v", err)
		}
		if !resp.Resumed {
			t.Fatal("expected Resumed to be true")
		}
		if resp.Message != "disc processing resumed" {
			t.Fatalf("unexpected message: %s", resp.Message)
		}
	})

	t.Run("status shows not paused", func(t *testing.T) {
		status, err := env.Client.Status()
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		if status.DiscPaused {
			t.Fatal("expected DiscPaused to be false after resume")
		}
	})

	t.Run("resume again is idempotent", func(t *testing.T) {
		resp, err := env.Client.DiscResume()
		if err != nil {
			t.Fatalf("DiscResume failed: %v", err)
		}
		if !resp.Resumed {
			t.Fatal("expected Resumed to be true")
		}
		if resp.Message != "disc processing already active" {
			t.Fatalf("unexpected message: %s", resp.Message)
		}
	})
}
