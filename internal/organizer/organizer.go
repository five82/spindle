package organizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"go.uber.org/zap"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/plex"
	"spindle/internal/stage"
)

// MetadataProvider describes the media metadata used for organization.
type MetadataProvider interface {
	GetLibraryPath(root, moviesDir, tvDir string) string
	GetFilename() string
	IsMovie() bool
	Title() string
}

// Organizer moves encoded files into the final library location.
type Organizer struct {
	store    *queue.Store
	cfg      *config.Config
	logger   *zap.Logger
	plex     plex.Service
	notifier notifications.Service
}

// NewOrganizer constructs the organizer stage handler using default dependencies.
func NewOrganizer(cfg *config.Config, store *queue.Store, logger *zap.Logger) *Organizer {
	plexService := plex.NewConfiguredService(cfg)
	return NewOrganizerWithDependencies(cfg, store, logger, plexService, notifications.NewService(cfg))
}

// NewOrganizerWithDependencies allows injecting collaborators (used in tests).
func NewOrganizerWithDependencies(cfg *config.Config, store *queue.Store, logger *zap.Logger, plexClient plex.Service, notifier notifications.Service) *Organizer {
	stageLogger := logger
	if stageLogger != nil {
		stageLogger = stageLogger.With(zap.String("component", "organizer"))
	}
	return &Organizer{store: store, cfg: cfg, logger: stageLogger, plex: plexClient, notifier: notifier}
}

func (o *Organizer) Prepare(ctx context.Context, item *queue.Item) error {
	if item.ProgressStage == "" {
		item.ProgressStage = "Organizing"
	}
	item.ProgressMessage = "Preparing library organization"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	return nil
}

func (o *Organizer) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, o.logger)
	if item.EncodedFile == "" {
		return services.WithHint(
			services.Wrap(services.ErrorValidation, "organizing", "validate inputs", "No encoded file present for organization", nil),
			"Run encoding before organizing or check staging_dir permissions",
		)
	}
	if item.NeedsReview {
		reviewPath, err := o.moveToReview(ctx, item)
		if err != nil {
			return err
		}
		item.FinalFile = reviewPath
		item.EncodedFile = reviewPath
		item.Status = queue.StatusCompleted
		item.ProgressStage = "Manual review"
		item.ProgressPercent = 100
		item.ProgressMessage = fmt.Sprintf("Moved to review directory: %s", filepath.Base(reviewPath))
		if strings.TrimSpace(item.ErrorMessage) == "" {
			item.ErrorMessage = strings.TrimSpace(item.ReviewReason)
		}
		if o.notifier != nil {
			label := filepath.Base(reviewPath)
			if err := o.notifier.NotifyUnidentifiedMedia(ctx, label); err != nil {
				logger.Warn("review notification failed", zap.Error(err))
			}
		}
		return nil
	}
	var meta MetadataProvider
	meta = queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
	if item.MetadataJSON == "" || meta.Title() == "" {
		fallbackTitle := item.DiscTitle
		if fallbackTitle == "" {
			base := strings.TrimSpace(filepath.Base(item.EncodedFile))
			fallbackTitle = strings.TrimSuffix(base, filepath.Ext(base))
		}
		basic := queue.NewBasicMetadata(fallbackTitle, true)
		encoded, err := json.Marshal(basic)
		if err != nil {
			return services.Wrap(services.ErrorTransient, "organizing", "encode metadata", "Failed to encode fallback metadata", err)
		}
		item.MetadataJSON = string(encoded)
		meta = basic
		if err := o.store.Update(ctx, item); err != nil {
			o.logger.Warn("failed to persist fallback metadata", zap.Error(err))
		}
	}

	o.updateProgress(ctx, item, "Organizing library structure", 20)
	targetPath, err := o.plex.Organize(ctx, item.EncodedFile, meta)
	if err != nil {
		return services.Wrap(services.ErrorExternalTool, "organizing", "move to library", "Failed to move media into library", err)
	}
	item.FinalFile = targetPath

	o.updateProgress(ctx, item, "Refreshing Plex library", 80)
	if err := o.plex.Refresh(ctx, meta); err != nil {
		logger.Warn("plex refresh failed", zap.Error(err))
	}

	o.updateProgress(ctx, item, "Organization completed", 100)
	item.ProgressMessage = fmt.Sprintf("Available in library: %s", filepath.Base(targetPath))

	if o.notifier != nil {
		title := strings.TrimSpace(meta.Title())
		if title == "" {
			title = strings.TrimSpace(item.DiscTitle)
		}
		if title == "" {
			title = filepath.Base(targetPath)
		}
		if err := o.notifier.NotifyOrganizationCompleted(ctx, title, filepath.Base(targetPath)); err != nil {
			logger.Warn("organization notifier failed", zap.Error(err))
		}
		if err := o.notifier.NotifyProcessingCompleted(ctx, title); err != nil {
			logger.Warn("processing completion notifier failed", zap.Error(err))
		}
	}

	return nil
}

func (o *Organizer) moveToReview(ctx context.Context, item *queue.Item) (string, error) {
	logger := logging.WithContext(ctx, o.logger)
	reviewDir := strings.TrimSpace(o.cfg.ReviewDir)
	if reviewDir == "" {
		return "", services.WithHint(
			services.Wrap(services.ErrorConfiguration, "organizing", "resolve review dir", "Review directory not configured", nil),
			"Set review_dir in your spindle config.toml",
		)
	}
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		return "", services.Wrap(services.ErrorConfiguration, "organizing", "ensure review dir", "Failed to create review directory", err)
	}
	ext := filepath.Ext(item.EncodedFile)
	if ext == "" {
		ext = ".mkv"
	}
	prefix := reviewFilenamePrefix(item)
	target, err := o.nextReviewPath(reviewDir, prefix, ext)
	if err != nil {
		return "", services.Wrap(services.ErrorTransient, "organizing", "allocate review filename", "Unable to allocate review filename", err)
	}
	if renameErr := os.Rename(item.EncodedFile, target); renameErr != nil {
		if errors.Is(renameErr, os.ErrExist) {
			retryTarget, retryErr := o.nextReviewPath(reviewDir, prefix, ext)
			if retryErr != nil {
				return "", services.Wrap(services.ErrorTransient, "organizing", "allocate review filename", "Unable to allocate review filename", retryErr)
			}
			target = retryTarget
			renameErr = os.Rename(item.EncodedFile, target)
		}
		if renameErr != nil {
			var linkErr *os.LinkError
			if errors.As(renameErr, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV) {
				if copyErr := copyFile(item.EncodedFile, target); copyErr != nil {
					return "", services.Wrap(services.ErrorTransient, "organizing", "copy review file", "Failed to copy file into review directory", copyErr)
				}
				if err := os.Remove(item.EncodedFile); err != nil {
					logger.Warn("failed to remove source file after copy", zap.Error(err))
				}
			} else {
				return "", services.Wrap(services.ErrorTransient, "organizing", "move review file", "Failed to move file into review directory", renameErr)
			}
		}
	}
	return target, nil
}

func (o *Organizer) nextReviewPath(dir, prefix, ext string) (string, error) {
	const maxAttempts = 10000
	if strings.TrimSpace(prefix) == "" {
		prefix = "unidentified"
	}
	if ext == "" {
		ext = ".mkv"
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		name := fmt.Sprintf("%s-%d%s", prefix, attempt, ext)
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("exhausted review filename slots in %s", dir)
}

func reviewFilenamePrefix(item *queue.Item) string {
	reason := strings.ToLower(strings.TrimSpace(item.ReviewReason))
	if reason == "" {
		reason = "unidentified"
	}
	slug := strings.Builder{}
	lastHyphen := false
	for _, r := range reason {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
			lastHyphen = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !lastHyphen {
				slug.WriteRune('-')
				lastHyphen = true
			}
		default:
			// drop other runes
		}
	}
	result := strings.Trim(slug.String(), "-")
	if result == "" {
		result = "unidentified"
	}
	fingerprint := strings.ToLower(strings.TrimSpace(item.DiscFingerprint))
	if fingerprint == "" {
		return result
	}
	fpSlug := strings.Builder{}
	for _, r := range fingerprint {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			fpSlug.WriteRune(r)
		default:
			// drop
		}
		if fpSlug.Len() >= 8 {
			break
		}
	}
	fingerprintSegment := strings.Trim(fpSlug.String(), "-")
	if fingerprintSegment == "" {
		return result
	}
	return result + "-" + fingerprintSegment
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return nil
}

// HealthCheck verifies organizer prerequisites such as library paths and Plex connectivity configuration.
func (o *Organizer) HealthCheck(ctx context.Context) stage.Health {
	const name = "organizer"
	if o.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(o.cfg.LibraryDir) == "" {
		return stage.Unhealthy(name, "library directory not configured")
	}
	if strings.TrimSpace(o.cfg.MoviesDir) == "" && strings.TrimSpace(o.cfg.TVDir) == "" {
		return stage.Unhealthy(name, "library subdirectories not configured")
	}
	if o.plex == nil {
		return stage.Unhealthy(name, "plex client unavailable")
	}
	return stage.Healthy(name)
}

func (o *Organizer) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) {
	logger := logging.WithContext(ctx, o.logger)
	copy := *item
	copy.ProgressMessage = message
	copy.ProgressPercent = percent
	if err := o.store.Update(ctx, &copy); err != nil {
		logger.Warn("failed to persist organizer progress", zap.Error(err))
		return
	}
	*item = copy
}
