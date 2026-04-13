// Package daemonrun provides the daemon runtime entry point, wiring together
// all services, stage handlers, and the workflow manager.
package daemonrun

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/daemon"
	"github.com/five82/spindle/internal/deps"
	"github.com/five82/spindle/internal/discidcache"
	"github.com/five82/spindle/internal/discmonitor"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/jellyfin"
	"github.com/five82/spindle/internal/keydb"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/tmdb"
	"github.com/five82/spindle/internal/transcription"
	"github.com/five82/spindle/internal/workflow"

	// Stage handlers
	"github.com/five82/spindle/internal/audioanalysis"
	"github.com/five82/spindle/internal/contentid"
	"github.com/five82/spindle/internal/encoder"
	"github.com/five82/spindle/internal/identify"
	"github.com/five82/spindle/internal/organizer"
	"github.com/five82/spindle/internal/ripper"
	"github.com/five82/spindle/internal/subtitle"
)

// Run starts the daemon and blocks until shutdown signal.
func Run(ctx context.Context, cfg *config.Config) error {
	// Ensure state/log directory exists.
	logDir := cfg.DaemonLogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	// Clean old log files before opening a new one.
	cleanOldLogs(logDir, cfg.Logging.RetentionDays)

	// Open timestamped JSON log file.
	logFileName := fmt.Sprintf("spindle-%s.log", time.Now().UTC().Format("20060102T150405.000Z"))
	logFilePath := filepath.Join(logDir, logFileName)
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	// Create symlink spindle.log -> active log file.
	symlinkPath := cfg.DaemonLogPath()
	_ = os.Remove(symlinkPath)
	if err := os.Symlink(logFilePath, symlinkPath); err != nil {
		// Hardlink fallback.
		_ = os.Link(logFilePath, symlinkPath)
	}

	// Set up logging: stderr (INFO) + file (DEBUG, toggleable via SIGUSR1).
	var fileLevel slog.LevelVar
	fileLevel.Set(slog.LevelDebug)
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	fileHandler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: &fileLevel})
	multi := newMultiHandler(stderrHandler, fileHandler)

	logBuffer := httpapi.NewLogBuffer(0) // default capacity
	if err := logBuffer.HydrateFromDir(logDir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: log buffer hydration failed: %v\n", err)
	}
	slog.SetDefault(slog.New(httpapi.NewLogHandler(multi, logBuffer)))
	logger := slog.Default()

	logger.Info("daemon log file opened", "path", logFilePath)

	// Open queue database.
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Create clients.
	tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language, logger)
	llmClient := llm.New(cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.Referer, cfg.LLM.Title, cfg.LLM.TimeoutSeconds, logger)
	notifier := notify.New(cfg.Notifications.NtfyTopic, cfg.Notifications.RequestTimeout, logger)
	jfClient := jellyfin.New(cfg.Jellyfin.URL, cfg.Jellyfin.APIKey, logger)
	osClient := opensubtitles.New(cfg.Subtitles.OpenSubtitlesAPIKey, cfg.Subtitles.OpenSubtitlesUserAgent, cfg.Subtitles.OpenSubtitlesUserToken, "", logger)

	// Optional services.
	var discIDStore *discidcache.Store
	if cfg.DiscIDCache.Enabled {
		discIDStore, err = discidcache.Open(cfg.DiscIDCachePath(), logger)
		if err != nil {
			logger.Warn("disc ID cache unavailable",
				"event_type", "disc_id_cache_unavailable",
				"error_hint", "cache file could not be opened",
				"impact", "disc identification will not use cached results",
				"error", err,
			)
		}
	}

	var keydbCat *keydb.Catalog
	if cat, _, loadErr := keydb.LoadOrDownload(ctx, cfg.MakeMKV.KeyDBPath, cfg.MakeMKV.KeyDBDownloadURL,
		cfg.MakeMKV.KeyDBTimeout(), logger); loadErr == nil {
		keydbCat = cat
		logger.Debug("KeyDB catalog loaded", "entries", keydbCat.Size())
	}

	var ripCacheStore *ripcache.Store
	if cfg.RipCache.Enabled {
		ripCacheStore = ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
	}

	transcriber := transcription.New(transcription.Config{
		Engine:    cfg.Subtitles.TranscriptionEngine,
		Model:     cfg.Subtitles.TranscriptionModel,
		Device:    cfg.Subtitles.TranscriptionDevice,
		Precision: cfg.Subtitles.TranscriptionPrecision,
		CacheDir:  cfg.TranscriptionCacheDir(),
		Logger:    logger,
	})

	// Create disc monitor (if optical drive configured).
	// Created before stage handlers so the ripper can pause/resume detection.
	var discMon *discmonitor.Monitor
	if cfg.MakeMKV.OpticalDrive != "" {
		discMon = discmonitor.New(cfg.MakeMKV.OpticalDrive, store, notifier, logger)
	}

	// Create stage handlers.
	identifyHandler := identify.New(cfg, store, tmdbClient, notifier, discIDStore, keydbCat)
	ripperHandler := ripper.New(cfg, store, notifier, ripCacheStore, discMon, ripper.NoTitleOverride)
	contentidHandler := contentid.New(cfg, store, llmClient, osClient, tmdbClient, transcriber)
	encoderHandler := encoder.New(cfg, store, notifier)
	audioHandler := audioanalysis.New(cfg, store, llmClient, transcriber)
	subtitleHandler := subtitle.New(cfg, store, osClient, transcriber)
	organizerHandler := organizer.New(cfg, store, jfClient, notifier)

	// Check dependencies and create status tracker.
	depReqs := []deps.Requirement{
		{Name: "makemkvcon", Command: "makemkvcon", Description: "MakeMKV CLI", Optional: false},
		{Name: "ffmpeg", Command: "ffmpeg", Description: "FFmpeg media processor", Optional: false},
		{Name: "ffprobe", Command: "ffprobe", Description: "FFprobe media analyzer", Optional: false},
		{Name: "mkvmerge", Command: "mkvmerge", Description: "MKVToolNix merge tool", Optional: false},
	}
	depStatuses := deps.CheckBinaries(depReqs)
	depResponses := make([]httpapi.DependencyResponse, len(depStatuses))
	for i, s := range depStatuses {
		depResponses[i] = httpapi.DependencyResponse{
			Name:        s.Name,
			Command:     s.Command,
			Description: s.Description,
			Optional:    s.Optional,
			Available:   s.Available,
			Detail:      s.Detail,
		}
	}
	statusTracker := httpapi.NewStatusTracker(depResponses)

	// Create workflow manager and configure stages.
	manager := workflow.New(store, notifier, statusTracker, logger)
	manager.ConfigureStages([]workflow.PipelineStage{
		{Stage: queue.StageIdentification, Handler: identifyHandler, Semaphore: workflow.SemDisc},
		{Stage: queue.StageRipping, Handler: ripperHandler, Semaphore: workflow.SemDisc},
		{Stage: queue.StageEpisodeIdentification, Handler: contentidHandler, Semaphore: workflow.SemTranscription},
		{Stage: queue.StageEncoding, Handler: encoderHandler, Semaphore: workflow.SemEncode},
		{Stage: queue.StageAudioAnalysis, Handler: audioHandler, Semaphore: workflow.SemTranscription},
		{Stage: queue.StageSubtitling, Handler: subtitleHandler, Semaphore: workflow.SemTranscription},
		{Stage: queue.StageOrganizing, Handler: organizerHandler, Semaphore: workflow.SemNone},
	})

	// Create HTTP API with shutdown channel.
	shutdownCh := make(chan struct{})
	api := httpapi.New(store, cfg.API.Token, discMon, shutdownCh, logger,
		httpapi.WithStatusInfo(httpapi.NewStatusInfo(cfg)),
		httpapi.WithLogBuffer(logBuffer),
		httpapi.WithStatusTracker(statusTracker))

	// Create and start daemon.
	d := daemon.New(cfg, store, manager, api, discMon, logger)
	if err := d.Start(ctx); err != nil {
		return err
	}

	// SIGQUIT: dump goroutine stacks to stderr (non-fatal, continues running).
	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, syscall.SIGQUIT)
	go func() {
		for range quitCh {
			buf := make([]byte, 1<<20) // 1 MiB
			n := runtime.Stack(buf, true)
			_, _ = os.Stderr.Write(buf[:n])
		}
	}()
	defer func() { signal.Stop(quitCh); close(quitCh) }()

	// SIGUSR1: toggle daemon log file level between DEBUG and INFO.
	usr1Ch := make(chan os.Signal, 1)
	signal.Notify(usr1Ch, syscall.SIGUSR1)
	go func() {
		for range usr1Ch {
			if fileLevel.Level() == slog.LevelDebug {
				fileLevel.Set(slog.LevelInfo)
				logger.Info("daemon log level raised to INFO via SIGUSR1")
			} else {
				fileLevel.Set(slog.LevelDebug)
				logger.Info("daemon log level lowered to DEBUG via SIGUSR1")
			}
		}
	}()
	defer func() { signal.Stop(usr1Ch); close(usr1Ch) }()

	// Wait for shutdown signal or HTTP stop request.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal", "signal", sig)
	case <-shutdownCh:
		logger.Info("received HTTP stop request")
	case <-ctx.Done():
	}

	d.Stop()
	return d.Close()
}

// cleanOldLogs removes timestamped daemon log files older than retentionDays.
func cleanOldLogs(dir string, retentionDays int) {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "spindle-") || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
