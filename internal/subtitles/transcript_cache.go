package subtitles

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

// tryReuseCachedTranscript checks whether a contentid transcript exists for the
// given episode and copies it to the subtitle output location when available.
// Returns the populated GenerateResult and true on success; false on any failure
// so the caller falls back to normal generation.
func (s *Stage) tryReuseCachedTranscript(target subtitleTarget, env *ripspec.Envelope) (GenerateResult, bool) {
	episodeKey := normalizeEpisodeKey(target.EpisodeKey)

	// Movies have no episode keys in contentid - skip cache entirely.
	if episodeKey == "primary" {
		return GenerateResult{}, false
	}

	cachedPath := lookupTranscriptPath(env, episodeKey)
	if cachedPath == "" {
		return GenerateResult{}, false
	}

	info, err := os.Stat(cachedPath)
	if err != nil || info.Size() == 0 {
		if s.logger != nil {
			s.logger.Info("contentid transcript cache miss",
				logging.String(logging.FieldDecisionType, "transcript_cache"),
				logging.String("decision_result", "miss"),
				logging.String("decision_reason", "file_unavailable"),
				logging.String("episode_key", episodeKey),
				logging.String("cached_path", cachedPath),
			)
		}
		return GenerateResult{}, false
	}

	// Verify the cached SRT has actual content.
	cues, err := countSRTCues(cachedPath)
	if err != nil || cues == 0 {
		if s.logger != nil {
			s.logger.Warn("contentid transcript cache rejected",
				logging.String(logging.FieldEventType, "transcript_cache_rejected"),
				logging.String(logging.FieldImpact, "falling back to WhisperX generation"),
				logging.String(logging.FieldErrorHint, "cached SRT was empty or unreadable"),
				logging.String("episode_key", episodeKey),
				logging.String("cached_path", cachedPath),
			)
		}
		// Remove the bad copy if it was already placed.
		return GenerateResult{}, false
	}

	// Build the destination path matching what Generate would produce.
	destPath := filepath.Join(target.OutputDir, target.BaseName+".srt")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		if s.logger != nil {
			s.logger.Warn("contentid transcript cache dir creation failed",
				logging.String(logging.FieldEventType, "transcript_cache_dir_failed"),
				logging.String(logging.FieldImpact, "falling back to WhisperX generation"),
				logging.String(logging.FieldErrorHint, "check file permissions on output directory"),
				logging.String("episode_key", episodeKey),
				logging.Error(err),
			)
		}
		return GenerateResult{}, false
	}
	if err := copyFile(cachedPath, destPath); err != nil {
		if s.logger != nil {
			s.logger.Warn("contentid transcript cache copy failed",
				logging.String(logging.FieldEventType, "transcript_cache_copy_failed"),
				logging.String(logging.FieldImpact, "falling back to WhisperX generation"),
				logging.String(logging.FieldErrorHint, "check file permissions on output directory"),
				logging.String("episode_key", episodeKey),
				logging.String("src", cachedPath),
				logging.String("dst", destPath),
				logging.Error(err),
			)
		}
		return GenerateResult{}, false
	}

	// Derive duration from the last timestamp in the SRT.
	var duration time.Duration
	if last, err := lastSRTTimestamp(destPath); err == nil && last > 0 {
		duration = time.Duration(last * float64(time.Second))
	}

	if s.logger != nil {
		s.logger.Info("contentid transcript cache hit",
			logging.String(logging.FieldDecisionType, "transcript_cache"),
			logging.String("decision_result", "hit"),
			logging.String("decision_reason", "contentid_srt_reused"),
			logging.String("episode_key", episodeKey),
			logging.String("cached_path", cachedPath),
			logging.String("dest_path", destPath),
			logging.Int("cues", cues),
		)
	}

	return GenerateResult{
		SubtitlePath: destPath,
		SegmentCount: cues,
		Duration:     duration,
		Source:       "whisperx",
	}, true
}

// lookupTranscriptPath retrieves the cached SRT path for an episode key from
// env.Attributes["content_id_transcripts"]. It handles both the in-memory
// map[string]string type and the map[string]any type that results from JSON
// round-tripping.
func lookupTranscriptPath(env *ripspec.Envelope, episodeKey string) string {
	if env == nil || len(env.Attributes) == 0 {
		return ""
	}
	raw, ok := env.Attributes["content_id_transcripts"]
	if !ok || raw == nil {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(episodeKey))
	if key == "" {
		return ""
	}
	switch m := raw.(type) {
	case map[string]string:
		return strings.TrimSpace(m[key])
	case map[string]any:
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
