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
	"sync"
	"syscall"
	"time"

	"github.com/gofrs/flock"

	"github.com/five82/spindle/internal/config"
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
	"github.com/five82/spindle/internal/ripspec"
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

// encodeTierClaims picks the encode slot matching the item's resolution
// tier: encode_4k on a positive UHD signal from identification, encode_1080p
// otherwise (unknown tiers default to 1080p so pairing stays available for
// the common non-UHD library; see the Phase 5 notes in the task-graph plan).
func encodeTierClaims(item *queue.Item) map[string]int {
	env, err := ripspec.Parse(item.RipSpecData)
	if err == nil && env.Metadata.UHD {
		return map[string]int{"encode_4k": 1}
	}
	return map[string]int{"encode_1080p": 1}
}

// contentIDClaims claims the GPU only for TV items: episode identification
// is a pure skip for movies and unknown media types, so those items must
// not queue behind other items' GPU work just to no-op through the stage.
func contentIDClaims(item *queue.Item) map[string]int {
	env, err := ripspec.Parse(item.RipSpecData)
	if err == nil && strings.EqualFold(strings.TrimSpace(env.Metadata.MediaType), "tv") {
		return map[string]int{"gpu": 1}
	}
	return map[string]int{}
}

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

	transcriber := transcription.New(cfg.Subtitles.WhisperXModel, cfg.Subtitles.WhisperXCUDAEnabled, cfg.Subtitles.WhisperXVADMethod, cfg.Subtitles.WhisperXHFToken, logger)

	// Create disc monitor (if optical drive configured).
	// Created before stage handlers so the ripper can pause/resume detection.
	var discMon *discmonitor.Monitor
	if cfg.MakeMKV.OpticalDrive != "" {
		discMon = discmonitor.New(cfg.MakeMKV.OpticalDrive, store, notifier, logger)
	}

	// Create stage handlers.
	identifyHandler := identify.New(cfg, tmdbClient, notifier, discIDStore, keydbCat)
	ripperHandler := ripper.New(cfg, notifier, ripCacheStore, discMon, ripper.NoTitleOverride)
	contentidHandler := contentid.New(cfg, llmClient, osClient, tmdbClient, transcriber)
	encoderHandler := encoder.New(cfg, notifier)
	analysisHandler := audioanalysis.New(cfg, llmClient, transcriber)
	subtitleHandler := subtitle.New(cfg, transcriber)
	applyHandler := audioanalysis.NewApply(cfg)
	organizerHandler := organizer.New(cfg, jfClient, notifier)

	// Check dependencies and create status tracker.
	depReqs := []deps.Requirement{
		{Name: "makemkvcon", Command: "makemkvcon", Description: "MakeMKV CLI", Optional: false},
		{Name: "ffmpeg", Command: "ffmpeg", Description: "FFmpeg media processor", Optional: false},
		{Name: "ffprobe", Command: "ffprobe", Description: "FFprobe media analyzer", Optional: false},
		{Name: "mkvmerge", Command: "mkvmerge", Description: "MKVToolNix merge tool", Optional: false},
		{Name: "libSvtAv1Enc", Command: "libSvtAv1Enc.so", Description: "Reel SVT-AV1 encoder library", Optional: false, Library: true},
		{Name: "libavformat", Command: "libavformat.so", Description: "Reel FFmpeg format library", Optional: false, Library: true},
		{Name: "libavcodec", Command: "libavcodec.so", Description: "Reel FFmpeg codec library", Optional: false, Library: true},
		{Name: "libavutil", Command: "libavutil.so", Description: "Reel FFmpeg utility library", Optional: false, Library: true},
		{Name: "libswscale", Command: "libswscale.so", Description: "Reel FFmpeg scaling library", Optional: false, Library: true},
		{Name: "libswresample", Command: "libswresample.so", Description: "Reel FFmpeg resampling library", Optional: false, Library: true},
		{Name: "libopusenc", Command: "libopusenc.so", Description: "Reel Opus encoder library", Optional: false, Library: true},
		{Name: "libvship", Command: "libvship.so", Description: "Reel target-quality VSHIP/CVVDP library", Optional: false, Library: true},
	}
	depStatuses := deps.CheckRequirements(depReqs)
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
	// The per-item template is a DAG (task-graph plan, Phase 4b/4d):
	// encoding starts right after identification and STREAMS ripped assets
	// as the ripper lands them (episode 1 encodes while episode 2 rips);
	// the analysis branch (commentary detection, subtitle generation --
	// both from RIPPED sources) runs after episode identification,
	// concurrently with encoding; apply joins the branches and performs
	// every write to the encoded files. Stable keys make this safe: episode
	// matching no longer renames asset keys, so it runs off the encode
	// critical path. Budgets stay at capacity 1 per resource (drive,
	// gpu, encode) -- the same exclusivity as before; the overlap is
	// between the gpu and encode lanes, which were already concurrent
	// across items. Registration order is the display priority: during
	// overlap the item shows the encoding stage (encoding owns progress).
	manager.ConfigureStages([]workflow.PipelineStage{
		{Stage: queue.StageIdentification, Handler: identifyHandler, Claims: map[string]int{"drive": 1}},
		{Stage: queue.StageRipping, Handler: ripperHandler, Claims: map[string]int{"drive": 1}, DependsOn: []queue.Stage{queue.StageIdentification}},
		{Stage: queue.StageEpisodeIdentification, Handler: contentidHandler, Claims: map[string]int{"gpu": 1}, ClaimsFunc: contentIDClaims, DependsOn: []queue.Stage{queue.StageRipping}},
		{Stage: queue.StageEncoding, Handler: encoderHandler,
			// Cross-title pairing (task-graph plan, Phase 5): one encode slot
			// per resolution tier, so a 1080p encode and a 4K encode pair
			// (validated 2026-07-04: probe scores bit-identical cross-process,
			// +23.9% pooled wall on the air+sully pair) while same-tier
			// concurrency stays 1. The tier claim resolves per item from the
			// UHD flag identification stamps into the envelope; a missed UHD
			// marker degrades to a disguised same-tier pair (slower, never
			// incorrect).
			Claims:     map[string]int{"encode_1080p": 1, "encode_4k": 1},
			ClaimsFunc: encodeTierClaims,
			DependsOn:  []queue.Stage{queue.StageIdentification}},
		{Stage: queue.StageAnalysis, Handler: analysisHandler, Claims: map[string]int{"gpu": 1}, DependsOn: []queue.Stage{queue.StageEpisodeIdentification}},
		{Stage: queue.StageSubtitling, Handler: subtitleHandler, Claims: map[string]int{"gpu": 1}, DependsOn: []queue.Stage{queue.StageAnalysis}},
		{Stage: queue.StageApply, Handler: applyHandler, DependsOn: []queue.Stage{queue.StageSubtitling, queue.StageEncoding}},
		{Stage: queue.StageOrganizing, Handler: organizerHandler, DependsOn: []queue.Stage{queue.StageApply}},
	})

	// Create HTTP API with shutdown channel. The manager supplies the
	// pipeline template and live resource occupancy for /api/status.
	shutdownCh := make(chan struct{})
	api := httpapi.New(store, cfg.API.Token, discMon, shutdownCh, logger, httpapi.NewStatusInfo(cfg), logBuffer, statusTracker, manager.PipelineInfo(), manager)

	// Create netlink monitor if optical drive is configured.
	var netlinkMon *discmonitor.NetlinkMonitor
	if discMon != nil {
		netlinkMon = discmonitor.NewNetlinkMonitor(
			discMon.Device(),
			func(ctx context.Context, device string) {
				if err := discmonitor.WaitForReady(ctx, device, logger); err != nil {
					logger.Warn("drive not ready after netlink event",
						"event_type", "drive_wait_failed",
						"error_hint", err.Error(),
						"impact", "disc detection skipped",
					)
					return
				}
				result, err := discMon.DetectAndEnqueue(ctx)
				if err != nil {
					logger.Error("disc detection after netlink event failed",
						"event_type", "disc_detection_failed",
						"error_hint", "detect and enqueue after netlink event failed",
						"error", err,
					)
					return
				}
				if result == nil {
					return // paused, already processing, or no disc
				}
			},
			discMon.IsPaused,
			logger,
		)
	}

	lockPath := cfg.LockPath()
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock file: %w", err)
	}
	if !locked {
		return fmt.Errorf("another daemon instance is running (lock: %s)", lockPath)
	}

	// Startup recovery: reset any stale in-progress items and running tasks.
	if err := store.ResetInProgress(); err != nil {
		logger.Error("startup recovery failed",
			"event_type", "startup_recovery_failed",
			"error_hint", "failed to reset in_progress flags on startup",
			"error", err,
		)
	}
	if err := store.ResetRunningTasks(); err != nil {
		logger.Error("startup recovery failed",
			"event_type", "startup_recovery_failed",
			"error_hint", "failed to reset running tasks on startup",
			"error", err,
		)
	}

	// Start HTTP API.
	socketPath := cfg.SocketPath()
	if err := api.ListenUnix(socketPath); err != nil {
		_ = lock.Unlock()
		return fmt.Errorf("start unix socket: %w", err)
	}
	logger.Info("HTTP API listening", "socket", socketPath)

	if cfg.API.Bind != "" {
		if err := api.ListenTCP(cfg.API.Bind); err != nil {
			_ = lock.Unlock()
			return fmt.Errorf("start tcp: %w", err)
		}
		logger.Info("HTTP API listening", "addr", cfg.API.Bind)
	}

	// Start netlink monitor (non-fatal).
	if netlinkMon != nil {
		if err := netlinkMon.Start(ctx); err != nil {
			logger.Warn("netlink monitor not started",
				"event_type", "netlink_start_failed",
				"error_hint", err.Error(),
				"impact", "automatic disc detection unavailable, manual detect via API still works",
			)
		}
	}

	// Start workflow manager.
	workflowCtx, workflowCancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		manager.Run(workflowCtx)
	}()

	logger.Info("daemon started")

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

	logger.Info("daemon stopping")

	// Stop netlink monitor.
	if netlinkMon != nil {
		netlinkMon.Stop()
	}

	// Cancel workflow context.
	workflowCancel()

	// Wait for workflow to finish.
	wg.Wait()

	// Shutdown recovery: clear in-progress flags and running tasks.
	if err := store.ResetInProgress(); err != nil {
		logger.Error("shutdown recovery failed",
			"event_type", "shutdown_recovery_failed",
			"error_hint", "failed to reset in_progress flags on shutdown",
			"error", err,
		)
	}
	if err := store.ResetRunningTasks(); err != nil {
		logger.Error("shutdown recovery failed",
			"event_type", "shutdown_recovery_failed",
			"error_hint", "failed to reset running tasks on shutdown",
			"error", err,
		)
	}

	// Shutdown HTTP API.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := api.Shutdown(shutdownCtx); err != nil {
		logger.Error("api shutdown failed",
			"event_type", "api_shutdown_failed",
			"error_hint", "HTTP API shutdown returned error",
			"error", err,
		)
	}

	// Clean up socket.
	_ = os.Remove(cfg.SocketPath())

	logger.Info("daemon stopped")
	return lock.Unlock()
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
