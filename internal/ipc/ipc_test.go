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

func TestIPCServerClient(t *testing.T) {
	cfg := testsupport.NewConfig(t)
	cfg.OpticalDrive = ""
	cfg.APIBind = "127.0.0.1:0"
	store := testsupport.MustOpenStore(t, cfg)
	logPath := filepath.Join(cfg.LogDir, "ipc-test.log")
	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})
	d, err := daemon.New(cfg, store, logger, mgr, logPath, logging.NewStreamHub(128))
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	socket := filepath.Join(cfg.LogDir, "spindle.sock")
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

	startResp, err := client.Start()
	if err != nil {
		t.Fatalf("Start RPC failed: %v", err)
	}
	if !startResp.Started {
		t.Fatalf("expected Started=true, message=%s", startResp.Message)
	}

	status, err := client.Status()
	if err != nil {
		t.Fatalf("Status RPC failed: %v", err)
	}
	if !status.Running {
		t.Fatal("expected daemon to be running")
	}

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
	manualDir := filepath.Join(cfg.StagingDir, "manual")
	if err := os.MkdirAll(manualDir, 0o755); err != nil {
		t.Fatalf("mkdir manual: %v", err)
	}
	manualPath := filepath.Join(manualDir, "Manual Movie.mkv")
	if err := os.WriteFile(manualPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write manual file: %v", err)
	}

	addResp, err := client.AddFile(manualPath)
	if err != nil {
		t.Fatalf("AddFile failed: %v", err)
	}
	if addResp.Item.Status != string(queue.StatusRipped) {
		t.Fatalf("expected manual item to be ripped, got %s", addResp.Item.Status)
	}
	if err := os.WriteFile(logPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	logResp, err := client.LogTail(ipc.LogTailRequest{Offset: -1, Limit: 2})
	if err != nil {
		t.Fatalf("LogTail initial failed: %v", err)
	}
	if len(logResp.Lines) != 2 || logResp.Lines[0] != "second" || logResp.Lines[1] != "third" {
		t.Fatalf("unexpected log tail response: %#v", logResp.Lines)
	}

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
	case <-time.After(10 * time.Second):
		t.Fatal("log tail follow timed out")
	}

	if addResp.Item.SourcePath == "" {
		t.Fatal("expected manual item to include source path")
	}
	stopDuring, err := client.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if !stopDuring.Stopped {
		t.Fatalf("expected Stop to report stopped, got: %#v", stopDuring)
	}

	discA.Status = queue.StatusCompleted
	if err := store.Update(ctx, discA); err != nil {
		t.Fatalf("Update discA: %v", err)
	}

	if err := store.Update(ctx, discC); err != nil {
		t.Fatalf("Update discC: %v", err)
	}

	listResp, err := client.QueueList(nil)
	if err != nil {
		t.Fatalf("QueueList failed: %v", err)
	}
	if len(listResp.Items) != 4 {
		t.Fatalf("expected 3 queue items, got %d", len(listResp.Items))
	}

	failedResp, err := client.QueueList([]string{string(queue.StatusFailed)})
	if err != nil {
		t.Fatalf("QueueList failed filter: %v", err)
	}
	if len(failedResp.Items) != 1 || failedResp.Items[0].ID != discB.ID {
		t.Fatalf("expected failed item %d", discB.ID)
	}

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

	clearFailedResp, err := client.QueueClearFailed()
	if err != nil {
		t.Fatalf("QueueClearFailed failed: %v", err)
	}
	if clearFailedResp.Removed != 1 {
		t.Fatalf("expected 1 failed item removed, got %d", clearFailedResp.Removed)
	}

	clearCompletedResp, err := client.QueueClearCompleted()
	if err != nil {
		t.Fatalf("QueueClearCompleted failed: %v", err)
	}
	if clearCompletedResp.Removed != 1 {
		t.Fatalf("expected 1 completed item removed, got %d", clearCompletedResp.Removed)
	}

	retryResp, err := client.QueueRetry(nil)
	if err != nil {
		t.Fatalf("QueueRetry failed: %v", err)
	}
	if retryResp.Updated != 0 {
		t.Fatalf("expected 0 retried items, got %d", retryResp.Updated)
	}

	healthResp, err := client.QueueHealth()
	if err != nil {
		t.Fatalf("QueueHealth failed: %v", err)
	}
	if healthResp.Total != 2 || healthResp.Failed != 0 {
		t.Fatalf("unexpected health response: %#v", healthResp)
	}

	dbHealth, err := client.DatabaseHealth()
	if err != nil {
		t.Fatalf("DatabaseHealth failed: %v", err)
	}
	if !strings.HasSuffix(dbHealth.DBPath, "queue.db") {
		t.Fatalf("unexpected db path: %s", dbHealth.DBPath)
	}

	notifyResp, err := client.TestNotification()
	if err != nil {
		t.Fatalf("TestNotification failed: %v", err)
	}
	if notifyResp == nil || notifyResp.Message == "" {
		t.Fatalf("expected notification message, got %#v", notifyResp)
	}

	clearResp, err := client.QueueClear()
	if err != nil {
		t.Fatalf("QueueClear failed: %v", err)
	}
	if clearResp.Removed != 2 {
		t.Fatalf("expected 2 items cleared, got %d", clearResp.Removed)
	}

	stopResp, err := client.Stop()
	if err != nil {
		t.Fatalf("Stop RPC failed: %v", err)
	}
	if !stopResp.Stopped {
		t.Fatal("expected stop response to be true")
	}

	status2, err := client.Status()
	if err != nil {
		t.Fatalf("Status RPC failed: %v", err)
	}
	if status2.Running {
		t.Fatal("expected daemon to be stopped")
	}
}
