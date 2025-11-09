package ripping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/contentid"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services"
	"spindle/internal/services/makemkv"
	"spindle/internal/stage"
)

// Ripper manages the MakeMKV ripping workflow.
type Ripper struct {
	store          *queue.Store
	cfg            *config.Config
	logger         *slog.Logger
	client         makemkv.Ripper
	notifier       notifications.Service
	contentMatcher *contentid.Matcher
}

const (
	minRipFileSizeBytes = 10 * 1024 * 1024
)

var probeVideo = ffprobe.Inspect

// NewRipper constructs the ripping handler using default dependencies.
func NewRipper(cfg *config.Config, store *queue.Store, logger *slog.Logger) *Ripper {
	client, err := makemkv.New(cfg.MakemkvBinary(), cfg.MakeMKVRipTimeout)
	if err != nil {
		logger.Warn("makemkv client unavailable", logging.Error(err))
	}
	return NewRipperWithDependencies(cfg, store, logger, client, notifications.NewService(cfg))
}

// NewRipperWithDependencies allows injecting all collaborators (used in tests).
func NewRipperWithDependencies(cfg *config.Config, store *queue.Store, logger *slog.Logger, client makemkv.Ripper, notifier notifications.Service) *Ripper {
	var matcher *contentid.Matcher
	if cfg != nil {
		matcher = contentid.NewMatcher(cfg, logger)
	}
	rip := &Ripper{store: store, cfg: cfg, client: client, notifier: notifier, contentMatcher: matcher}
	rip.SetLogger(logger)
	return rip
}

// SetLogger updates the ripper's logging destination while preserving component labeling.
func (r *Ripper) SetLogger(logger *slog.Logger) {
	stageLogger := logger
	if stageLogger == nil {
		stageLogger = logging.NewNop()
	}
	r.logger = stageLogger.With(logging.String("component", "ripper"))
}

func (r *Ripper) Prepare(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	if item.ProgressStage == "" {
		item.ProgressStage = "Ripping"
	}
	item.ProgressMessage = "Starting rip"
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	logger.Info(
		"starting rip preparation",
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("source_path", strings.TrimSpace(item.SourcePath)),
	)
	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipStarted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Warn("failed to send rip start notification", logging.Error(err))
		}
	}
	return nil
}

func (r *Ripper) Execute(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, r.logger)
	startedAt := time.Now()
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
	const progressInterval = time.Minute
	var lastPersisted time.Time
	lastStage := item.ProgressStage
	lastMessage := item.ProgressMessage
	lastPercent := item.ProgressPercent
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
		r.applyProgress(ctx, item, update)
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
	stagingRoot := item.StagingRoot(r.cfg.StagingDir)
	if stagingRoot == "" {
		stagingRoot = filepath.Join(strings.TrimSpace(r.cfg.StagingDir), fmt.Sprintf("queue-%d", item.ID))
	}
	destDir := filepath.Join(stagingRoot, "rips")
	logger.Info(
		"starting rip execution",
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("staging_root", stagingRoot),
		logging.String("destination_dir", destDir),
		logging.Bool("makemkv_enabled", r.client != nil),
	)

	var titleIDs []int
	if r.client != nil {
		if err := ensureMakeMKVSelectionRule(); err != nil {
			logger.Error(
				"failed to configure makemkv selection",
				logging.Error(err),
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
		logger.Info(
			"launching makemkv rip",
			logging.String("destination_dir", destDir),
			logging.Any("title_ids", titleIDs),
		)
		path, err := r.client.Rip(ctx, item.DiscTitle, item.SourcePath, destDir, titleIDs, progressCB)
		if err != nil {
			return services.Wrap(
				services.ErrExternalTool,
				"ripping",
				"makemkv rip",
				"MakeMKV rip failed; check MakeMKV installation and disc readability",
				err,
			)
		}
		target = path
		if err := r.refineAudioTracks(ctx, target); err != nil {
			return services.Wrap(
				services.ErrExternalTool,
				"ripping",
				"refine audio tracks",
				"Failed to optimize ripped audio tracks with ffmpeg",
				err,
			)
		}
		logger.Info("makemkv rip finished", logging.String("ripped_file", target))
	}

	if target == "" {
		sourcePath := strings.TrimSpace(item.SourcePath)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return services.Wrap(
				services.ErrConfiguration,
				"ripping",
				"ensure staging dir",
				"Failed to create staging directory; set staging_dir to a writable location",
				err,
			)
		}
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
		logger.Info(
			"copied source into rip staging",
			logging.String("source_path", sourcePath),
			logging.String("ripped_file", target),
		)
	}

	validationTargets := []string{}
	if strings.TrimSpace(target) != "" {
		validationTargets = append(validationTargets, target)
	}
	specDirty := false
	if hasEpisodes && r.client != nil {
		assigned := assignEpisodeAssets(&env, destDir, logger)
		if assigned == 0 {
			logger.Warn("episode asset mapping incomplete", logging.String("dest_dir", destDir))
		} else {
			specDirty = true
			if r.contentMatcher != nil {
				if updated, err := r.contentMatcher.Match(ctx, item, &env); err != nil {
					logger.Warn("episode content identification failed", logging.Error(err))
				} else if updated {
					specDirty = true
				}
			}
			paths := episodeAssetPaths(env)
			if len(paths) > 0 {
				validationTargets = paths
				target = paths[0]
			}
		}
	}
	if specDirty {
		if encoded, encodeErr := env.Encode(); encodeErr == nil {
			item.RipSpecData = encoded
		} else {
			logger.Warn("failed to encode rip spec after ripping", logging.Error(encodeErr))
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

	item.RippedFile = target
	item.ProgressStage = "Ripped"
	item.ProgressPercent = 100
	item.ProgressMessage = "Disc content ripped"
	logger.Info("ripping completed", logging.String("ripped_file", target))

	if r.notifier != nil {
		if err := r.notifier.Publish(ctx, notifications.EventRipCompleted, notifications.Payload{"discTitle": item.DiscTitle}); err != nil {
			logger.Warn("rip completion notification failed", logging.Error(err))
		}
	}

	return nil
}

func (r *Ripper) refineAudioTracks(ctx context.Context, path string) error {
	logger := logging.WithContext(ctx, r.logger)
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("refine audio: empty path")
	}
	ffprobeBinary := "ffprobe"
	if r.cfg != nil {
		ffprobeBinary = r.cfg.FFprobeBinary()
	}
	probe, err := probeVideo(ctx, ffprobeBinary, path)
	if err != nil {
		return fmt.Errorf("inspect ripped audio: %w", err)
	}
	totalAudio := countAudioStreams(probe.Streams)
	if totalAudio <= 1 {
		return nil
	}
	selection := audio.Select(probe.Streams)
	if !selection.Changed(totalAudio) {
		return nil
	}
	if len(selection.KeepIndices) == 0 {
		return fmt.Errorf("refine audio: selection produced no audio streams")
	}
	tmpPath := deriveTempAudioPath(path)
	if err := r.remuxAudioSelection(ctx, path, tmpPath, selection); err != nil {
		return err
	}
	// Replace the original rip with the remuxed variant.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("refine audio: remove original rip: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("refine audio: finalize remux: %w", err)
	}
	if logger != nil {
		fields := []any{
			logging.String("primary_audio", selection.PrimaryLabel()),
			logging.Int("kept_audio_streams", len(selection.KeepIndices)),
		}
		if labels := selection.CommentaryLabels(); len(labels) > 0 {
			fields = append(fields, logging.Any("commentary_audio", labels))
		}
		if len(selection.RemovedIndices) > 0 {
			fields = append(fields, logging.Any("removed_audio_indices", selection.RemovedIndices))
		}
		logger.Info("refined ripped audio tracks", fields...)
	}
	return nil
}

func (r *Ripper) remuxAudioSelection(ctx context.Context, src, dst string, selection audio.Selection) error {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(dst) == "" {
		return fmt.Errorf("remux audio: invalid path")
	}
	ffmpegBinary := "ffmpeg"
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", src, "-map", "0:v?", "-map", "0:s?", "-map", "0:d?", "-map", "0:t?"}
	for _, idx := range selection.KeepIndices {
		args = append(args, "-map", fmt.Sprintf("0:%d", idx))
	}
	args = append(args, "-c", "copy")
	if len(selection.KeepIndices) > 0 {
		args = append(args, "-disposition:a:0", "default")
		for i := 1; i < len(selection.KeepIndices); i++ {
			args = append(args, "-disposition:a:"+strconv.Itoa(i), "none")
		}
	}
	if format := outputFormatForPath(dst); format != "" {
		args = append(args, "-f", format)
	}
	args = append(args, dst)
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg remux: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func countAudioStreams(streams []ffprobe.Stream) int {
	count := 0
	for _, stream := range streams {
		if strings.EqualFold(stream.CodecType, "audio") {
			count++
		}
	}
	return count
}

func deriveTempAudioPath(path string) string {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return path + ".spindle-audio"
	}
	ext := filepath.Ext(clean)
	base := strings.TrimSuffix(clean, ext)
	if ext == "" {
		ext = ".mkv"
	}
	return fmt.Sprintf("%s.spindle-audio%s", base, ext)
}

func outputFormatForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".mk3d":
		return "matroska"
	case ".mp4", ".m4v":
		return "mp4"
	case ".mov":
		return "mov"
	case ".ts", ".m2ts":
		return "mpegts"
	case ".mka":
		return "matroska"
	default:
		return ""
	}
}

func (r *Ripper) validateRippedArtifact(ctx context.Context, item *queue.Item, path string, startedAt time.Time) error {
	logger := logging.WithContext(ctx, r.logger)
	clean := strings.TrimSpace(path)
	if clean == "" {
		logger.Error("ripping validation failed", logging.String("reason", "empty path"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			"Ripping produced an empty file path",
			nil,
		)
	}
	info, err := os.Stat(clean)
	if err != nil {
		logger.Error("ripping validation failed", logging.String("reason", "stat failure"), logging.Error(err))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			"Failed to stat ripped file",
			err,
		)
	}
	if info.IsDir() {
		logger.Error("ripping validation failed", logging.String("reason", "path is directory"), logging.String("ripped_path", clean))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			"Ripped artifact points to a directory",
			nil,
		)
	}
	if info.Size() < minRipFileSizeBytes {
		logger.Error(
			"ripping validation failed",
			logging.String("reason", "file too small"),
			logging.Int64("size_bytes", info.Size()),
		)
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate output",
			fmt.Sprintf("Ripped file %q is unexpectedly small (%d bytes)", clean, info.Size()),
			nil,
		)
	}

	binary := "ffprobe"
	if r.cfg != nil {
		binary = r.cfg.FFprobeBinary()
	}
	probe, err := probeVideo(ctx, binary, clean)
	if err != nil {
		logger.Error("ripping validation failed", logging.String("reason", "ffprobe"), logging.Error(err))
		return services.Wrap(
			services.ErrExternalTool,
			"ripping",
			"ffprobe validation",
			"Failed to inspect ripped file with ffprobe",
			err,
		)
	}
	if probe.VideoStreamCount() == 0 {
		logger.Error("ripping validation failed", logging.String("reason", "no video stream"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate video stream",
			"Ripped file does not contain a video stream",
			nil,
		)
	}
	if probe.AudioStreamCount() == 0 {
		logger.Error("ripping validation failed", logging.String("reason", "no audio stream"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate audio stream",
			"Ripped file does not contain an audio stream",
			nil,
		)
	}
	duration := probe.DurationSeconds()
	if duration <= 0 {
		logger.Error("ripping validation failed", logging.String("reason", "invalid duration"))
		return services.Wrap(
			services.ErrValidation,
			"ripping",
			"validate duration",
			"Ripped file duration could not be determined",
			nil,
		)
	}

	logger.Info(
		"ripping validation succeeded",
		logging.String("ripped_file", clean),
		logging.Duration("elapsed", time.Since(startedAt)),
		logging.String("ffprobe_binary", binary),
		logging.Group("ffprobe",
			logging.Float64("duration_seconds", duration),
			logging.Int("video_streams", probe.VideoStreamCount()),
			logging.Int("audio_streams", probe.AudioStreamCount()),
			logging.Int64("size_bytes", info.Size()),
			logging.Int64("bitrate_bps", probe.BitRate()),
		),
	)

	return nil
}

// HealthCheck verifies MakeMKV ripping dependencies.
func (r *Ripper) HealthCheck(ctx context.Context) stage.Health {
	const name = "ripper"
	if r.cfg == nil {
		return stage.Unhealthy(name, "configuration unavailable")
	}
	if strings.TrimSpace(r.cfg.StagingDir) == "" {
		return stage.Unhealthy(name, "staging directory not configured")
	}
	if strings.TrimSpace(r.cfg.OpticalDrive) == "" {
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

func (r *Ripper) applyProgress(ctx context.Context, item *queue.Item, update makemkv.ProgressUpdate) {
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
		logger.Warn("failed to persist progress", logging.Error(err))
		return
	}
	fields := []any{logging.Int("percent", int(math.Round(copy.ProgressPercent)))}
	if stage := strings.TrimSpace(copy.ProgressStage); stage != "" {
		fields = append(fields, logging.String("stage", stage))
	}
	if message := strings.TrimSpace(copy.ProgressMessage); message != "" && !strings.HasPrefix(message, "Progress ") {
		fields = append(fields, logging.String("message", message))
	}
	logger.Info("makemkv progress", fields...)
	*item = copy
}

const minPrimaryRuntimeSeconds = 20 * 60

func (r *Ripper) selectTitleIDs(item *queue.Item, logger *slog.Logger) []int {
	if item == nil {
		return nil
	}
	raw := strings.TrimSpace(item.RipSpecData)
	if raw == "" {
		return nil
	}
	env, err := ripspec.Parse(raw)
	if err != nil {
		if logger != nil {
			logger.Debug("failed to parse rip spec", logging.Error(err))
		}
		return nil
	}
	mediaType := strings.ToLower(strings.TrimSpace(fmt.Sprint(env.Metadata["media_type"])))
	if mediaType == "tv" {
		ids := uniqueEpisodeTitleIDs(env)
		if len(ids) == 0 {
			return nil
		}
		sort.Ints(ids)
		return ids
	}
	if selection, ok := choosePrimaryTitle(env.Titles); ok {
		if logger != nil {
			logger.Info(
				"selecting primary title",
				logging.Int("title_id", selection.ID),
				logging.Int("duration_seconds", selection.Duration),
				logging.String("title_name", strings.TrimSpace(selection.Name)),
			)
		}
		return []int{selection.ID}
	}
	return nil
}

func choosePrimaryTitle(titles []ripspec.Title) (ripspec.Title, bool) {
	if len(titles) == 0 {
		return ripspec.Title{}, false
	}
	indices := make([]int, 0, len(titles))
	for idx := range titles {
		if titles[idx].ID < 0 {
			continue
		}
		indices = append(indices, idx)
	}
	if len(indices) == 0 {
		return ripspec.Title{}, false
	}
	sort.Slice(indices, func(i, j int) bool {
		left := titles[indices[i]]
		right := titles[indices[j]]
		if left.Duration == right.Duration {
			return left.ID < right.ID
		}
		return left.Duration > right.Duration
	})
	primaryIdx := -1
	for _, idx := range indices {
		title := titles[idx]
		if title.Duration >= minPrimaryRuntimeSeconds {
			primaryIdx = idx
			break
		}
	}
	if primaryIdx == -1 {
		for _, idx := range indices {
			title := titles[idx]
			if title.Duration > 0 {
				primaryIdx = idx
				break
			}
		}
	}
	if primaryIdx == -1 {
		return ripspec.Title{}, false
	}
	return titles[primaryIdx], true
}

func uniqueEpisodeTitleIDs(env ripspec.Envelope) []int {
	if len(env.Episodes) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(env.Episodes))
	ids := make([]int, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		if episode.TitleID < 0 {
			continue
		}
		if _, ok := seen[episode.TitleID]; ok {
			continue
		}
		seen[episode.TitleID] = struct{}{}
		ids = append(ids, episode.TitleID)
	}
	return ids
}

var titleFilePattern = regexp.MustCompile(`(?i)title_t?(\d{2,3})`)

func assignEpisodeAssets(env *ripspec.Envelope, dir string, logger *slog.Logger) int {
	if env == nil || len(env.Episodes) == 0 {
		return 0
	}
	titleFiles := make(map[int]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to inspect rip directory", logging.String("dir", dir), logging.Error(err))
		}
		return 0
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".mkv") {
			continue
		}
		id, ok := parseTitleID(name)
		if !ok {
			continue
		}
		titleFiles[id] = filepath.Join(dir, name)
	}
	assigned := 0
	for _, episode := range env.Episodes {
		if episode.TitleID < 0 {
			continue
		}
		path, ok := titleFiles[episode.TitleID]
		if !ok {
			continue
		}
		env.Assets.AddAsset("ripped", ripspec.Asset{EpisodeKey: episode.Key, TitleID: episode.TitleID, Path: path})
		assigned++
	}
	return assigned
}

func episodeAssetPaths(env ripspec.Envelope) []string {
	if len(env.Episodes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(env.Episodes))
	paths := make([]string, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("ripped", episode.Key)
		if !ok {
			continue
		}
		clean := strings.TrimSpace(asset.Path)
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	return paths
}

func parseTitleID(name string) (int, bool) {
	match := titleFilePattern.FindStringSubmatch(name)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return value, true
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}

func copyPlaceholder(src, dst string) error {
	sourceData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source file: %w", err)
	}
	if err := os.WriteFile(dst, sourceData, 0o644); err != nil {
		return fmt.Errorf("write placeholder file: %w", err)
	}
	return nil
}
