// Package daemonrun provides the daemon runtime entry point, wiring together
// all services, stage handlers, and the workflow manager.
package daemonrun

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/daemon"
	"github.com/five82/spindle/internal/discidcache"
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
	logger := slog.Default()

	// Open queue database.
	store, err := queue.Open(cfg.QueueDBPath())
	if err != nil {
		return fmt.Errorf("open queue: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Create clients.
	tmdbClient := tmdb.New(cfg.TMDB.APIKey, cfg.TMDB.BaseURL, cfg.TMDB.Language)
	llmClient := llm.New(cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLM.Referer, cfg.LLM.Title, cfg.LLM.TimeoutSeconds)
	notifier := notify.New(cfg.Notifications.NtfyTopic, cfg.Notifications.RequestTimeout)
	jfClient := jellyfin.New(cfg.Jellyfin.URL, cfg.Jellyfin.APIKey)
	osClient := opensubtitles.New(cfg.Subtitles.OpenSubtitlesAPIKey, cfg.Subtitles.OpenSubtitlesUserAgent, cfg.Subtitles.OpenSubtitlesUserToken, "")

	// Optional services.
	var discIDStore *discidcache.Store
	if cfg.DiscIDCache.Enabled {
		discIDStore, err = discidcache.Open(cfg.DiscIDCachePath())
		if err != nil {
			logger.Warn("disc ID cache unavailable", "error", err)
		}
	}

	var keydbCat *keydb.Catalog
	keydbCat, _ = keydb.LoadFromFile(cfg.MakeMKV.KeyDBPath)

	var ripCacheStore *ripcache.Store
	if cfg.RipCache.Enabled {
		ripCacheStore = ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
	}

	transcriber := transcription.New(cfg.Subtitles.WhisperXModel, cfg.Subtitles.WhisperXCUDAEnabled, cfg.Subtitles.WhisperXVADMethod, cfg.Subtitles.WhisperXHFToken, cfg.WhisperXCacheDir())

	// Create stage handlers.
	identifyHandler := identify.New(cfg, store, tmdbClient, llmClient, notifier, discIDStore, keydbCat)
	ripperHandler := ripper.New(cfg, store, notifier, ripCacheStore)
	contentidHandler := contentid.New(cfg, store, llmClient, osClient, transcriber)
	encoderHandler := encoder.New(cfg, store, notifier)
	audioHandler := audioanalysis.New(cfg, store, llmClient, transcriber)
	subtitleHandler := subtitle.New(cfg, store, osClient, transcriber)
	organizerHandler := organizer.New(cfg, store, jfClient, notifier)

	// Create workflow manager and configure stages.
	manager := workflow.New(store, notifier, logger)
	manager.ConfigureStages([]workflow.PipelineStage{
		{Name: "identification", Handler: identifyHandler, Stage: queue.StagePending, Semaphore: workflow.SemDisc},
		{Name: "ripping", Handler: ripperHandler, Stage: queue.StageIdentification, Semaphore: workflow.SemDisc},
		{Name: "episode_identification", Handler: contentidHandler, Stage: queue.StageRipping, Semaphore: workflow.SemWhisperX},
		{Name: "encoding", Handler: encoderHandler, Stage: queue.StageEpisodeIdentification, Semaphore: workflow.SemEncode},
		{Name: "audio_analysis", Handler: audioHandler, Stage: queue.StageEncoding, Semaphore: workflow.SemWhisperX},
		{Name: "subtitling", Handler: subtitleHandler, Stage: queue.StageAudioAnalysis, Semaphore: workflow.SemWhisperX},
		{Name: "organizing", Handler: organizerHandler, Stage: queue.StageSubtitling, Semaphore: workflow.SemNone},
	})

	// Create HTTP API.
	api := httpapi.New(store, cfg.API.Token, logger)

	// Create and start daemon.
	d := daemon.New(cfg, store, manager, api, logger)
	if err := d.Start(ctx); err != nil {
		return err
	}

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("received signal", "signal", sig)
	case <-ctx.Done():
	}

	d.Stop()
	return d.Close()
}
