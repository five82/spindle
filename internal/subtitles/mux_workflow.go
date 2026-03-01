package subtitles

import (
	"context"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

// shouldMuxSubtitles returns true if subtitle muxing is enabled.
func (g *Generator) shouldMuxSubtitles() bool {
	if g == nil || g.service == nil || g.service.config == nil {
		return false
	}
	return g.service.config.Subtitles.MuxIntoMKV
}

// tryMuxSubtitles attempts to mux generated subtitles into the encoded MKV.
// Returns true if muxing succeeded, false otherwise (sidecars are preserved on failure).
func (g *Generator) tryMuxSubtitles(ctx context.Context, target subtitleTarget, result GenerateResult, env *ripspec.Envelope, episodeKey string) bool {
	if g == nil || g.muxer == nil {
		return false
	}

	// Collect subtitle paths to mux
	var srtPaths []string
	if strings.TrimSpace(result.SubtitlePath) != "" {
		srtPaths = append(srtPaths, result.SubtitlePath)
	}
	if strings.TrimSpace(result.ForcedSubtitlePath) != "" {
		srtPaths = append(srtPaths, result.ForcedSubtitlePath)
	}
	if len(srtPaths) == 0 {
		return false
	}

	// Get the encoded MKV path from the target
	mkvPath := strings.TrimSpace(target.SourcePath)
	if mkvPath == "" {
		if g.logger != nil {
			g.logger.Warn("cannot mux subtitles: no source MKV path",
				logging.String("episode_key", episodeKey),
				logging.String(logging.FieldEventType, "mux_skipped"),
			)
		}
		return false
	}

	// Determine language from subtitle context or filename
	lang := "en"
	if g.service != nil && len(g.service.languages) > 0 {
		lang = g.service.languages[0]
	}

	if g.logger != nil {
		g.logger.Info("subtitle mux decision",
			logging.String(logging.FieldDecisionType, "subtitle_mux"),
			logging.String("decision_result", "muxing"),
			logging.String("decision_reason", "mux_into_mkv_enabled"),
			logging.String("episode_key", episodeKey),
			logging.String("mkv_path", mkvPath),
			logging.Int("subtitle_count", len(srtPaths)),
		)
	}

	muxResult, err := g.muxer.MuxSubtitles(ctx, MuxRequest{
		MKVPath:           mkvPath,
		SubtitlePaths:     srtPaths,
		Language:          lang,
		StripExistingSubs: true, // Remove any existing subtitle tracks
	})
	if err != nil {
		if g.logger != nil {
			g.logger.Warn("subtitle muxing failed; keeping sidecar files",
				logging.Error(err),
				logging.String("episode_key", episodeKey),
				logging.String("mkv_path", mkvPath),
				logging.String(logging.FieldEventType, "mux_failed"),
				logging.String(logging.FieldErrorHint, "check mkvmerge installation and MKV file integrity"),
				logging.String(logging.FieldImpact, "subtitles preserved as external sidecar files"),
			)
		}
		return false
	}

	if g.logger != nil {
		g.logger.Info("subtitle muxing completed",
			logging.String(logging.FieldEventType, "mux_complete"),
			logging.String("episode_key", episodeKey),
			logging.String("output_path", muxResult.OutputPath),
			logging.Int("tracks_muxed", len(muxResult.MuxedSubtitles)),
			logging.Int("sidecars_removed", len(muxResult.RemovedSidecars)),
		)
	}

	// Validate that subtitles were actually embedded in the MKV
	ffprobeBinary := ""
	if g.service != nil && g.service.config != nil {
		ffprobeBinary = g.service.config.FFprobeBinary()
	}
	if err := ValidateMuxedSubtitles(ctx, ffprobeBinary, mkvPath, len(srtPaths), lang, g.logger); err != nil {
		// Error already logged by ValidateMuxedSubtitles
		return false
	}

	return true
}
