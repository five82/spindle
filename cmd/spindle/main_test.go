package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/ipc"
	"spindle/internal/queue"
	"spindle/internal/workflow"
)

type cliTestEnv struct {
	cfg        *config.Config
	store      *queue.Store
	daemon     *daemon.Daemon
	server     *ipc.Server
	socketPath string
	configPath string
	baseDir    string
	cancel     context.CancelFunc
}

func setupCLITestEnv(t *testing.T) *cliTestEnv {
	t.Helper()

	base := t.TempDir()
	cfgVal := config.Default()
	cfgVal.TMDBAPIKey = "test"
	cfgVal.StagingDir = filepath.Join(base, "staging")
	cfgVal.LibraryDir = filepath.Join(base, "library")
	cfgVal.LogDir = filepath.Join(base, "logs")
	cfgVal.ReviewDir = filepath.Join(base, "review")
	cfgVal.OpticalDrive = filepath.Join(base, "fake-drive")

	cfg := &cfgVal

	configPath := filepath.Join(base, "config.toml")
	writeTestConfig(t, configPath, cfg)

	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}

	logger := zap.NewNop()
	mgr := workflow.NewManager(cfg, store, logger)

	d, err := daemon.New(cfg, store, logger, mgr)
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
		cancel:     cancel,
	}

	t.Cleanup(func() {
		cancel()
		srv.Close()
		d.Close()
		store.Close()
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

func TestCLIQueueAndAddFileCommands(t *testing.T) {
	env := setupCLITestEnv(t)
	ctx := context.Background()

	if _, err := env.store.NewDisc(ctx, "Alpha", "fp-alpha"); err != nil {
		t.Fatalf("NewDisc pending: %v", err)
	}

	failed, err := env.store.NewDisc(ctx, "Beta", "fp-beta")
	if err != nil {
		t.Fatalf("NewDisc failed: %v", err)
	}
	failed.Status = queue.StatusFailed
	if err := env.store.Update(ctx, failed); err != nil {
		t.Fatalf("update failed item: %v", err)
	}

	out, _, err := runCLI(t, []string{"queue", "status"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue status: %v", err)
	}
	if !strings.Contains(out, "Pending") || !strings.Contains(out, "Failed") {
		t.Fatalf("unexpected queue status output: %q", out)
	}

	out, _, err = runCLI(t, []string{"queue", "list"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue list: %v", err)
	}
	if !strings.Contains(out, "Alpha") || !strings.Contains(out, "Beta") {
		t.Fatalf("queue list missing items: %q", out)
	}

	out, _, err = runCLI(t, []string{"queue", "retry"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue retry: %v", err)
	}
	if !strings.Contains(out, "Retried 1 failed items") {
		t.Fatalf("unexpected retry output: %q", out)
	}
	updatedFailed, err := env.store.GetByID(ctx, failed.ID)
	if err != nil {
		t.Fatalf("GetByID after retry: %v", err)
	}
	if updatedFailed.Status != queue.StatusPending {
		t.Fatalf("expected failed item retried to pending, got %s", updatedFailed.Status)
	}

	updatedFailed.Status = queue.StatusFailed
	if err := env.store.Update(ctx, updatedFailed); err != nil {
		t.Fatalf("reset failed status: %v", err)
	}

	out, _, err = runCLI(t, []string{"queue", "clear", "--failed"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue clear --failed: %v", err)
	}
	if !strings.Contains(out, "Cleared 1 failed items") {
		t.Fatalf("unexpected clear failed output: %q", out)
	}

	out, _, err = runCLI(t, []string{"queue", "clear"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue clear: %v", err)
	}
	if !strings.Contains(out, "Cleared") {
		t.Fatalf("unexpected clear output: %q", out)
	}

	out, _, err = runCLI(t, []string{"queue", "status"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("queue status after clear: %v", err)
	}
	if !strings.Contains(out, "Queue is empty") {
		t.Fatalf("expected empty queue message, got %q", out)
	}

	manualDir := filepath.Join(env.cfg.StagingDir, "manual")
	if err := os.MkdirAll(manualDir, 0o755); err != nil {
		t.Fatalf("ensure manual dir: %v", err)
	}
	manualPath := filepath.Join(manualDir, "Manual Movie.mkv")
	if err := os.WriteFile(manualPath, []byte("data"), 0o644); err != nil {
		t.Fatalf("write manual file: %v", err)
	}

	out, _, err = runCLI(t, []string{"add-file", manualPath}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("add-file: %v", err)
	}
	if !strings.Contains(out, "Queued manual file") {
		t.Fatalf("unexpected add-file output: %q", out)
	}
}

func TestCLIShowCommands(t *testing.T) {
	env := setupCLITestEnv(t)

	logPath := filepath.Join(env.cfg.LogDir, "spindle.log")
	if err := os.WriteFile(logPath, []byte("first\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	out, _, err := runCLI(t, []string{"show", "--lines", "2"}, env.socketPath, env.configPath)
	if err != nil {
		t.Fatalf("show --lines: %v", err)
	}
	if !strings.Contains(out, "second") || !strings.Contains(out, "third") {
		t.Fatalf("unexpected show output: %q", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--socket", env.socketPath, "--config", env.configPath, "show", "--follow"})
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Execute()
	}()

	time.Sleep(100 * time.Millisecond)
	if err := appendLine(logPath, "followed"); err != nil {
		t.Fatalf("append log: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("show --follow execute: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("show --follow did not exit")
	}

	if !strings.Contains(stdout.String(), "followed") {
		t.Fatalf("expected follow output to include new line, got %q", stdout.String())
	}
}

func TestCLIContractParity(t *testing.T) {
	env := setupCLITestEnv(t)
	stubDir := filepath.Join(env.baseDir, "bin")
	makeStubExecutables(t, stubDir, "makemkvcon", "drapto", "lsblk", "tail")
	originalPath := os.Getenv("PATH")
	uvBin := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	sep := string(os.PathListSeparator)
	compositePath := strings.Join([]string{stubDir, uvBin, originalPath}, sep)
	t.Setenv("PATH", compositePath)

	ctx := context.Background()
	if _, err := env.store.NewDisc(ctx, "Alpha Movie", "fp-alpha-123456"); err != nil {
		t.Fatalf("create alpha disc: %v", err)
	}
	beta, err := env.store.NewDisc(ctx, "Beta Show", "fp-beta-abcdef")
	if err != nil {
		t.Fatalf("create beta disc: %v", err)
	}
	beta.Status = queue.StatusFailed
	if err := env.store.Update(ctx, beta); err != nil {
		t.Fatalf("set beta failed: %v", err)
	}

	logPath := filepath.Join(env.cfg.LogDir, "spindle.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	setBetaStatus := func(status queue.Status) {
		item, err := env.store.GetByID(ctx, beta.ID)
		if err != nil {
			t.Fatalf("lookup beta: %v", err)
		}
		item.Status = status
		item.ProgressStage = ""
		item.ProgressMessage = ""
		item.ErrorMessage = ""
		item.ProgressPercent = 0
		if err := env.store.Update(ctx, item); err != nil {
			t.Fatalf("update beta status: %v", err)
		}
	}

	commands := []struct {
		name       string
		pythonArgs []string
		goArgs     []string
		prepare    func()
	}{
		{
			name:       "status",
			pythonArgs: []string{"status"},
			goArgs:     []string{"status"},
			prepare: func() {
				setBetaStatus(queue.StatusFailed)
			},
		},
		{
			name:       "queue_status",
			pythonArgs: []string{"queue", "status"},
			goArgs:     []string{"queue", "status"},
			prepare: func() {
				setBetaStatus(queue.StatusFailed)
			},
		},
		{
			name:       "queue_list",
			pythonArgs: []string{"queue", "list"},
			goArgs:     []string{"queue", "list"},
			prepare: func() {
				setBetaStatus(queue.StatusFailed)
			},
		},
		{
			name:       "queue_retry",
			pythonArgs: []string{"queue", "retry", fmt.Sprintf("%d", beta.ID)},
			goArgs:     []string{"queue", "retry", fmt.Sprintf("%d", beta.ID)},
			prepare: func() {
				setBetaStatus(queue.StatusFailed)
			},
		},
		{
			name:       "show",
			pythonArgs: []string{"show", "--lines", "2"},
			goArgs:     []string{"show", "--lines", "2"},
			prepare: func() {
				setBetaStatus(queue.StatusFailed)
				if err := os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
					t.Fatalf("rewrite log: %v", err)
				}
			},
		},
	}

	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepare != nil {
				tc.prepare()
			}
			pyOut, _ := runPythonCLI(t, env.configPath, tc.pythonArgs)
			if tc.prepare != nil {
				tc.prepare()
			}
			goOut, _, err := runCLI(t, tc.goArgs, env.socketPath, env.configPath)
			if err != nil {
				t.Fatalf("go CLI %v failed: %v", tc.goArgs, err)
			}
			if strings.TrimSpace(goOut) != strings.TrimSpace(pyOut) {
				t.Fatalf("output mismatch for %s\nGo:\n%s\nPython:\n%s", tc.name, goOut, pyOut)
			}
		})
	}
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
		"staging_dir = %q\nlibrary_dir = %q\nlog_dir = %q\nreview_dir = %q\ntmdb_api_key = %q\noptical_drive = %q\n",
		cfg.StagingDir,
		cfg.LibraryDir,
		cfg.LogDir,
		cfg.ReviewDir,
		cfg.TMDBAPIKey,
		cfg.OpticalDrive,
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func makeStubExecutables(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create stub bin dir: %v", err)
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		var script string
		switch name {
		case "lsblk":
			script = "#!/bin/sh\nexit 0\n"
		case "tail":
			script = `#!/bin/sh
if [ "$1" = "-n" ]; then
    count=$2
    shift 2
    file=$1
    python3 - <<'PY' "$file" "$count"
import sys
from collections import deque
path = sys.argv[1]
count = int(sys.argv[2])
with open(path, 'r', encoding='utf-8', errors='ignore') as fh:
    lines = deque(fh, maxlen=count)
for line in lines:
    sys.stdout.write(line)
PY
    exit 0
fi
echo 'tail stub only supports -n' >&2
exit 1
`
		default:
			script = "#!/bin/sh\nexit 0\n"
		}
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
}
func repoRoot(t *testing.T) string {
	// tests in cmd/spindle run from that directory; traverse up two levels to reach repo root.
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

func runPythonCLI(t *testing.T, configPath string, args []string) (string, string) {
	t.Helper()
	uvPath, err := exec.LookPath("uv")
	if err != nil {
		t.Skipf("skipping contract test: uv not found (%v)", err)
	}
	cmdArgs := append([]string{"run", "spindle", "--config", configPath}, args...)
	cmd := exec.Command(uvPath, cmdArgs...)
	cmd.Dir = repoRoot(t)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python CLI %v failed: %v\nstderr: %s", args, err, stderr.String())
	}
	return stdout.String(), stderr.String()
}
