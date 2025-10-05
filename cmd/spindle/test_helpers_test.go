package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spindle/internal/config"
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

type cliTestEnv struct {
	cfg        *config.Config
	store      *queue.Store
	daemon     *daemon.Daemon
	server     *ipc.Server
	socketPath string
	configPath string
	baseDir    string
	logPath    string
	cancel     context.CancelFunc
}

func setupCLITestEnv(t *testing.T) *cliTestEnv {
	t.Helper()

	base := t.TempDir()
	homeDir := filepath.Join(base, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	t.Setenv("HOME", homeDir)
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.OpticalDrive = filepath.Join(base, "fake-drive")
	cfg.APIBind = "127.0.0.1:0"
	logPath := filepath.Join(cfg.LogDir, "spindle-test.log")
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if _, err := os.Stat(logPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(logPath, nil, 0o644); err != nil {
			t.Fatalf("create log file: %v", err)
		}
	}

	configPath := filepath.Join(homeDir, ".config", "spindle", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	writeTestConfig(t, configPath, cfg)

	store := testsupport.MustOpenStore(t, cfg)

	logger := logging.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)
	mgr.ConfigureStages(workflow.StageSet{Identifier: noopStage{}})

	d, err := daemon.New(cfg, store, logger, mgr, logPath)
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(cfg.LogDir, "cli.sock")
	srv, err := ipc.NewServer(ctx, socketPath, d, logger)
	if err != nil {
		t.Fatalf("ipc.NewServer: %v", err)
	}
	srv.Serve()

	env := &cliTestEnv{
		cfg:        cfg,
		store:      store,
		daemon:     d,
		server:     srv,
		socketPath: socketPath,
		configPath: configPath,
		baseDir:    base,
		logPath:    logPath,
		cancel:     cancel,
	}

	t.Cleanup(func() {
		cancel()
		srv.Close()
		d.Close()
	})

	return env
}

func runCLI(t *testing.T, args []string, socket, configPath string) (string, string, error) {
	t.Helper()
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	flags := []string{"--socket", socket}
	if configPath != "" {
		flags = append(flags, "--config", configPath)
	}
	cmd.SetArgs(append(flags, args...))
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func writeTestConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	content := fmt.Sprintf(
		"staging_dir = %q\nlibrary_dir = %q\nlog_dir = %q\nreview_dir = %q\ntmdb_api_key = %q\noptical_drive = %q\napi_bind = %q\n",
		cfg.StagingDir,
		cfg.LibraryDir,
		cfg.LogDir,
		cfg.ReviewDir,
		cfg.TMDBAPIKey,
		cfg.OpticalDrive,
		cfg.APIBind,
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func waitFor(t *testing.T, duration time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", duration)
}

func requireContains(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Fatalf("expected %q to contain %q", output, substr)
	}
}
