// Package subtitle implements the subtitle generation stage (Layer 4).
//
// Subtitle generation: WhisperX transcription, SRT validation, forced subtitle
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
	var records []ripspec.SubtitleGenRecord

	for i, key := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Resume support: skip already-completed subtitled assets.
		if existing, ok := env.Assets.FindAsset("subtitled", key); ok && existing.IsCompleted() {
			logger.Info("subtitle already completed, skipping",
				"decision_type", logs.DecisionSubtitleResume,
				"decision_result", "skipped",
				"decision_reason", "already completed",
				"episode_key", key,
			)
			continue
		}

		asset, ok := env.Assets.FindAsset("encoded", key)
		if !ok || !asset.IsCompleted() {
			continue
		}

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

		// Transcribe.
		selectedAudio, err := h.transcriber.SelectPrimaryAudioTrack(ctx, asset.Path, "en")
		if err != nil {
			return fmt.Errorf("select audio %s: %w", key, err)
		}
		contentKey := fmt.Sprintf("%s:%s:%d", item.DiscFingerprint, key, selectedAudio.Index)
		outputDir := filepath.Join(os.TempDir(), fmt.Sprintf("spindle-subtitle-%s-%s", item.DiscFingerprint, key))
		result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
			InputPath:  asset.Path,
			AudioIndex: selectedAudio.Index,
			Language:   selectedAudio.Language,
			OutputDir:  outputDir,
			ContentKey: contentKey,
		}, func(phase string, elapsed time.Duration) {
			item.ProgressPercent = overallSubtitlePercent(i, len(keys), subtitlePhasePercent(phase, elapsed))
			switch phase {
			case "extract":
				if elapsed == 0 {
					item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Extracting audio (%s)", i+1, len(keys), key)
				}
			case "transcribe":
				if elapsed == 0 {
					item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Transcribing audio (%s)", i+1, len(keys), key)
				}
			}
			_ = h.store.UpdateProgress(item)
		})
		if err != nil {
			return fmt.Errorf("transcribe %s: %w", key, err)
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

		// Hallucination filtering.
		srtContent, readErr := os.ReadFile(result.SRTPath)
		if readErr != nil {
			return fmt.Errorf("read SRT %s: %w", key, readErr)
		}
		videoDuration := result.Duration
		filtered, filterErr := filterWhisperXOutput(string(srtContent), videoDuration)
		if filterErr != nil {
			return fmt.Errorf("hallucination filter %s: %w", key, filterErr)
		}
		if writeErr := os.WriteFile(result.SRTPath, []byte(filtered), 0o644); writeErr != nil {
			return fmt.Errorf("write filtered SRT %s: %w", key, writeErr)
		}

		filteredCues := srtutil.Parse(filtered)
		logger.Info("hallucination filter applied",
			"decision_type", logs.DecisionHallucinationFilter,
			"decision_result", "filtered",
			"decision_reason", fmt.Sprintf("original=%d filtered=%d cues", result.Segments, len(filteredCues)),
			"episode_key", key,
		)

		// SRT validation.
		validationIssues, valErr := ValidateSRTContent(result.SRTPath, videoDuration)
		if valErr != nil {
			logger.Warn("SRT validation failed",
				"event_type", "srt_validation_error",
				"error_hint", valErr.Error(),
				"impact", "validation skipped",
			)
		}
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
			Source:           "whisperx",
			Cached:           result.Cached,
			SubtitlePath:     result.SRTPath,
			Segments:         len(filteredCues),
			DurationSec:      result.Duration,
			Language:         selectedAudio.Language,
			ValidationIssues: validationIssues,
		}

		// Try forced subtitles from OpenSubtitles (if enabled and disc has forced sub track).
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

		// Mux into MKV if configured.
		srtPath := result.SRTPath
		subtitledPath := asset.Path // default: same file
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
			}
		}

		// Record subtitled asset.
		env.Assets.AddAsset("subtitled", ripspec.Asset{
			EpisodeKey:     key,
			Path:           subtitledPath,
			Status:         "completed",
			SubtitlesMuxed: h.cfg.Subtitles.MuxIntoMKV,
		})
		item.ProgressPercent = overallSubtitlePercent(i+1, len(keys), 0)
		item.ProgressMessage = fmt.Sprintf("Phase %d/%d - Generated subtitles (%s)", i+1, len(keys), key)
		if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
			return err
		}
	}

	// Store subtitle generation results.
	env.Attributes.SubtitleGenerationResults = records

	// Persist.
	if err := queue.PersistRipSpec(ctx, h.store, item, &env); err != nil {
		return err
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

func subtitlePhasePercent(phase string, elapsed time.Duration) float64 {
	phase = strings.ToLower(strings.TrimSpace(phase))
	switch phase {
	case "extract":
		if elapsed > 0 {
			return 25
		}
		return 10
	case "transcribe":
		if elapsed > 0 {
			return 90
		}
		return 35
	default:
		return 0
	}
}

// tryForcedSubs searches OpenSubtitles for forced subtitle tracks and
// downloads the best match if found. The record's OpenSubtitlesDecision
// field is updated with the outcome.
func (h *Handler) tryForcedSubs(
	ctx context.Context,
	logger *slog.Logger,
	env *ripspec.Envelope,
	key string,
	_ string, // videoPath reserved for future hash-based lookup
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
	destDir := filepath.Dir(record.SubtitlePath)
	destPath := filepath.Join(destDir, fmt.Sprintf("%s.forced.srt", key))

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
