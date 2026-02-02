package organizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/jellyfin"
	"spindle/internal/stage"
)

// MetadataProvider describes the media metadata used for organization.
type MetadataProvider interface {
	GetLibraryPath(root, moviesDir, tvDir string) string
	GetFilename() string
	IsMovie() bool
	Title() string
	GetEdition() string
}

// Organizer moves encoded files into the final library location.
type Organizer struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *slog.Logger
	jellyfin jellyfin.Service
	notifier notifications.Service
}

const (
	minOrganizedFileSizeBytes = 5 * 1024 * 1024
)

var organizerProbe = ffprobe.Inspect

// NewOrganizer constructs the organizer stage handler using default dependencies.
func NewOrganizer(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Organizer {
	jellyfinService := jellyfin.NewConfiguredService(cfg)
	return NewOrganizerWithDependencies(cfg, store, logger, jellyfinService, notifications.NewService(cfg))
}

// NewOrganizerWithDependencies allows injecting collaborators (used in tests).
func NewOrganizerWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, jellyfinClient jellyfin.Service, notifier notifications.Service) *Organizer {
	org := &Organizer{store: store, cfg: cfg, jellyfin: jellyfinClient, notifier: notifier}
	org.SetLogger(logger)
	return org
}

// SetLogger updates the organizer's logging destination while preserving component labeling.
func (o *Organizer) SetLogger(logger *slog.Logger) {
	o.logger = logging.NewComponentLogger(logger, "organizer")
}

// Prepare initializes progress for the organizing stage.
func (o *Organizer) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, o.logger)
	item.InitProgress("Organizing", "Preparing library organization")
	logger.Debug("starting organization preparation")
	return nil
}

// Execute organizes encoded files into the library.
func (o *Organizer) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, o.logger)
	stageStart := time.Now()

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil {
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"parse rip spec",
			"Rip specification missing or invalid; rerun identification",
			err,
		)
	}

	logger.Debug("starting organization")

	// Cross-stage validation: check for missing encoded episodes
	if missing := env.MissingEpisodes("encoded"); len(missing) > 0 {
		logger.Warn("missing encoded episodes at organizer start",
			logging.Int("missing_count", len(missing)),
			logging.String("missing_episodes", strings.Join(missing, ",")),
			logging.String(logging.FieldEventType, "organizer_missing_encoded"),
			logging.String(logging.FieldErrorHint, "some episodes failed encoding"),
		)
		item.NeedsReview = true
		if item.ReviewReason == "" {
			item.ReviewReason = fmt.Sprintf("missing %d encoded episode(s)", len(missing))
		}
	}

	encodedSources := collectEncodedSources(item, &env)
	if len(encodedSources) == 0 {
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate inputs",
			"No encoded file present for organization; run encoding before organizing or check staging_dir permissions",
			nil,
		)
	}

	// Check if item needs manual review
	if item.NeedsReview {
		logReviewDecision(logger, "review", "needs_review_flag")
		logger.Debug("routing item to manual review", logging.String("reason", strings.TrimSpace(item.ReviewReason)))
		return o.finishReview(ctx, item, stageStart, strings.TrimSpace(item.ReviewReason), encodedSources, nil)
	}
	logReviewDecision(logger, "organize", "ready_for_organize")

	// Resolve metadata
	meta, err := o.resolveMetadata(ctx, item, logger)
	if err != nil {
		return err
	}

	// Build organize jobs for episodes
	jobs, err := buildOrganizeJobs(env, queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle))
	if err != nil {
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"plan tv organization",
			"Unable to map encoded episodes to library destinations",
			err,
		)
	}

	// Log job plan
	attrs := []logging.Attr{
		logging.String(logging.FieldDecisionType, "organizer_job_plan"),
		logging.String("decision_result", logging.Ternary(len(jobs) > 0, "episodes", "single_file")),
		logging.String("decision_reason", logging.Ternary(len(jobs) > 0, "episode_assets", "single_media_asset")),
		logging.String("decision_options", "episodes, single_file"),
		logging.Int("job_count", len(jobs)),
	}
	attrs = appendOrganizeJobLines(attrs, jobs)
	logger.Info("organizer job plan", logging.Args(attrs...)...)

	// Route to episode or single-file organization
	if len(jobs) > 0 {
		return o.organizeEpisodes(ctx, item, &env, jobs, logger, stageStart)
	}
	return o.organizeToLibrary(ctx, item, meta, stageStart, &env)
}

// resolveMetadata resolves and validates metadata for organization.
func (o *Organizer) resolveMetadata(ctx context.Context, item *queue.Item, logger *slog.Logger) (MetadataProvider, error) {
	meta := MetadataProvider(queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle))

	if item.MetadataJSON == "" || meta.Title() == "" {
		fallbackTitle := item.DiscTitle
		if fallbackTitle == "" {
			base := strings.TrimSpace(filepath.Base(item.EncodedFile))
			fallbackTitle = strings.TrimSuffix(base, filepath.Ext(base))
		}
		fallbackReason := logging.Ternary(item.MetadataJSON == "", "metadata_missing", "title_missing")
		logger.Info(
			"metadata selection decision",
			logging.String(logging.FieldDecisionType, "metadata_fallback"),
			logging.String("decision_result", "fallback_metadata"),
			logging.String("decision_reason", fallbackReason),
			logging.String("decision_options", "metadata, fallback"),
			logging.String("fallback_title", strings.TrimSpace(fallbackTitle)),
		)
		basic := queue.NewBasicMetadata(fallbackTitle, true)
		encoded, err := json.Marshal(basic)
		if err != nil {
			return nil, services.Wrap(services.ErrTransient, "organizing", "encode metadata", "Failed to encode fallback metadata", err)
		}
		item.MetadataJSON = string(encoded)
		meta = basic
		if err := o.store.Update(ctx, item); err != nil {
			o.logger.Warn("failed to persist fallback metadata; organizer may re-evaluate defaults",
				logging.Error(err),
				logging.String(logging.FieldEventType, "metadata_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
				logging.String(logging.FieldImpact, "metadata may be regenerated on retry"),
			)
		}
	}
	return meta, nil
}

// moveGeneratedSubtitles moves subtitle sidecars to the library.
// Returns the count of moved subtitle files.
func (o *Organizer) moveGeneratedSubtitles(ctx context.Context, item *queue.Item, targetPath string) (int, error) {
	if item == nil {
		return 0, nil
	}
	encodedPath := strings.TrimSpace(item.EncodedFile)
	if encodedPath == "" {
		return 0, nil
	}
	stagingDir := filepath.Dir(encodedPath)
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return 0, fmt.Errorf("enumerate staging dir: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(encodedPath), filepath.Ext(encodedPath))
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	}
	destBase := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	destDir := filepath.Dir(targetPath)

	moved := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".srt") {
			continue
		}
		prefix := base + "."
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := name[len(prefix):]
		if suffix == "" {
			continue
		}
		source := filepath.Join(stagingDir, name)
		destination := filepath.Join(destDir, fmt.Sprintf("%s.%s", destBase, suffix))
		if o.cfg != nil && o.cfg.Library.OverwriteExisting {
			if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				return moved, fmt.Errorf("remove existing subtitle %q: %w", destination, err)
			}
		}
		if err := jellyfin.FileMover(source, destination); err != nil {
			return moved, fmt.Errorf("move subtitle %q: %w", name, err)
		}
		moved++
	}
	if o.logger != nil {
		o.logger.Info("subtitle sidecar move decision",
			logging.String(logging.FieldDecisionType, "subtitle_sidecar_move"),
			logging.String("decision_result", logging.Ternary(moved > 0, "moved", "none_found")),
			logging.String("decision_reason", logging.Ternary(moved > 0, "subtitles_found", "no_matching_srt_files")),
			logging.Int("count", moved),
			logging.String("destination", destDir),
		)
	}
	return moved, nil
}

// publishCompletionNotifications sends organization and processing completion events.
func (o *Organizer) publishCompletionNotifications(ctx context.Context, logger *slog.Logger, title, finalPath string, jellyfinRefreshed bool, episodeCount, failedCount int) {
	if o.notifier == nil {
		return
	}
	payload := notifications.Payload{
		"mediaTitle":        title,
		"finalFile":         filepath.Base(finalPath),
		"jellyfinRefreshed": jellyfinRefreshed,
	}
	if episodeCount > 0 {
		payload["episodeCount"] = episodeCount
		payload["failedCount"] = failedCount
	}
	if err := o.notifier.Publish(ctx, notifications.EventOrganizationCompleted, payload); err != nil {
		logger.Warn("organization notifier failed; completion alert skipped",
			logging.Error(err),
			logging.String(logging.FieldEventType, "notify_failed"),
			logging.String(logging.FieldErrorHint, "check ntfy_topic configuration"),
			logging.String(logging.FieldImpact, "user will not receive completion notification"),
		)
	}
	if episodeCount == 0 {
		if err := o.notifier.Publish(ctx, notifications.EventProcessingCompleted, notifications.Payload{"title": title}); err != nil {
			logger.Warn("processing completion notifier failed; completion alert skipped",
				logging.Error(err),
				logging.String(logging.FieldEventType, "notify_failed"),
				logging.String(logging.FieldErrorHint, "check ntfy_topic configuration"),
				logging.String(logging.FieldImpact, "user will not receive completion notification"),
			)
		}
	}
}

// HealthCheck verifies organizer prerequisites such as library paths and Jellyfin connectivity configuration.
func (o *Organizer) HealthCheck(ctx context.Context) stage.Health {
	const name = "organizer"
	if o.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(o.cfg.Paths.LibraryDir) == "" {
		return stage.Unhealthy(name, "library directory not configured")
	}
	if strings.TrimSpace(o.cfg.Library.MoviesDir) == "" && strings.TrimSpace(o.cfg.Library.TVDir) == "" {
		return stage.Unhealthy(name, "library subdirectories not configured")
	}
	if o.jellyfin == nil {
		return stage.Unhealthy(name, "jellyfin client unavailable")
	}
	return stage.Healthy(name)
}
