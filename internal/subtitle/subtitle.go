// Package subtitle implements the subtitle generation stage (Layer 4).
//
// Subtitle generation: canonical transcript reuse, generic ASR artifact
// filtering, Stable-TS display formatting, SRT validation, forced subtitle
// ranking from OpenSubtitles, MKV muxing, and resume support.
package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/transcription"
)

// Handler implements stage.Handler for subtitle generation.
type Handler struct {
	cfg         *config.Config
	store       *queue.Store
	osClient    *opensubtitles.Client
	transcriber *transcription.Service
}

// New creates a subtitle handler.
func New(
	cfg *config.Config,
	store *queue.Store,
	osClient *opensubtitles.Client,
	transcriber *transcription.Service,
) *Handler {
	return &Handler{
		cfg:         cfg,
		store:       store,
		osClient:    osClient,
		transcriber: transcriber,
	}
}

// Run executes the subtitle generation stage.
func (h *Handler) Run(ctx context.Context, item *queue.Item) error {
	logger := stage.LoggerFromContext(ctx)
	logger.Info("subtitle stage started", "event_type", "stage_start", "stage", "subtitling")

	if !h.cfg.Subtitles.Enabled {
		logger.Info("subtitles disabled, skipping",
			"decision_type", logs.DecisionSubtitleSkip,
			"decision_result", "skipped",
			"decision_reason", "subtitles.enabled = false",
		)
		return nil
	}

	env, err := stage.ParseRipSpec(item.RipSpecData)
	if err != nil {
		return err
	}

	keys := env.AssetKeys()
	var (
		records     []ripspec.SubtitleGenRecord
		attempted   int
		succeeded   int
		failedCount int
	)

	for i, key := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if existing, ok := env.Assets.FindAsset(ripspec.AssetKindSubtitled, key); ok && existing.IsCompleted() {
			logger.Info("subtitle already completed, skipping",
				"decision_type", logs.DecisionSubtitleResume,
				"decision_result", "skipped",
				"decision_reason", "already completed",
				"episode_key", key,
			)
			continue
		}

		asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, key)
		if !ok || !asset.IsCompleted() {
			continue
		}
		attempted++

		logger.Info("encoded asset selected for transcription",
			"decision_type", logs.DecisionTranscriptionAsset,
			"decision_result", asset.Path,
			"decision_reason", fmt.Sprintf("episode_key=%s", key),
		)

		logger.Info(fmt.Sprintf("Phase %d/%d - Generating subtitles (%s)", i+1, len(keys), key),
			"event_type", "subtitle_start",
		)

		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Generating subtitles (%s)", i+1, len(keys), key)
		item.ActiveEpisodeKey = key
		item.ProgressPercent = overallSubtitlePercent(i, len(keys), 0)
		_ = h.store.UpdateProgress(item)

		selectedAudio, err := h.transcriber.SelectPrimaryAudioTrack(ctx, asset.Path, "en")
		if err != nil {
			failedCount++
			h.recordSubtitleFailure(ctx, logger, item, &env, key, fmt.Sprintf("select audio: %v", err))
			continue
		}

		contentKey := fmt.Sprintf("%s:%s:%d", item.DiscFingerprint, key, selectedAudio.Index)
		workDir := filepath.Join(os.TempDir(), fmt.Sprintf("spindle-subtitle-%s-%s", item.DiscFingerprint, key))
		result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
			InputPath:        asset.Path,
			AudioIndex:       selectedAudio.Index,
			Language:         selectedAudio.Language,
			OutputDir:        workDir,
			ContentKey:       contentKey,
			RequireAlignment: true,
		}, func(phase transcription.Phase, elapsed time.Duration) {
			item.ProgressPercent = overallSubtitlePercent(i, len(keys), subtitlePhasePercent(phase, elapsed))
			switch phase {
			case transcription.PhaseExtract:
				if elapsed == 0 {
					item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Extracting audio (%s)", i+1, len(keys), key)
				}
			case transcription.PhaseTranscribe:
				if elapsed == 0 {
					item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Transcribing audio (%s)", i+1, len(keys), key)
				}
			}
			_ = h.store.UpdateProgress(item)
		})
		if err != nil {
			failedCount++
			h.recordSubtitleFailure(ctx, logger, item, &env, key, fmt.Sprintf("transcribe: %v", err))
			continue
		}

		logger.Info("transcription complete",
			"event_type", "transcription_complete",
			"episode_key", key,
			"segments", result.Segments,
			"content_duration_s", result.Duration,
			"cached", result.Cached,
			"extract_time_ms", result.ExtractTime.Milliseconds(),
			"transcribe_time_ms", result.TranscribeTime.Milliseconds(),
		)

		displayPath := displaySubtitlePath(asset.Path, selectedAudio.Language)
		formatting, err := formatSubtitleFromCanonical(ctx, transcriptionArtifacts{JSONPath: result.JSONPath}, workDir, displayPath, result.Duration, selectedAudio.Language)
		if err != nil {
			failedCount++
			h.recordSubtitleFailure(ctx, logger, item, &env, key, fmt.Sprintf("format subtitle: %v", err))
			continue
		}
		logger.Info("subtitle formatting complete",
			"decision_type", logs.DecisionSubtitleFormatting,
			"decision_result", formatting.FormatterDecision,
			"decision_reason", fmt.Sprintf("original_segments=%d filtered_segments=%d", formatting.OriginalSegments, formatting.FilteredSegments),
			"episode_key", key,
			"subtitle_file", formatting.DisplayPath,
		)
		logger.Info("subtitle artifact filter applied",
			"decision_type", logs.DecisionSubtitleArtifactFilter,
			"decision_result", "filtered",
			"decision_reason", fmt.Sprintf("original=%d filtered=%d segments", formatting.OriginalSegments, formatting.FilteredSegments),
			"episode_key", key,
		)

		formattedCues, readErr := srtutil.ParseFile(formatting.DisplayPath)
		if readErr != nil {
			failedCount++
			h.recordSubtitleFailure(ctx, logger, item, &env, key, fmt.Sprintf("read formatted subtitle: %v", readErr))
			continue
		}
		if len(formattedCues) == 0 {
			failedCount++
			h.recordSubtitleFailure(ctx, logger, item, &env, key, "formatted subtitle produced zero cues")
			continue
		}

		validationIssues := validateCues(formattedCues, result.Duration)
		for _, issue := range validationIssues {
			logger.Info("SRT validation issue",
				"decision_type", logs.DecisionSRTValidation,
				"decision_result", issue,
				"decision_reason", "automated quality check",
				"episode_key", key,
			)
			if ep := env.EpisodeByKey(key); ep != nil {
				ep.AppendReviewReason("Subtitle validation: " + issue)
			}
			item.AppendReviewReason("srt_validation: " + issue + " (" + key + ")")
		}

		record := ripspec.SubtitleGenRecord{
			EpisodeKey:       key,
			Source:           "qwen3_asr",
			Cached:           result.Cached,
			SubtitlePath:     formatting.DisplayPath,
			Segments:         len(formattedCues),
			DurationSec:      result.Duration,
			Language:         selectedAudio.Language,
			ValidationIssues: validationIssues,
		}

		if h.osClient != nil && h.cfg.Subtitles.OpenSubtitlesEnabled && env.Attributes.HasForcedSubtitleTrack {
			h.tryForcedSubs(ctx, logger, &env, key, asset.Path, &record)
		} else {
			var reason string
			switch {
			case h.osClient == nil:
				reason = "opensubtitles client unavailable"
			case !h.cfg.Subtitles.OpenSubtitlesEnabled:
				reason = "opensubtitles_enabled is false"
			default:
				reason = "no forced subtitle track on disc"
			}
			logger.Info("forced subtitle search skipped",
				"decision_type", logs.DecisionForcedSubtitleSearch,
				"decision_result", "skipped",
				"decision_reason", reason,
				"episode_key", key,
			)
		}

		records = append(records, record)

		srtPath := formatting.DisplayPath
		subtitledPath := asset.Path
		subtitlesMuxed := false
		if !h.cfg.Subtitles.MuxIntoMKV {
			logger.Info("subtitle mux skipped",
				"decision_type", logs.DecisionSubtitleMux,
				"decision_result", "skipped",
				"decision_reason", "mux_into_mkv is disabled",
			)
		} else {
			muxedPath, err := h.muxSubtitles(ctx, logger, asset.Path, srtPath, key, selectedAudio.Language)
			if err != nil {
				logger.Warn("subtitle mux failed",
					"event_type", "mux_error",
					"error_hint", err.Error(),
					"impact", "subtitle remains as sidecar",
				)
			} else {
				subtitledPath = muxedPath
				subtitlesMuxed = true
			}
		}

		env.Assets.AddAsset(ripspec.AssetKindSubtitled, ripspec.Asset{
			EpisodeKey:     key,
			Path:           subtitledPath,
			Status:         ripspec.AssetStatusCompleted,
			SubtitlesMuxed: subtitlesMuxed,
		})
		succeeded++
		item.ProgressPercent = overallSubtitlePercent(i+1, len(keys), 0)
		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Generated subtitles (%s)", i+1, len(keys), key)
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
	}

	env.Attributes.SubtitleGenerationResults = records
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
	}
	if attempted > 0 && succeeded == 0 && failedCount > 0 {
		return fmt.Errorf("all %d subtitle job(s) failed", attempted)
	}

	logger.Info("subtitle stage completed", "event_type", "stage_complete", "stage", "subtitling")
	return nil
}

func overallSubtitlePercent(completedItems, totalItems int, currentItemPercent float64) float64 {
	if totalItems <= 0 {
		return 0
	}
	if completedItems < 0 {
		completedItems = 0
	}
	if completedItems > totalItems {
		completedItems = totalItems
	}
	if currentItemPercent < 0 {
		currentItemPercent = 0
	}
	if currentItemPercent > 100 {
		currentItemPercent = 100
	}
	progress := float64(completedItems) + (currentItemPercent / 100)
	if progress > float64(totalItems) {
		progress = float64(totalItems)
	}
	return progress / float64(totalItems) * 100
}

func subtitlePhasePercent(phase transcription.Phase, elapsed time.Duration) float64 {
	switch phase {
	case transcription.PhaseExtract:
		if elapsed > 0 {
			return 25
		}
		return 10
	case transcription.PhaseTranscribe:
		if elapsed > 0 {
			return 90
		}
		return 35
	default:
		return 0
	}
}

func (h *Handler) recordSubtitleFailure(
	ctx context.Context,
	logger *slog.Logger,
	item *queue.Item,
	env *ripspec.Envelope,
	key string,
	errMsg string,
) {
	errMsg = strings.TrimSpace(errMsg)
	if errMsg == "" {
		errMsg = "subtitle generation failed"
	}
	logger.Error("subtitle generation failed for episode",
		"event_type", "episode_subtitle_failed",
		"episode_key", key,
		"error_hint", errMsg,
		"error", errMsg,
		"impact", "subtitle missing for this episode; continuing with others",
	)
	env.Assets.AddAsset(ripspec.AssetKindSubtitled, ripspec.Asset{
		EpisodeKey: key,
		Status:     ripspec.AssetStatusFailed,
		ErrorMsg:   errMsg,
	})
	if ep := env.EpisodeByKey(key); ep != nil {
		ep.AppendReviewReason("Subtitle generation failed: " + errMsg)
	}
	item.AppendReviewReason("subtitle_failure: " + errMsg + " (" + key + ")")
	_ = queue.PersistRipSpec(ctx, h.store, item, env)
}

// tryForcedSubs searches OpenSubtitles for forced subtitle tracks and
// downloads the best match if found. The record's OpenSubtitlesDecision
// field is updated with the outcome.
func (h *Handler) tryForcedSubs(
	ctx context.Context,
	logger *slog.Logger,
	env *ripspec.Envelope,
	key string,
	videoPath string,
	record *ripspec.SubtitleGenRecord,
) {
	tmdbID := env.Metadata.ID
	if tmdbID == 0 {
		logger.Info("forced subtitle search skipped",
			"decision_type", logs.DecisionForcedSubtitleSearch,
			"decision_result", "skipped",
			"decision_reason", "no TMDB ID available",
			"episode_key", key,
		)
		record.OpenSubtitlesDecision = "skipped:no_tmdb_id"
		return
	}

	var season, episode int
	if ep := env.EpisodeByKey(key); ep != nil {
		season = ep.Season
		episode = ep.Episode
	}

	languages := h.cfg.Subtitles.OpenSubtitlesLanguages
	if len(languages) == 0 {
		languages = []string{"en"}
	}

	results, err := h.osClient.Search(ctx, tmdbID, season, episode, languages)
	if err != nil {
		logger.Warn("opensubtitles search failed",
			"event_type", "opensubtitles_error",
			"error_hint", err.Error(),
			"impact", "forced subtitle lookup skipped",
		)
		record.OpenSubtitlesDecision = "error:search_failed"
		return
	}

	// Find the best forced-only subtitle by download count.
	var best *opensubtitles.SubtitleResult
	for i := range results {
		r := &results[i]
		if !r.Attributes.ForeignPartsOnly {
			continue
		}
		if best == nil || r.Attributes.DownloadCount > best.Attributes.DownloadCount {
			best = r
		}
	}

	for _, r := range results {
		var result string
		if !r.Attributes.ForeignPartsOnly {
			result = "skipped"
		} else if best != nil && r.ID == best.ID {
			result = "selected"
		} else {
			result = "candidate"
		}
		logger.Info("forced subtitle candidate",
			"decision_type", logs.DecisionSubtitleRank,
			"decision_result", result,
			"foreign_parts_only", r.Attributes.ForeignPartsOnly,
			"downloads", r.Attributes.DownloadCount,
			"files", len(r.Attributes.Files),
			"episode_key", key,
		)
	}

	if best != nil {
		logger.Info("forced subtitle candidate selected",
			"decision_type", logs.DecisionForcedSubtitleRanking,
			"decision_result", "selected",
			"decision_reason", fmt.Sprintf("candidates=%d best_downloads=%d", len(results), best.Attributes.DownloadCount),
		)
	}

	if best == nil {
		logger.Info("no forced subtitles found on OpenSubtitles",
			"decision_type", logs.DecisionForcedSubtitle,
			"decision_result", "none_available",
			"decision_reason", "no foreign_parts_only results",
			"episode_key", key,
		)
		record.OpenSubtitlesDecision = "none_available"
		return
	}

	if len(best.Attributes.Files) == 0 {
		logger.Warn("forced subtitle has no downloadable files",
			"event_type", "opensubtitles_no_files",
			"error_hint", "best forced subtitle result has zero files",
			"impact", "forced subtitle not downloaded",
			"episode_key", key,
		)
		record.OpenSubtitlesDecision = "error:no_files"
		return
	}

	fileID := best.Attributes.Files[0].FileID
	destPath := displayForcedSubtitlePath(videoPath, record.Language)

	if err := h.osClient.DownloadToFile(ctx, fileID, destPath); err != nil {
		logger.Warn("forced subtitle download failed",
			"event_type", "opensubtitles_error",
			"error_hint", err.Error(),
			"impact", "forced subtitle not available",
		)
		record.OpenSubtitlesDecision = "error:download_failed"
		return
	}

	// Clean the downloaded SRT.
	raw, err := os.ReadFile(destPath)
	if err == nil {
		cleaned := opensubtitles.CleanSRT(string(raw))
		if writeErr := os.WriteFile(destPath, []byte(cleaned), 0o644); writeErr != nil {
			logger.Warn("failed to write cleaned forced SRT",
				"event_type", "file_write_error",
				"error_hint", writeErr.Error(),
				"impact", "forced subtitle may contain HTML tags",
			)
		}
	}

	logger.Info("forced subtitle downloaded",
		"decision_type", logs.DecisionForcedSubtitle,
		"decision_result", "downloaded",
		"decision_reason", fmt.Sprintf("best match: %d downloads", best.Attributes.DownloadCount),
		"episode_key", key,
	)
	record.OpenSubtitlesDecision = "downloaded"
}

// muxSubtitles runs mkvmerge to add an SRT subtitle track to the MKV file.
// It writes to a temp file and renames on success.
func (h *Handler) muxSubtitles(
	ctx context.Context,
	logger *slog.Logger,
	videoPath string,
	srtPath string,
	key string,
	subtitleLanguage string,
) (string, error) {
	dir := filepath.Dir(videoPath)
	ext := filepath.Ext(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), ext)
	outPath := filepath.Join(dir, base+".subtitled"+ext)
	tmpPath := outPath + ".tmp"

	languageCode := language.ToISO3(subtitleLanguage)
	if languageCode == "" || languageCode == "und" {
		languageCode = "eng"
	}
	trackName := language.DisplayName(subtitleLanguage)
	if strings.TrimSpace(trackName) == "" {
		trackName = "English"
	}

	args := []string{
		"-o", tmpPath,
		videoPath,
		"--language", "0:" + languageCode,
		"--track-name", "0:" + trackName,
		"--default-track-flag", "0:no",
		srtPath,
	}

	cmd := exec.CommandContext(ctx, "mkvmerge", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Clean up partial output.
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("mkvmerge %s: %w: %s", key, err, output)
	}

	// Rename temp to final.
	if err := os.Rename(tmpPath, outPath); err != nil {
		return "", fmt.Errorf("rename muxed file %s: %w", key, err)
	}

	logger.Info("subtitles muxed into MKV",
		"event_type", "mux_complete",
		"episode_key", key,
		"output_path", outPath,
	)

	return outPath, nil
}
