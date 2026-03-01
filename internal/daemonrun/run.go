package daemonrun

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

	"github.com/google/uuid"

	"spindle/internal/audioanalysis"
	"spindle/internal/config"
	"spindle/internal/daemon"
	"spindle/internal/deps"
	"spindle/internal/encoding"
	"spindle/internal/episodeid"
	"spindle/internal/identification"
	"spindle/internal/ipc"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/organizer"
	"spindle/internal/queue"
	"spindle/internal/ripping"
	"spindle/internal/stage"
	"spindle/internal/subtitles"
	"spindle/internal/workflow"
)

// Options configures daemon process runtime behavior.
type Options struct {
	LogLevel    string
	Development bool
	Diagnostic  bool
}

// Run starts the spindle daemon runtime loop.
func Run(cmdCtx context.Context, cfg *config.Config, opts Options) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}

	signalCtx, cancel := signal.NotifyContext(cmdCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	var sessionID string
	var debugLogPath string
	var debugItemsDir string
	if opts.Diagnostic {
		sessionID = uuid.NewString()
		debugDir := filepath.Join(cfg.Paths.LogDir, "debug")
		if err := os.MkdirAll(debugDir, 0o755); err != nil {
			return fmt.Errorf("create debug log directory: %w", err)
		}
		debugLogPath = filepath.Join(debugDir, fmt.Sprintf("spindle-%s.log", runID))
		debugItemsDir = filepath.Join(debugDir, "items")
		if err := os.MkdirAll(debugItemsDir, 0o755); err != nil {
			return fmt.Errorf("create debug items directory: %w", err)
		}
	}

	loggerOpts := logging.Options{
		Level:            opts.LogLevel,
		Format:           cfg.Logging.Format,
		OutputPaths:      []string{"stdout", logPath},
		ErrorOutputPaths: []string{"stderr", logPath},
		Development:      opts.Development,
		Stream:           logHub,
		SessionID:        sessionID,
	}
	logger, err := logging.New(loggerOpts)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}

	if opts.Diagnostic {
		debugLogger, debugErr := logging.New(logging.Options{
			Level:            "debug",
			Format:           "json",
			OutputPaths:      []string{debugLogPath},
			ErrorOutputPaths: []string{debugLogPath},
			Development:      true,
			SessionID:        sessionID,
		})
		if debugErr != nil {
			fmt.Fprintf(os.Stderr, "warn: unable to initialize debug logger: %v\n", debugErr)
		} else {
			logger = logging.TeeLogger(logger, debugLogger.Handler())
			if err := ensureCurrentLogPointer(filepath.Join(cfg.Paths.LogDir, "debug"), debugLogPath); err != nil {
				fmt.Fprintf(os.Stderr, "warn: unable to update debug/spindle.log link: %v\n", err)
			}
		}
		logger.Info("diagnostic mode enabled",
			logging.String(logging.FieldEventType, "diagnostic_mode_enabled"),
			logging.String(logging.FieldSessionID, sessionID),
			logging.String("debug_log_path", debugLogPath),
		)
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

	notifier := notifications.NewService(cfg)
	workflowManager := workflow.NewManagerWithOptions(cfg, store, logger, notifier, logHub,
		workflow.WithDiagnosticMode(opts.Diagnostic, debugItemsDir, sessionID))
	registerStages(workflowManager, cfg, store, logger, notifier)

	d, err := daemon.New(cfg, store, logger, workflowManager, logPath, logHub, eventArchive, notifier)
	if err != nil {
		return fmt.Errorf("create daemon: %w", err)
	}
	defer d.Close()

	socketPath := filepath.Join(cfg.Paths.LogDir, "spindle.sock")
	ipcServer, err := ipc.NewServer(signalCtx, socketPath, d, logger)
	if err != nil {
		return fmt.Errorf("start IPC server: %w", err)
	}
	defer ipcServer.Close()
	ipcServer.Serve()

	if err := d.Start(signalCtx); err != nil {
		logger.Warn("daemon start failed",
			logging.Error(err),
			logging.String(logging.FieldEventType, "daemon_start_failed"),
			logging.String(logging.FieldErrorHint, "check configuration and queue database access"),
			logging.String(logging.FieldImpact, "daemon may not process queue items"),
		)
	}

	<-signalCtx.Done()
	logger.Info("spindle daemon shutting down")
	return nil
}

func registerStages(mgr *workflow.Manager, cfg *config.Config, store *queue.Store, logger *slog.Logger, notifier notifications.Service) {
	if mgr == nil || cfg == nil {
		return
	}

	var subtitleStage stage.Handler
	if cfg.Subtitles.Enabled {
		service := subtitles.NewService(cfg, logger)
		subtitleStage = subtitles.NewGenerator(store, service, logger)
	}

	mgr.ConfigureStages(workflow.StageSet{
		Identifier:        identification.NewIdentifier(cfg, store, logger, notifier),
		Ripper:            ripping.NewRipper(cfg, store, logger, notifier),
		AudioAnalysis:     audioanalysis.NewAnalyzer(cfg, store, logger),
		EpisodeIdentifier: episodeid.NewEpisodeIdentifier(cfg, store, logger),
		Encoder:           encoding.NewEncoder(cfg, store, logger, notifier),
		Subtitles:         subtitleStage,
		Organizer:         organizer.NewOrganizer(cfg, store, logger, notifier),
	})
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
	}
	if err := os.Link(target, current); err != nil {
		return fmt.Errorf("link log pointer: %w", err)
	}
	return nil
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
	ffmpeg := deps.ResolveFFmpegPath()
	ffprobe := cfg.FFprobeBinary()
	logger.Info("dependency snapshot",
		logging.String(logging.FieldEventType, "dependency_snapshot"),
		logging.Bool("tmdb_key_present", strings.TrimSpace(cfg.TMDB.APIKey) != ""),
		logging.Bool("makemkv_available", binaryAvailable(makemkv)),
		logging.String("makemkv_binary", makemkv),
		logging.Bool("ffmpeg_available", binaryAvailable(ffmpeg)),
		logging.String("ffmpeg_binary", ffmpeg),
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
