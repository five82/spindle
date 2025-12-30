package ripping

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/makemkv"
	"spindle/internal/stage"
)

// Ripper manages the MakeMKV ripping workflow.
type Ripper struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	client   makemkv.Ripper
	notifier notifications.Service
	cache    *ripcache.Manager
}

// NewRipper constructs the ripping handler using default dependencies.
func NewRipper(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Ripper {
	client, err := makemkv.New(cfg.MakemkvBinary(), cfg.MakeMKV.RipTimeout)
	if err != nil {
		logger.Warn("makemkv client unavailable; ripping disabled",
			logging.Error(err),
			logging.String(logging.FieldEventType, "makemkv_unavailable"),
			logging.String(logging.FieldErrorHint, "check makemkv_binary and license configuration"),
		)
	}
	return NewRipperWithDependencies(cfg, store, logger, client, notifications.NewService(cfg))
}

// NewRipperWithDependencies allows injecting all collaborators (used in tests).
func NewRipperWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client makemkv.Ripper, notifier notifications.Service) *Ripper {
	rip := &Ripper{
		store:    store,
		cfg:      cfg,
		client:   client,
		notifier: notifier,
		cache:    ripcache.NewManager(cfg, logger),
	}
	rip.SetLogger(logger)
	return rip
}

// SetLogger updates the ripper's logging destination while preserving component labeling.
func (r *Ripper) SetLogger(logger *slog.Logger) {
	r.logger = logging.NewComponentLogger(logger, "ripper")
	if r.cache != nil {
		r.cache.SetLogger(logger)
	}
}

func (r *Ripper) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	item.InitProgress("Ripping", "Starting rip")
	logger.Debug("starting rip preparation")
	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipStarted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Debug("failed to send rip start notification", logging.Error(err))
		}
	}
	return nil
}

func (r *Ripper) Execute(ctx context.Context, item *queue.Item) (err error) {
	logger := logging.WithContext(ctx, r.logger)
	startedAt := time.Now()
	var cacheCleanup string
	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"parse rip spec",
			"Rip specification missing or invalid; rerun identification",
			err,
		)
	}
	hasEpisodes := len(env.Episodes) > 0
	var target string
	var destDir string
	// MakeMKV can emit progress events very frequently; persisting them too often
	// causes unnecessary SQLite churn while providing little UX value. Keep the
	// TUI feeling responsive without hammering the queue DB.
	const progressInterval = 5 * time.Second
	var lastPersisted time.Time
	lastStage := item.ProgressStage
	lastMessage := item.ProgressMessage
	lastPercent := item.ProgressPercent
	progressSampler := logging.NewProgressSampler(5)
	rippedSignature := func(list []ripspec.Asset) string {
		if len(list) == 0 {
			return ""
		}
		var b strings.Builder
		b.Grow(len(list) * 64)
		for _, asset := range list {
			key := strings.ToLower(strings.TrimSpace(asset.EpisodeKey))
			path := strings.TrimSpace(asset.Path)
			if key == "" && path == "" {
				continue
			}
			b.WriteString(key)
			b.WriteByte('=')
			b.WriteString(path)
			b.WriteByte('#')
			b.WriteString(strconv.Itoa(asset.TitleID))
			b.WriteByte('|')
		}
		return b.String()
	}
	lastRippedSignature := rippedSignature(env.Assets.Ripped)
	persistRipSpecIfNeeded := func() {
		if !hasEpisodes || strings.TrimSpace(destDir) == "" {
			return
		}
		before := lastRippedSignature
		assignEpisodeAssets(&env, destDir, logger)
		after := rippedSignature(env.Assets.Ripped)
		if after == before {
			return
		}
		encoded, encodeErr := env.Encode()
		if encodeErr != nil {
			logger.Warn("failed to encode rip spec after episode rip; progress metadata stale",
				logging.Error(encodeErr),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if episode mapping looks wrong"),
			)
			return
		}
		copy := *item
		copy.RipSpecData = encoded
		if updateErr := r.store.Update(ctx, &copy); updateErr != nil {
			logger.Warn("failed to persist rip spec after episode rip; progress metadata stale",
				logging.Error(updateErr),
				logging.String(logging.FieldEventType, "rip_spec_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
			)
			return
		}
		*item = copy
		lastRippedSignature = after
	}
	progressCB := func(update makemkv.ProgressUpdate) {
		now := time.Now()
		if update.Percent >= 100 && lastPercent < 95 {
			return
		}
		stageChanged := update.Stage != "" && update.Stage != lastStage
		messageChanged := update.Message != "" && update.Message != lastMessage
		percentReached := update.Percent >= 100 && lastPercent < 100
		intervalElapsed := lastPersisted.IsZero() || now.Sub(lastPersisted) >= progressInterval
		isProgressMessage := strings.HasPrefix(update.Message, "Progress ")
		allow := stageChanged || percentReached || intervalElapsed
		if messageChanged && !isProgressMessage {
			allow = true
		}
		if !allow {
			return
		}
		r.applyProgress(ctx, item, update, progressSampler)
		if percentReached {
			persistRipSpecIfNeeded()
		}
		lastPersisted = now
		if update.Stage != "" {
			lastStage = update.Stage
		}
		if update.Message != "" {
			lastMessage = update.Message
		}
		if update.Percent >= 0 {
			lastPercent = update.Percent
		}
	}
	stagingRoot := item.StagingRoot(r.cfg.Paths.StagingDir)
	if stagingRoot == "" {
		stagingRoot = filepath.Join(strings.TrimSpace(r.cfg.Paths.StagingDir), fmt.Sprintf("queue-%d", item.ID))
	}
	workingDir := filepath.Join(stagingRoot, "rips")

	fingerprintAvailable := hasDiscFingerprint(item)
	if !fingerprintAvailable {
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"verify disc fingerprint",
			"Disc fingerprint missing before ripping; rerun identification to capture scanner output",
			nil,
		)
	}
	useCache := r.cache != nil
	destDir = workingDir
	if useCache {
		destDir = r.cache.Path(item)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		contextLabel := "staging dir"
		message := "Failed to create staging directory; set staging_dir to a writable location"
		if useCache {
			contextLabel = "cache dir"
			message = "Failed to create rip cache directory; check rip_cache_dir permissions"
		}
		return services.Wrap(
			services.ErrConfiguration,
			"ripping",
			"ensure "+contextLabel,
			message,
			err,
		)
	}

	cacheUsed := false
	var cacheStatus string // "hit", "miss", "invalid", "error", or ""
	if useCache && existsNonEmptyDir(destDir) {
		cachedTarget, err := selectCachedRip(destDir)
		if err != nil {
			cacheStatus = "error"
			logger.Warn("cache inspection failed; falling back to MakeMKV",
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_cache_inspection_failed"),
				logging.String(logging.FieldErrorHint, "check rip_cache_dir permissions or disable rip_cache_enabled"),
			)
		} else if cachedTarget != "" {
			if err := r.validateRippedArtifact(ctx, item, cachedTarget, startedAt); err == nil {
				target = cachedTarget
				cacheUsed = true
				cacheStatus = "hit"
				logger.Debug(
					"rip cache hit; skipping makemkv rip",
					logging.Bool("cache_used", true),
					logging.String("cache_decision", "hit"),
					logging.String("rip_dir", destDir),
					logging.String("ripped_file", cachedTarget),
				)
				copy := *item
				copy.ProgressMessage = "Rip cache hit; skipping MakeMKV rip"
				if err := r.store.UpdateProgress(ctx, &copy); err != nil {
					logger.Warn("failed to persist rip cache hit progress; queue status may lag",
						logging.Error(err),
						logging.String(logging.FieldEventType, "queue_progress_persist_failed"),
						logging.String(logging.FieldErrorHint, "check queue database access"),
					)
				} else {
					*item = copy
				}
			} else {
				cacheStatus = "invalid"
				logger.Debug("cached rip failed validation", logging.Error(err))
				_ = os.RemoveAll(destDir)
				if mkErr := os.MkdirAll(destDir, 0o755); mkErr != nil {
					return services.Wrap(
						services.ErrConfiguration,
						"ripping",
						"ensure cache dir",
						"Failed to recreate rip cache directory after pruning invalid entry",
						mkErr,
					)
				}
			}
		} else {
			cacheStatus = "miss"
		}
	} else if useCache {
		cacheStatus = "miss"
	}
	if useCache && cacheStatus != "hit" {
		cacheCleanup = destDir
	}
	defer func() {
		if cacheCleanup == "" {
			return
		}
		if err != nil {
			_ = os.RemoveAll(cacheCleanup)
		}
	}()
	logger.Debug("starting rip execution")

	var titleIDs []int
	var makemkvDuration time.Duration
	if !cacheUsed && r.client != nil {
		if err := ensureMakeMKVSelectionRule(); err != nil {
			logger.Error(
				"failed to configure makemkv selection; ripping aborted",
				logging.Error(err),
				logging.String(logging.FieldEventType, "makemkv_config_failed"),
				logging.String(logging.FieldErrorHint, "ensure Spindle can write to ~/.MakeMKV"),
			)
			return services.Wrap(
				services.ErrConfiguration,
				"ripping",
				"configure makemkv",
				"Failed to configure MakeMKV audio selection; ensure Spindle can write to ~/.MakeMKV",
				err,
			)
		}
		titleIDs = r.selectTitleIDs(item, logger)
		logger.Debug(
			"launching makemkv rip",
			logging.String("destination", destDir),
			logging.Any("title_ids", titleIDs),
			logging.Int("title_count", len(titleIDs)),
		)
		makemkvStart := time.Now()
		path, err := r.client.Rip(ctx, item.DiscTitle, item.SourcePath, destDir, titleIDs, progressCB)
		makemkvDuration = time.Since(makemkvStart)
		if err != nil {
			logger.Error("makemkv rip failed",
				logging.Error(err),
				logging.Duration("makemkv_duration", makemkvDuration),
				logging.Any("title_ids", titleIDs),
				logging.String(logging.FieldEventType, "makemkv_rip_failed"),
				logging.String(logging.FieldErrorHint, "check disc readability and MakeMKV logs"),
			)
			return services.Wrap(
				services.ErrExternalTool,
				"ripping",
				"makemkv rip",
				"MakeMKV rip failed; check MakeMKV installation and disc readability",
				err,
			)
		}
		target = path
		// Get ripped file size for resource tracking
		var rippedSize int64
		if info, statErr := os.Stat(target); statErr == nil {
			rippedSize = info.Size()
		}
		logger.Debug("makemkv rip finished",
			logging.Duration("duration", makemkvDuration),
			logging.Int64("size_bytes", rippedSize))
	}

	if target == "" {
		sourcePath := strings.TrimSpace(item.SourcePath)
		if sourcePath == "" {
			logger.Error(
				"ripping validation failed",
				logging.String("reason", "no rip output"),
				logging.Bool("makemkv_available", r.client != nil),
			)
			return services.Wrap(
				services.ErrValidation,
				"ripping",
				"resolve rip output",
				"No ripped artifact produced and no source path available for fallback",
				nil,
			)
		}
		cleaned := sanitizeFileName(item.DiscTitle)
		if cleaned == "" {
			cleaned = strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
			if cleaned == "" {
				cleaned = "spindle-disc"
			}
		}
		ext := filepath.Ext(sourcePath)
		if ext == "" {
			ext = ".mkv"
		}
		target = filepath.Join(destDir, cleaned+ext)
		if err := copyPlaceholder(sourcePath, target); err != nil {
			return services.Wrap(services.ErrTransient, "ripping", "stage source", "Failed to copy source into staging", err)
		}
		logger.Debug(
			"copied source into rip staging",
			logging.String("source_file", sourcePath),
			logging.String("ripped_file", target),
		)
	}
	if useCache && destDir != workingDir {
		if err := refreshWorkingCopy(destDir, workingDir); err != nil {
			return services.Wrap(
				services.ErrTransient,
				"ripping",
				"refresh working copy",
				"Failed to copy raw rip into staging; check disk space and permissions",
				err,
			)
		}
		if strings.TrimSpace(target) != "" {
			target = mapToWorkingPath(target, destDir, workingDir)
		}
	}

	validationTargets := []string{}
	if strings.TrimSpace(target) != "" {
		validationTargets = append(validationTargets, target)
	}
	specDirty := false
	if hasEpisodes {
		assigned := assignEpisodeAssets(&env, workingDir, logger)
		if assigned == 0 {
			logger.Warn("episode asset mapping incomplete; episode-level ripping may be missing",
				logging.String("destination", workingDir),
				logging.String(logging.FieldEventType, "episode_asset_mapping_incomplete"),
				logging.String(logging.FieldErrorHint, "verify rip outputs and episode title IDs"),
			)
		} else {
			specDirty = true
			paths := episodeAssetPaths(env)
			if len(paths) > 0 {
				validationTargets = paths
				target = paths[0]
			}
		}
	}

	if err := RefineAudioTargets(ctx, r.cfg, r.logger, validationTargets); err != nil {
		return services.Wrap(
			services.ErrExternalTool,
			"ripping",
			"refine audio tracks",
			"Failed to optimize ripped audio tracks with ffmpeg",
			err,
		)
	}
	if specDirty {
		if encoded, encodeErr := env.Encode(); encodeErr == nil {
			item.RipSpecData = encoded
		} else {
			logger.Warn("failed to encode rip spec after ripping; metadata may be stale",
				logging.Error(encodeErr),
				logging.String(logging.FieldEventType, "rip_spec_encode_failed"),
				logging.String(logging.FieldErrorHint, "rerun identification if rip spec data looks wrong"),
			)
		}
	}
	visited := make(map[string]struct{}, len(validationTargets))
	for _, path := range validationTargets {
		clean := strings.TrimSpace(path)
		if clean == "" {
			continue
		}
		if _, ok := visited[clean]; ok {
			continue
		}
		visited[clean] = struct{}{}
		if err := r.validateRippedArtifact(ctx, item, clean, startedAt); err != nil {
			return err
		}
	}
	if len(validationTargets) == 0 {
		if err := r.validateRippedArtifact(ctx, item, target, startedAt); err != nil {
			return err
		}
	}

	if useCache {
		if err := r.cache.Register(ctx, item, destDir); err != nil {
			return services.Wrap(
				services.ErrConfiguration,
				"ripping",
				"rip cache register",
				"Failed to register rip cache entry; free space may be insufficient",
				err,
			)
		} else {
			cacheCleanup = ""
		}
	}

	item.RippedFile = target
	item.ProgressStage = "Ripped"
	item.ProgressPercent = 100
	if cacheUsed {
		item.ProgressMessage = "Rip cache hit; MakeMKV rip skipped"
	} else {
		item.ProgressMessage = "Disc content ripped"
	}

	// Log stage summary with timing and resource metrics
	var totalRippedBytes int64
	if info, statErr := os.Stat(target); statErr == nil {
		totalRippedBytes = info.Size()
	}
	summaryAttrs := []logging.Attr{
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.Duration("stage_duration", time.Since(startedAt)),
		logging.Int64("total_ripped_bytes", totalRippedBytes),
		logging.Int("titles_ripped", len(titleIDs)),
	}
	if cacheStatus != "" {
		summaryAttrs = append(summaryAttrs, logging.String("cache_decision", cacheStatus))
		summaryAttrs = append(summaryAttrs, logging.Bool("cache_used", cacheStatus == "hit"))
	}
	if makemkvDuration > 0 {
		summaryAttrs = append(summaryAttrs, logging.Duration("rip_time", makemkvDuration))
	}
	logger.Info("ripping stage summary", logging.Args(summaryAttrs...)...)

	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipCompleted, notifications.Payload{
			"discTitle": item.DiscTitle,
			"duration":  time.Since(startedAt),
			"bytes":     totalRippedBytes,
			"cache":     cacheStatus,
		}); err != nil {
			logger.Debug("rip completion notification failed", logging.Error(err))
		}
	}

	return nil
}

func hasDiscFingerprint(item *queue.Item) bool {
	if item == nil {
		return false
	}
	return strings.TrimSpace(item.DiscFingerprint) != ""
}

// HealthCheck verifies MakeMKV ripping dependencies.
func (r *Ripper) HealthCheck(ctx context.Context) stage.Health {
	const name = "ripper"
	if r.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(r.cfg.Paths.StagingDir) == "" {
		return stage.Unhealthy(name, "staging directory not configured")
	}
	if strings.TrimSpace(r.cfg.MakeMKV.OpticalDrive) == "" {
		return stage.Unhealthy(name, "optical drive not configured")
	}
	if r.client == nil {
		return stage.Unhealthy(name, "makemkv client unavailable")
	}
	binary := strings.TrimSpace(r.cfg.MakemkvBinary())
	if binary == "" {
		return stage.Unhealthy(name, "makemkv binary not configured")
	}
	if _, err := exec.LookPath(binary); err != nil {
		return stage.Unhealthy(name, fmt.Sprintf("makemkv binary %q not found", binary))
	}
	return stage.Healthy(name)
}

func (r *Ripper) applyProgress(ctx context.Context, item *queue.Item, update makemkv.ProgressUpdate, sampler *logging.ProgressSampler) {
	logger := logging.WithContext(ctx, r.logger)
	copy := *item
	if update.Stage != "" {
		copy.ProgressStage = update.Stage
	}
	if update.Percent >= 0 {
		copy.ProgressPercent = update.Percent
	}
	if update.Message != "" {
		copy.ProgressMessage = update.Message
	}
	if err := r.store.UpdateProgress(ctx, &copy); err != nil {
		logger.Warn("failed to persist progress; queue status may lag",
			logging.Error(err),
			logging.String(logging.FieldEventType, "queue_progress_persist_failed"),
			logging.String(logging.FieldErrorHint, "check queue database access"),
		)
		return
	}
	progressMessage := strings.TrimSpace(copy.ProgressMessage)
	if strings.HasPrefix(progressMessage, "Progress ") {
		progressMessage = ""
	}
	shouldLog := sampler == nil || sampler.ShouldLog(copy.ProgressPercent, copy.ProgressStage, progressMessage)
	if !shouldLog {
		*item = copy
		return
	}
	fields := []any{
		logging.Float64(logging.FieldProgressPercent, math.Round(copy.ProgressPercent)),
	}
	if stage := strings.TrimSpace(copy.ProgressStage); stage != "" {
		fields = append(fields, logging.String(logging.FieldProgressStage, stage))
	}
	if progressMessage != "" {
		fields = append(fields, logging.String(logging.FieldProgressMessage, progressMessage))
	}
	logger.Debug("makemkv progress", fields...)
	*item = copy
}
