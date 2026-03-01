package encoding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/services/drapto"
)

type draptoRunner struct {
	cfg    *config.Config
	client drapto.Client
	store  *queue.Store
}

func newDraptoRunner(cfg *config.Config, client drapto.Client, store *queue.Store) *draptoRunner {
	return &draptoRunner{cfg: cfg, client: client, store: store}
}

func (r *draptoRunner) Encode(ctx context.Context, item *queue.Item, sourcePath, encodedDir, label, episodeKey string, episodeIndex, episodeCount int, logger *slog.Logger) (string, error) {
	if r == nil || r.client == nil {
		return "", nil
	}
	label = strings.TrimSpace(label)
	episodeKey = strings.ToLower(strings.TrimSpace(episodeKey))
	jobLogger := logger
	if label != "" || episodeKey != "" {
		jobLogger = jobLogger.With(
			logging.String(logging.FieldEpisodeKey, episodeKey),
			logging.String(logging.FieldEpisodeLabel, label),
			logging.Int(logging.FieldEpisodeIndex, episodeIndex),
			logging.Int(logging.FieldEpisodeCount, episodeCount),
		)
	}
	jobLogger.Debug(
		"launching drapto encode",
		logging.String("source_file", sourcePath),
		logging.String("output_dir", encodedDir),
		logging.String("job", label),
	)
	snapshot := loadEncodingSnapshot(jobLogger, item.EncodingDetailsJSON)
	snapshot.JobLabel = label
	snapshot.EpisodeKey = episodeKey
	snapshot.EpisodeIndex = episodeIndex
	snapshot.EpisodeCount = episodeCount
	if raw, err := snapshot.Marshal(); err != nil {
		jobLogger.Warn("failed to marshal encoding snapshot; progress details may be stale",
			logging.Error(err),
			logging.String(logging.FieldEventType, "encoding_snapshot_marshal_failed"),
			logging.String(logging.FieldErrorHint, "check encoding_details_json schema changes"),
		)
	} else if raw != "" {
		copy := *item
		copy.EncodingDetailsJSON = raw
		copy.ActiveEpisodeKey = episodeKey
		if err := r.store.UpdateProgress(ctx, &copy); err != nil {
			jobLogger.Warn("failed to persist encoding job context; progress may be incomplete",
				logging.Error(err),
				logging.String(logging.FieldEventType, "encoding_job_context_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
			)
		} else {
			*item = copy
		}
	}
	const progressPersistInterval = 2 * time.Second
	var lastPersisted time.Time
	progress := func(update drapto.ProgressUpdate) {
		copy := *item
		changed := false
		message := progressMessageText(update)
		if message != "" && label != "" && episodeIndex > 0 && episodeCount > 0 {
			message = fmt.Sprintf("%s (%d/%d) — %s", label, episodeIndex, episodeCount, message)
		} else if message != "" && label != "" {
			message = fmt.Sprintf("%s — %s", label, message)
		}
		snapshotChanged := applyDraptoUpdate(&snapshot, update, message)
		// Update estimated file size during encoding progress
		if update.Type == drapto.EventTypeEncodingProgress {
			snapshotChanged = updateEstimatedSize(&snapshot, update.Percent) || snapshotChanged
		}
		if snapshotChanged {
			if raw, err := snapshot.Marshal(); err != nil {
				jobLogger.Warn("failed to marshal encoding snapshot; progress details may be stale",
					logging.Error(err),
					logging.String(logging.FieldEventType, "encoding_snapshot_marshal_failed"),
					logging.String(logging.FieldErrorHint, "check encoding_details_json schema changes"),
				)
			} else {
				copy.EncodingDetailsJSON = raw
			}
			changed = true
		}
		if stage := strings.TrimSpace(update.Stage); stage != "" && stage != copy.ProgressStage {
			copy.ProgressStage = stage
			changed = true
		}
		if update.Percent >= 0 && update.Percent != copy.ProgressPercent {
			copy.ProgressPercent = update.Percent
			changed = true
		}
		if message != "" && message != strings.TrimSpace(copy.ProgressMessage) {
			copy.ProgressMessage = message
			changed = true
		}
		if !changed {
			return
		}
		if update.Type == drapto.EventTypeEncodingProgress {
			now := time.Now()
			if !lastPersisted.IsZero() && now.Sub(lastPersisted) < progressPersistInterval {
				*item = copy
				return
			}
			lastPersisted = now
		}
		if err := r.store.UpdateProgress(ctx, &copy); err != nil {
			jobLogger.Warn("failed to persist encoding progress; queue status may lag",
				logging.Error(err),
				logging.String(logging.FieldEventType, "queue_progress_persist_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
			)
		}
		*item = copy
	}
	progressSampler := logging.NewProgressSampler(5)
	logProgressEvent := func(update drapto.ProgressUpdate) {
		stage := strings.TrimSpace(update.Stage)
		summary := progressMessageText(update)
		if !progressSampler.ShouldLog(update.Percent, stage, summary) {
			return
		}
		attrs := []logging.Attr{logging.String("job", label)}
		if update.Percent >= 0 {
			attrs = append(attrs, logging.Float64("progress_percent", update.Percent))
		}
		if stage != "" {
			attrs = append(attrs, logging.String("progress_stage", stage))
		}
		if summary != "" {
			attrs = append(attrs, logging.String("progress_message", summary))
		}
		if update.ETA > 0 {
			attrs = append(attrs, logging.Duration("progress_eta", update.ETA))
		}
		if strings.TrimSpace(update.Bitrate) != "" {
			attrs = append(attrs, logging.String("progress_bitrate", strings.TrimSpace(update.Bitrate)))
		}
		jobLogger.Debug("drapto progress", logging.Args(attrs...)...)
	}

	progressLogger := func(update drapto.ProgressUpdate) {
		persist := false
		switch update.Type {
		case drapto.EventTypeHardware:
			logDraptoHardware(jobLogger, label, update.Hardware)
			persist = true
		case drapto.EventTypeInitialization:
			logDraptoVideo(jobLogger, label, update.Video)
			persist = true
		case drapto.EventTypeCropResult:
			logDraptoCrop(jobLogger, label, update.Crop)
			persist = true
		case drapto.EventTypeEncodingConfig:
			logDraptoEncodingConfig(jobLogger, label, update.EncodingConfig)
			persist = true
		case drapto.EventTypeEncodingStarted:
			logDraptoEncodingStart(jobLogger, label, update.TotalFrames)
			persist = true
		case drapto.EventTypeValidation:
			logDraptoValidation(jobLogger, label, update.Validation)
			persist = true
		case drapto.EventTypeEncodingComplete:
			logDraptoEncodingResult(jobLogger, label, update.Result)
			persist = true
		case drapto.EventTypeOperationComplete:
			logDraptoOperation(jobLogger, label, update.OperationComplete)
		case drapto.EventTypeWarning:
			logDraptoWarning(jobLogger, label, update.Warning)
			persist = true
		case drapto.EventTypeError:
			logDraptoError(jobLogger, label, update.Error)
			persist = true
		case drapto.EventTypeBatchStarted:
			logDraptoBatchStart(jobLogger, label, update.BatchStart)
		case drapto.EventTypeFileProgress:
			logDraptoFileProgress(jobLogger, label, update.FileProgress)
		case drapto.EventTypeBatchComplete:
			logDraptoBatchSummary(jobLogger, label, update.BatchSummary)
		case drapto.EventTypeStageProgress, drapto.EventTypeEncodingProgress, drapto.EventTypeUnknown:
			logProgressEvent(update)
			persist = true
		default:
			if strings.TrimSpace(update.Message) != "" {
				attrs := []logging.Attr{
					logging.String("job", label),
					logging.String("drapto_event_type", string(update.Type)),
					logging.String("message", strings.TrimSpace(update.Message)),
				}
				jobLogger.Debug("drapto event", logging.Args(attrs...)...)
			}
		}
		if persist {
			progress(update)
		}
	}

	path, err := r.client.Encode(ctx, sourcePath, encodedDir, drapto.EncodeOptions{
		Progress: progressLogger,
	})
	if err != nil {
		return "", services.Wrap(
			services.ErrExternalTool,
			"encoding",
			"drapto encode",
			"Drapto encoding failed; inspect the encoding log output and confirm the binary path in config",
			err,
		)
	}
	return path, nil
}
