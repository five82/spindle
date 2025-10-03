package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/encoding"
	"spindle/internal/identification"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/workflow"
)

func runDaemonProcess(cmdCtx context.Context, ctx *commandContext) error {
	if ctx == nil {
		return fmt.Errorf("command context is required")
	}

	signalCtx, cancel := signal.NotifyContext(cmdCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := ctx.ensureConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	runID := time.Now().UTC().Format("20060102T150405.000Z")
	logPath := filepath.Join(cfg.LogDir, fmt.Sprintf("spindle-%s.log", runID))
	logger, err := logging.New(logging.Options{
		Level:            cfg.LogLevel,
		Format:           cfg.LogFormat,
		OutputPaths:      []string{"stdout", logPath},
		ErrorOutputPaths: []string{"stderr", logPath},
		Development:      false,
	})
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck
	if err := ensureCurrentLogPointer(cfg.LogDir, logPath); err != nil {
		fmt.Fprintf(os.Stderr, "warn: unable to update spindle.log link: %v\n", err)
	}
	pidPath := filepath.Join(cfg.LogDir, "spindle.pid")
	if err := writePIDFile(pidPath); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer os.Remove(pidPath)

	store, err := queue.Open(cfg)
	if err != nil {
		logger.Fatal("open queue store", zap.Error(err))
		return err
	}
	defer store.Close()

	workflowManager := workflow.NewManager(cfg, store, logger)
	registerStages(workflowManager, cfg, store, logger)

	d, err := daemon.New(cfg, store, logger, workflowManager, logPath)
	if err != nil {
		return fmt.Errorf("create daemon: %w", err)
	}
	defer d.Close()

	socketPath := buildSocketPath(cfg)
	ipcServer, err := ipc.NewServer(signalCtx, socketPath, d, logger)
	if err != nil {
		return fmt.Errorf("start IPC server: %w", err)
	}
	defer ipcServer.Close()
	ipcServer.Serve()

	if err := d.Start(signalCtx); err != nil {
		logger.Warn("daemon start", zap.Error(err))
	}

	<-signalCtx.Done()
	logger.Info("spindle daemon shutting down")
	return nil
}

func registerStages(mgr *workflow.Manager, cfg *config.Config, store *queue.Store, logger *zap.Logger) {
	if mgr == nil || cfg == nil {
		return
	}

	mgr.ConfigureStages(workflow.StageSet{
		Identifier: identification.NewIdentifier(cfg, store, logger),
		Ripper:     ripping.NewRipper(cfg, store, logger),
		Encoder:    encoding.NewEncoder(cfg, store, logger),
		Organizer:  organizer.NewOrganizer(cfg, store, logger),
	})
}

func buildSocketPath(cfg *config.Config) string {
	if cfg == nil {
		return filepath.Join("", "spindle.sock")
	}
	return filepath.Join(cfg.LogDir, "spindle.sock")
}

func ensureCurrentLogPointer(logDir, target string) error {
	if logDir == "" || target == "" {
		return nil
	}
	current := filepath.Join(logDir, "spindle.log")
	if err := os.Remove(current); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing log pointer: %w", err)
	}
	if err := os.Symlink(target, current); err == nil {
		return nil
	} else {
		if err := os.Link(target, current); err == nil {
			return nil
		}
		return fmt.Errorf("link log pointer: %w", err)
	}
}

func writePIDFile(path string) error {
	if path == "" {
		return nil
	}
	value := strconv.Itoa(os.Getpid()) + "\n"
	return os.WriteFile(path, []byte(value), 0o644)
}
