package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/encoding"
	"spindle/internal/episodeid"
	"spindle/internal/identification"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/subtitles"
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
	logPath := filepath.Join(cfg.Paths.LogDir, fmt.Sprintf("spindle-%s.log", runID))
	eventsPath := filepath.Join(cfg.Paths.LogDir, fmt.Sprintf("spindle-%s.events", runID))
	logHub := logging.NewStreamHub(4096)
	eventArchive, archiveErr := logging.NewEventArchive(eventsPath)
	if archiveErr != nil {
		fmt.Fprintf(os.Stderr, "warn: unable to initialize log archive: %v\n", archiveErr)
	} else if eventArchive != nil {
		logHub.AddSink(eventArchive)
	}
	logLevel := ctx.resolvedLogLevel(cfg)
	logger, err := logging.New(logging.Options{
		Level:            logLevel,
		Format:           cfg.Logging.Format,
		OutputPaths:      []string{"stdout", logPath},
		ErrorOutputPaths: []string{"stderr", logPath},
		Development:      ctx.logDevelopment(cfg),
		Stream:           logHub,
	})
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logDependencySnapshot(logger, cfg)
	if err := ensureCurrentLogPointer(cfg.Paths.LogDir, logPath); err != nil {
		fmt.Fprintf(os.Stderr, "warn: unable to update spindle.log link: %v\n", err)
	}
	logging.CleanupOldLogs(logger, cfg.Logging.RetentionDays,
		logging.RetentionTarget{Dir: cfg.Paths.LogDir, Pattern: "spindle-*.log", Exclude: []string{logPath}},
		logging.RetentionTarget{Dir: cfg.Paths.LogDir, Pattern: "spindle-*.events", Exclude: []string{eventsPath}},
		logging.RetentionTarget{Dir: filepath.Join(cfg.Paths.LogDir, "items"), Pattern: "*.log"},
		logging.RetentionTarget{Dir: filepath.Join(cfg.Paths.LogDir, "tool"), Pattern: "*.log"},
	)
	pidPath := filepath.Join(cfg.Paths.LogDir, "spindle.pid")
	if err := writePIDFile(pidPath); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	defer os.Remove(pidPath)

	store, err := queue.Open(cfg)
	if err != nil {
		logger.Error("open queue store", logging.Error(err))
		return err
	}
	defer store.Close()

	workflowManager := workflow.NewManagerWithOptions(cfg, store, logger, notifications.NewService(cfg), logHub)
	registerStages(workflowManager, cfg, store, logger)

	d, err := daemon.New(cfg, store, logger, workflowManager, logPath, logHub, eventArchive)
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
		logger.Warn("daemon start", logging.Error(err))
	}

	<-signalCtx.Done()
	logger.Info("spindle daemon shutting down")
	return nil
}

func registerStages(mgr *workflow.Manager, cfg *config.Config, store *queue.Store, logger *slog.Logger) {
	if mgr == nil || cfg == nil {
		return
	}

	var subtitleStage workflow.StageHandler
	if cfg.Subtitles.Enabled {
		service := subtitles.NewService(cfg, logger)
		subtitleStage = subtitles.NewStage(store, service, logger)
	}

	mgr.ConfigureStages(workflow.StageSet{
		Identifier:        identification.NewIdentifier(cfg, store, logger),
		Ripper:            ripping.NewRipper(cfg, store, logger),
		EpisodeIdentifier: episodeid.NewEpisodeIdentifier(cfg, store, logger),
		Encoder:           encoding.NewEncoder(cfg, store, logger),
		Subtitles:         subtitleStage,
		Organizer:         organizer.NewOrganizer(cfg, store, logger),
	})
}

func buildSocketPath(cfg *config.Config) string {
	if cfg == nil {
		return filepath.Join("", "spindle.sock")
	}
	return filepath.Join(cfg.Paths.LogDir, "spindle.sock")
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

func logDependencySnapshot(logger *slog.Logger, cfg *config.Config) {
	if logger == nil || cfg == nil {
		return
	}
	makemkv := cfg.MakemkvBinary()
	drapto := cfg.DraptoBinary()
	ffprobe := cfg.FFprobeBinary()
	logger.Info("dependency snapshot",
		logging.String(logging.FieldEventType, "dependency_snapshot"),
		logging.Bool("tmdb_key_present", strings.TrimSpace(cfg.TMDB.APIKey) != ""),
		logging.Bool("makemkv_available", binaryAvailable(makemkv)),
		logging.String("makemkv_binary", makemkv),
		logging.Bool("drapto_available", binaryAvailable(drapto)),
		logging.String("drapto_binary", drapto),
		logging.Bool("ffprobe_available", binaryAvailable(ffprobe)),
		logging.String("ffprobe_binary", ffprobe),
		logging.Bool("opensubtitles_enabled", cfg.Subtitles.OpenSubtitlesEnabled),
		logging.Bool("opensubtitles_key_present", strings.TrimSpace(cfg.Subtitles.OpenSubtitlesAPIKey) != ""),
		logging.Bool("whisperx_cuda", cfg.Subtitles.WhisperXCUDAEnabled),
		logging.String("whisperx_vad_method", strings.TrimSpace(cfg.Subtitles.WhisperXVADMethod)),
	)
}

func binaryAvailable(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	_, err := exec.LookPath(name)
	return err == nil
}
