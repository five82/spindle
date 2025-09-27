package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/workflow"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, _, _, err := config.Load("")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	logger, err := logging.NewFromConfig(cfg)
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	store, err := queue.Open(cfg)
	if err != nil {
		logger.Fatal("open queue store", zap.Error(err))
	}

	workflowManager := workflow.NewManager(cfg, store, logger)
	registerStages(workflowManager, cfg, store, logger)

	d, err := daemon.New(cfg, store, logger, workflowManager)
	if err != nil {
		logger.Fatal("create daemon", zap.Error(err))
	}
	defer d.Close()

	socketPath := buildSocketPath(cfg)
	ipcServer, err := ipc.NewServer(ctx, socketPath, d, logger)
	if err != nil {
		logger.Fatal("start IPC server", zap.Error(err))
	}
	defer ipcServer.Close()
	ipcServer.Serve()

	if err := d.Start(ctx); err != nil {
		logger.Warn("daemon start", zap.Error(err))
	}

	<-ctx.Done()
	logger.Info("spindled shutting down")
}
