package main

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

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

	logger, err := logging.NewFromConfig(cfg)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	store, err := queue.Open(cfg)
	if err != nil {
		logger.Fatal("open queue store", zap.Error(err))
		return err
	}
	defer store.Close()

	workflowManager := workflow.NewManager(cfg, store, logger)
	registerStages(workflowManager, cfg, store, logger)

	d, err := daemon.New(cfg, store, logger, workflowManager)
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
