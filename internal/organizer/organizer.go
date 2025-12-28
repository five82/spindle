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

func (o *Organizer) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, o.logger)
	item.InitProgress("Organizing", "Preparing library organization")
	logger.Debug("starting organization preparation")
	return nil
}

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
	if item.NeedsReview {
		logger.Info("routing item to manual review", logging.String("reason", strings.TrimSpace(item.ReviewReason)))
		return o.finishReview(ctx, item, stageStart, strings.TrimSpace(item.ReviewReason), encodedSources, nil)
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
			return services.Wrap(services.ErrTransient, "organizing", "encode metadata", "Failed to encode fallback metadata", err)
		}
		item.MetadataJSON = string(encoded)
		meta = basic
		if err := o.store.Update(ctx, item); err != nil {
			o.logger.Warn("failed to persist fallback metadata", logging.Error(err))
		}
	}
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
	if len(jobs) > 0 {
		return o.organizeEpisodes(ctx, item, &env, jobs, logger, stageStart)
	}

	o.updateProgress(ctx, item, "Organizing library structure", 20)
	logger.Info("organizing encoded file into library", logging.String("encoded_file", item.EncodedFile))
	targetPath, err := o.jellyfin.Organize(ctx, item.EncodedFile, meta)
	if err != nil {
		if isLibraryUnavailable(err) {
			logger.Warn("library unavailable; moving to review directory", logging.Error(err))
			return o.finishReview(ctx, item, stageStart, "Library unavailable", encodedSources, err)
		}
		return services.Wrap(services.ErrExternalTool, "organizing", "move to library", "Failed to move media into library", err)
	}
	item.FinalFile = targetPath
	logger.Info("library move completed", logging.String("final_file", targetPath))
	if err := o.moveGeneratedSubtitles(ctx, item, targetPath); err != nil {
		logger.Warn("subtitle sidecar move failed", logging.Error(err))
	}
	if err := o.validateOrganizedArtifact(ctx, targetPath, stageStart); err != nil {
		return err
	}

	o.updateProgress(ctx, item, "Refreshing Jellyfin library", 80)
	jellyfinRefreshed := false
	if err := o.jellyfin.Refresh(ctx, meta); err != nil {
		logger.Warn("jellyfin refresh failed", logging.Error(err))
	} else {
		logger.Info("jellyfin library refresh requested", logging.String("title", strings.TrimSpace(meta.Title())))
		jellyfinRefreshed = true
	}

	o.updateProgress(ctx, item, "Organization completed", 100)
	item.ProgressMessage = fmt.Sprintf("Available in library: %s", filepath.Base(targetPath))

	// Calculate resource metrics
	var finalFileSize int64
	if info, err := os.Stat(targetPath); err == nil {
		finalFileSize = info.Size()
	}

	// Log stage summary
	logger.Info(
		"organizing stage summary",
		logging.String("final_file", targetPath),
		logging.Duration("stage_duration", time.Since(stageStart)),
		logging.Int64("final_file_size_bytes", finalFileSize),
		logging.String("media_title", strings.TrimSpace(meta.Title())),
		logging.Bool("is_movie", meta.IsMovie()),
	)

	if o.notifier != nil {
		title := strings.TrimSpace(meta.Title())
		if title == "" {
			title = strings.TrimSpace(item.DiscTitle)
		}
		if title == "" {
			title = filepath.Base(targetPath)
		}
		if err := o.notifier.Publish(ctx, notifications.EventOrganizationCompleted, notifications.Payload{
			"mediaTitle":        title,
			"finalFile":         filepath.Base(targetPath),
			"jellyfinRefreshed": jellyfinRefreshed,
		}); err != nil {
			logger.Warn("organization notifier failed", logging.Error(err))
		}
		if err := o.notifier.Publish(ctx, notifications.EventProcessingCompleted, notifications.Payload{"title": title}); err != nil {
			logger.Warn("processing completion notifier failed", logging.Error(err))
		}
	}

	o.cleanupStaging(ctx, item)
	return nil
}

func (o *Organizer) moveGeneratedSubtitles(ctx context.Context, item *queue.Item, targetPath string) error {
	if item == nil {
		return nil
	}
	encodedPath := strings.TrimSpace(item.EncodedFile)
	if encodedPath == "" {
		return nil
	}
	stagingDir := filepath.Dir(encodedPath)
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return fmt.Errorf("enumerate staging dir: %w", err)
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
				return fmt.Errorf("remove existing subtitle %q: %w", destination, err)
			}
		}
		if err := jellyfin.FileMover(source, destination); err != nil {
			return fmt.Errorf("move subtitle %q: %w", name, err)
		}
		moved++
	}
	if moved > 0 && o.logger != nil {
		o.logger.Info(
			"moved subtitle sidecars",
			logging.Int("count", moved),
			logging.String("destination_dir", destDir),
		)
	}
	return nil
}

type organizeJob struct {
	Episode  ripspec.Episode
	Source   string
	Metadata queue.Metadata
}

func buildOrganizeJobs(env ripspec.Envelope, base queue.Metadata) ([]organizeJob, error) {
	if len(env.Episodes) == 0 {
		return nil, nil
	}
	show := strings.TrimSpace(base.ShowTitle)
	if show == "" {
		show = strings.TrimSpace(base.Title())
	}
	if show == "" {
		show = "Manual Import"
	}
	jobs := make([]organizeJob, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("encoded", episode.Key)
		if !ok || strings.TrimSpace(asset.Path) == "" {
			return nil, fmt.Errorf("missing encoded asset for %s", episode.Key)
		}
		display := fmt.Sprintf("%s Season %02d", show, episode.Season)
		meta := queue.NewTVMetadata(show, episode.Season, []int{episode.Episode}, display)
		jobs = append(jobs, organizeJob{Episode: episode, Source: asset.Path, Metadata: meta})
	}
	return jobs, nil
}

func (o *Organizer) organizeEpisodes(ctx context.Context, item *queue.Item, env *ripspec.Envelope, jobs []organizeJob, logger *slog.Logger, stageStarted time.Time) error {
	finalPaths := make([]string, 0, len(jobs))
	step := 80.0
	if len(jobs) > 0 {
		step = 80.0 / float64(len(jobs))
	}
	for idx, job := range jobs {
		item.ActiveEpisodeKey = strings.ToLower(strings.TrimSpace(job.Episode.Key))
		label := fmt.Sprintf("S%02dE%02d", job.Episode.Season, job.Episode.Episode)
		o.updateProgress(ctx, item, fmt.Sprintf("Organizing %s (%d/%d)", label, idx+1, len(jobs)), step*float64(idx))
		targetPath, err := o.jellyfin.Organize(ctx, job.Source, job.Metadata)
		if err != nil {
			if isLibraryUnavailable(err) {
				logger.Warn("library unavailable; moving to review directory", logging.Error(err))
				return o.finishReview(ctx, item, stageStarted, "Library unavailable", collectEncodedSources(item, env), err)
			}
			return services.Wrap(
				services.ErrExternalTool,
				"organizing",
				"move to library",
				"Failed to move media into library",
				err,
			)
		}
		if env != nil {
			env.Assets.AddAsset("final", ripspec.Asset{EpisodeKey: job.Episode.Key, TitleID: job.Episode.TitleID, Path: targetPath})
			// Persist per-episode progress so API consumers can surface completed
			// episodes while the organizing stage is still running.
			if encoded, err := env.Encode(); err == nil {
				copy := *item
				copy.RipSpecData = encoded
				if err := o.store.Update(ctx, &copy); err != nil {
					logger.Warn("failed to persist rip spec after episode organize", logging.Error(err))
				} else {
					*item = copy
				}
			} else {
				logger.Warn("failed to encode rip spec after episode organize", logging.Error(err))
			}
		}
		if err := o.validateOrganizedArtifact(ctx, targetPath, stageStarted); err != nil {
			return err
		}
		itemCopy := *item
		itemCopy.EncodedFile = job.Source
		if err := o.moveGeneratedSubtitles(ctx, &itemCopy, targetPath); err != nil {
			logger.Warn("subtitle sidecar move failed", logging.Error(err))
		}
		if err := o.jellyfin.Refresh(ctx, job.Metadata); err != nil {
			logger.Warn("jellyfin refresh failed", logging.Error(err))
		}
		finalPaths = append(finalPaths, targetPath)
	}
	if len(finalPaths) == 0 {
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"finalize episodes",
			"No encoded episodes were organized",
			nil,
		)
	}
	if env != nil {
		if encoded, err := env.Encode(); err == nil {
			item.RipSpecData = encoded
		} else {
			logger.Warn("failed to encode rip spec after organizing", logging.Error(err))
		}
	}
	item.FinalFile = finalPaths[len(finalPaths)-1]
	item.ProgressStage = "Organizing"
	item.ProgressPercent = 100
	item.ProgressMessage = fmt.Sprintf("Available in library (%d episodes)", len(finalPaths))
	item.ActiveEpisodeKey = ""
	if o.notifier != nil {
		if err := o.notifier.Publish(ctx, notifications.EventOrganizationCompleted, notifications.Payload{
			"mediaTitle":        strings.TrimSpace(item.DiscTitle),
			"finalFile":         filepath.Base(item.FinalFile),
			"jellyfinRefreshed": true,
		}); err != nil {
			logger.Warn("organization notifier failed", logging.Error(err))
		}
	}
	o.cleanupStaging(ctx, item)
	return nil
}

func (o *Organizer) validateOrganizedArtifact(ctx context.Context, path string, startedAt time.Time) error {
	logger := logging.WithContext(ctx, o.logger)
	clean := strings.TrimSpace(path)
	if clean == "" {
		logger.Error("organizer validation failed", logging.String("reason", "empty path"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			"Organization produced an empty target path",
			nil,
		)
	}
	info, err := os.Stat(clean)
	if err != nil {
		logger.Error("organizer validation failed", logging.String("reason", "stat failure"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			"Failed to stat organized file",
			err,
		)
	}
	if info.IsDir() {
		logger.Error("organizer validation failed", logging.String("reason", "path is directory"), logging.String("final_path", clean))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			"Organized artifact points to a directory",
			nil,
		)
	}
	if info.Size() < minOrganizedFileSizeBytes {
		logger.Error(
			"organizer validation failed",
			logging.String("reason", "file too small"),
			logging.Int64("size_bytes", info.Size()),
		)
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate output",
			fmt.Sprintf("Organized file %q is unexpectedly small (%d bytes)", clean, info.Size()),
			nil,
		)
	}

	binary := "ffprobe"
	if o.cfg != nil {
		binary = o.cfg.FFprobeBinary()
	}
	probe, err := organizerProbe(ctx, binary, clean)
	if err != nil {
		logger.Error("organizer validation failed", logging.String("reason", "ffprobe"), logging.Error(err))
		return services.Wrap(
			services.ErrExternalTool,
			"organizing",
			"ffprobe validation",
			"Failed to inspect organized file with ffprobe",
			err,
		)
	}
	if probe.VideoStreamCount() == 0 {
		logger.Error("organizer validation failed", logging.String("reason", "no video stream"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate video stream",
			"Organized file does not contain a video stream",
			nil,
		)
	}
	if probe.AudioStreamCount() == 0 {
		logger.Error("organizer validation failed", logging.String("reason", "no audio stream"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate audio stream",
			"Organized file does not contain an audio stream",
			nil,
		)
	}
	duration := probe.DurationSeconds()
	if duration <= 0 {
		logger.Error("organizer validation failed", logging.String("reason", "invalid duration"))
		return services.Wrap(
			services.ErrValidation,
			"organizing",
			"validate duration",
			"Organized file duration could not be determined",
			nil,
		)
	}

	logger.Debug(
		"organizer validation succeeded",
		logging.String("final_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.Group("ffprobe",
			logging.Float64("duration_seconds", duration),
			logging.Int("video_streams", probe.VideoStreamCount()),
			logging.Int("audio_streams", probe.AudioStreamCount()),
		),
	)
	return nil
}

func (o *Organizer) cleanupStaging(ctx context.Context, item *queue.Item) {
	if item == nil || o.cfg == nil {
		return
	}
	base := strings.TrimSpace(o.cfg.Paths.StagingDir)
	if base == "" {
		return
	}
	root := strings.TrimSpace(item.StagingRoot(base))
	if root == "" {
		return
	}
	logger := logging.WithContext(ctx, o.logger)
	if err := os.RemoveAll(root); err != nil {
		logger.Warn("failed to clean staging directory", logging.String("staging_root", root), logging.Error(err))
		return
	}
	logger.Info("cleaned staging directory", logging.String("staging_root", root))
}

func (o *Organizer) movePathToReview(ctx context.Context, item *queue.Item, sourcePath string) (string, error) {
	logger := logging.WithContext(ctx, o.logger)
	logger.Info(
		"moving encoded file to review",
		logging.String("encoded_file", strings.TrimSpace(sourcePath)),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
	)
	reviewDir := strings.TrimSpace(o.cfg.Paths.ReviewDir)
	if reviewDir == "" {
		return "", services.Wrap(
			services.ErrConfiguration,
			"organizing",
			"resolve review dir",
			"Review directory not configured; set review_dir in your spindle config.toml",
			nil,
		)
	}
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		return "", services.Wrap(services.ErrConfiguration, "organizing", "ensure review dir", "Failed to create review directory", err)
	}
	ext := filepath.Ext(sourcePath)
	if ext == "" {
		ext = ".mkv"
	}
	prefix := reviewFilenamePrefix(item)
	target, err := o.nextReviewPath(reviewDir, prefix, ext)
	if err != nil {
		return "", services.Wrap(services.ErrTransient, "organizing", "allocate review filename", "Unable to allocate review filename", err)
	}
	if renameErr := os.Rename(sourcePath, target); renameErr != nil {
		if errors.Is(renameErr, os.ErrExist) {
			retryTarget, retryErr := o.nextReviewPath(reviewDir, prefix, ext)
			if retryErr != nil {
				return "", services.Wrap(services.ErrTransient, "organizing", "allocate review filename", "Unable to allocate review filename", retryErr)
			}
			target = retryTarget
			renameErr = os.Rename(sourcePath, target)
		}
		if renameErr != nil {
			var linkErr *os.LinkError
			if errors.As(renameErr, &linkErr) && errors.Is(linkErr.Err, syscall.EXDEV) {
				if copyErr := copyFile(sourcePath, target); copyErr != nil {
					return "", services.Wrap(services.ErrTransient, "organizing", "copy review file", "Failed to copy file into review directory", copyErr)
				}
				if err := os.Remove(sourcePath); err != nil {
					logger.Warn("failed to remove source file after copy", logging.Error(err))
				}
			} else {
				return "", services.Wrap(services.ErrTransient, "organizing", "move review file", "Failed to move file into review directory", renameErr)
			}
		}
	}
	return target, nil
}

func (o *Organizer) finishReview(ctx context.Context, item *queue.Item, stageStart time.Time, reason string, sources []string, detailErr error) error {
	if item == nil {
		return services.Wrap(services.ErrValidation, "organizing", "move to review", "Queue item unavailable for review routing", nil)
	}
	logger := logging.WithContext(ctx, o.logger)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Manual review required"
	}
	item.NeedsReview = true
	item.ReviewReason = reason

	if len(sources) == 0 && strings.TrimSpace(item.EncodedFile) != "" {
		sources = []string{item.EncodedFile}
	}

	moved := make([]string, 0, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		target, err := o.movePathToReview(ctx, item, source)
		if err != nil {
			return err
		}
		moved = append(moved, target)
	}
	if len(moved) == 0 {
		return services.Wrap(services.ErrValidation, "organizing", "move to review", "No encoded files available to move to review directory", nil)
	}

	item.FinalFile = moved[len(moved)-1]
	item.EncodedFile = item.FinalFile
	item.Status = queue.StatusCompleted
	item.ProgressStage = "Manual review"
	item.ProgressPercent = 100
	item.ActiveEpisodeKey = ""
	if len(moved) == 1 {
		item.ProgressMessage = fmt.Sprintf("Moved to review directory: %s", filepath.Base(item.FinalFile))
	} else {
		item.ProgressMessage = fmt.Sprintf("Moved %d files to review directory", len(moved))
	}
	if strings.TrimSpace(item.ErrorMessage) == "" {
		if detailErr != nil {
			item.ErrorMessage = fmt.Sprintf("%s: %v", reason, detailErr)
		} else {
			item.ErrorMessage = reason
		}
	}

	if o.notifier != nil {
		label := filepath.Base(item.FinalFile)
		payload := notifications.Payload{
			"filename": label,
			"reason":   strings.TrimSpace(item.ReviewReason),
		}
		if len(moved) > 1 {
			payload["count"] = len(moved)
		}
		if err := o.notifier.Publish(ctx, notifications.EventUnidentifiedMedia, payload); err != nil {
			logger.Debug("review notification failed", logging.Error(err))
		}
	}

	for _, reviewPath := range moved {
		if err := o.validateOrganizedArtifact(ctx, reviewPath, stageStart); err != nil {
			return err
		}
	}
	o.cleanupStaging(ctx, item)
	return nil
}

func collectEncodedSources(item *queue.Item, env *ripspec.Envelope) []string {
	sources := make([]string, 0, 4)
	if env != nil {
		for _, asset := range env.Assets.Encoded {
			if path := strings.TrimSpace(asset.Path); path != "" {
				sources = append(sources, path)
			}
		}
	}
	if len(sources) == 0 && item != nil {
		if path := strings.TrimSpace(item.EncodedFile); path != "" {
			sources = append(sources, path)
		}
	}
	return sources
}

func isLibraryUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	return errors.Is(err, syscall.ENODEV) ||
		errors.Is(err, syscall.ENOTCONN) ||
		errors.Is(err, syscall.EHOSTDOWN) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.EIO) ||
		errors.Is(err, syscall.ESTALE)
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

func (o *Organizer) updateProgress(ctx context.Context, item *queue.Item, message string, percent float64) {
	logger := logging.WithContext(ctx, o.logger)
	copy := *item
	copy.ProgressMessage = message
	copy.ProgressPercent = percent
	if err := o.store.UpdateProgress(ctx, &copy); err != nil {
		logger.Warn("failed to persist organizer progress", logging.Error(err))
		return
	}
	*item = copy
}
