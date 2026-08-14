package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
)

// auditReferenceTranscript returns best-effort untimed comparison text. A
// missing or unusable reference deliberately falls back to the existing
// WhisperX-only audit rather than failing subtitle generation.
func (h *Handler) auditReferenceTranscript(ctx context.Context, sess *stage.Session, episodeKey string) string {
	if h.llm == nil || h.cfg == nil {
		return ""
	}

	var transcript, reason string
	switch strings.ToLower(strings.TrimSpace(sess.Env.Metadata.MediaType)) {
	case "tv":
		transcript, reason = h.tvReferenceTranscript(sess, episodeKey)
	case "movie":
		transcript, reason = h.movieReferenceTranscript(ctx, sess.Logger, sess.Env.Metadata)
	default:
		reason = "media type has no reference acquisition policy"
	}

	if transcript == "" {
		sess.Logger.Info("subtitle audit reference unavailable",
			"decision_type", "subtitle_audit_reference",
			"decision_result", "whisperx_only",
			"decision_reason", reason,
			"episode_key", episodeKey,
		)
		return ""
	}

	sess.Logger.Info("subtitle audit reference selected",
		"decision_type", "subtitle_audit_reference",
		"decision_result", "reference_assisted",
		"decision_reason", reason,
		"episode_key", episodeKey,
		"reference_words", len(strings.Fields(transcript)),
	)
	return transcript
}

func (h *Handler) tvReferenceTranscript(sess *stage.Session, episodeKey string) (string, string) {
	episode := sess.Env.EpisodeByKey(episodeKey)
	if episode == nil || episode.Season <= 0 || episode.Episode <= 0 {
		return "", "TV episode identity is unresolved"
	}
	stagingRoot, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		warnReferenceUnavailable(sess.Logger, episodeKey, "staging path unavailable", err)
		return "", "TV reference staging path is unavailable"
	}

	var parts []string
	for number := episode.Episode; number <= episode.EpisodeLast(); number++ {
		pattern := filepath.Join(stagingRoot, "contentid", "references", fmt.Sprintf("s%02de%02d-*.srt", episode.Season, number))
		matches, err := filepath.Glob(pattern)
		if err != nil {
			warnReferenceUnavailable(sess.Logger, episodeKey, "reference lookup failed", err)
			continue
		}
		if len(matches) != 1 {
			if len(matches) > 1 {
				warnReferenceUnavailable(sess.Logger, episodeKey, "reference lookup was ambiguous", fmt.Errorf("%d files match %s", len(matches), pattern))
			}
			continue
		}
		text, err := readReferenceTranscript(matches[0])
		if err != nil {
			warnReferenceUnavailable(sess.Logger, episodeKey, "reference transcript is unreadable", err)
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "", "identified TV episode has no usable downloaded reference"
	}
	return strings.Join(parts, "\n"), "downloaded reference for the identified TV episode"
}

func (h *Handler) movieReferenceTranscript(ctx context.Context, logger *slog.Logger, metadata ripspec.Metadata) (string, string) {
	text, fileID, cached, err := fetchOpenSubtitlesTranscript(ctx, h.cfg, h.osClient, metadata.ID, 0, 0)
	if err != nil {
		warnReferenceUnavailable(logger, "movie", "OpenSubtitles movie reference failed", err)
		return "", "OpenSubtitles movie reference failed"
	}
	if cached {
		return text, fmt.Sprintf("cached OpenSubtitles movie reference file_id=%d", fileID)
	}
	return text, fmt.Sprintf("OpenSubtitles movie reference file_id=%d", fileID)
}

type auditReferenceChoice struct {
	result *opensubtitles.SubtitleResult
	file   opensubtitles.SubtitleFile
}

func selectAuditReference(results []opensubtitles.SubtitleResult) (auditReferenceChoice, bool) {
	var best auditReferenceChoice
	found := false
	for i := range results {
		result := &results[i]
		if result.Attributes.ForeignPartsOnly {
			continue
		}
		for _, file := range result.Attributes.Files {
			if file.FileID <= 0 {
				continue
			}
			if !found || betterAuditReference(result, file, best.result, best.file) {
				best = auditReferenceChoice{result: result, file: file}
				found = true
			}
		}
	}
	return best, found
}

func betterAuditReference(candidate *opensubtitles.SubtitleResult, file opensubtitles.SubtitleFile, current *opensubtitles.SubtitleResult, currentFile opensubtitles.SubtitleFile) bool {
	candidateNonHI := !candidate.Attributes.HearingImpaired
	currentNonHI := current != nil && !current.Attributes.HearingImpaired
	if candidateNonHI != currentNonHI {
		return candidateNonHI
	}
	if candidate.Attributes.DownloadCount != current.Attributes.DownloadCount {
		return candidate.Attributes.DownloadCount > current.Attributes.DownloadCount
	}
	return file.FileID < currentFile.FileID
}

func fetchOpenSubtitlesTranscript(ctx context.Context, cfg *config.Config, client *opensubtitles.Client, tmdbID, season, episode int) (string, int, bool, error) {
	if cfg == nil || client == nil || tmdbID <= 0 {
		return "", 0, false, fmt.Errorf("OpenSubtitles or TMDB identity is unavailable")
	}
	languages := cfg.Subtitles.OpenSubtitlesLanguages
	if len(languages) == 0 {
		languages = []string{"en"}
	}
	results, err := client.Search(ctx, tmdbID, season, episode, languages)
	if err != nil {
		return "", 0, false, err
	}
	choice, ok := selectAuditReference(results)
	if !ok {
		return "", 0, false, fmt.Errorf("OpenSubtitles returned no full reference")
	}

	cachePath := filepath.Join(cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", choice.file.FileID))
	if text, readErr := readReferenceTranscript(cachePath); readErr == nil {
		return text, choice.file.FileID, true, nil
	}
	if err := client.DownloadToFile(ctx, choice.file.FileID, cachePath); err != nil {
		return "", 0, false, err
	}
	text, err := readReferenceTranscript(cachePath)
	if err != nil {
		return "", 0, false, err
	}
	return text, choice.file.FileID, false, nil
}

var (
	tmdbPathIDPattern  = regexp.MustCompile(`(?i)\[tmdbid-([0-9]+)\]`)
	seasonPathPattern  = regexp.MustCompile(`(?i)(?:^|/)season ([0-9]{1,2})(?:/|$)`)
	episodePathPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s([0-9]{1,2})e([0-9]{1,3})(?:-e([0-9]{1,3}))?(?:[^a-z0-9]|$)`)
)

// ReferenceTranscriptForPath reads Jellyfin's TMDB provider marker from a
// library path and fetches the corresponding movie or TV episode transcript.
// found is false when the path has no marker, preserving reference-free debug
// subtitle behavior for arbitrary input files.
func ReferenceTranscriptForPath(ctx context.Context, cfg *config.Config, client *opensubtitles.Client, path string) (transcript string, found bool, err error) {
	matches := tmdbPathIDPattern.FindAllStringSubmatch(filepath.ToSlash(path), -1)
	if len(matches) == 0 {
		return "", false, nil
	}
	tmdbID, err := strconv.Atoi(matches[0][1])
	if err != nil || tmdbID <= 0 {
		return "", true, fmt.Errorf("invalid TMDB ID in path")
	}
	for _, match := range matches[1:] {
		if match[1] != matches[0][1] {
			return "", true, fmt.Errorf("conflicting TMDB IDs in path")
		}
	}

	normalizedPath := filepath.ToSlash(path)
	seasonMatch := seasonPathPattern.FindStringSubmatch(normalizedPath)
	episodeMatch := episodePathPattern.FindStringSubmatch(filepath.Base(path))
	if len(seasonMatch) == 0 {
		text, _, _, err := fetchOpenSubtitlesTranscript(ctx, cfg, client, tmdbID, 0, 0)
		return text, true, err
	}
	if len(episodeMatch) == 0 {
		return "", true, fmt.Errorf("TV library path has no episode marker")
	}
	season, _ := strconv.Atoi(episodeMatch[1])
	folderSeason, _ := strconv.Atoi(seasonMatch[1])
	first, _ := strconv.Atoi(episodeMatch[2])
	last := first
	if episodeMatch[3] != "" {
		last, _ = strconv.Atoi(episodeMatch[3])
	}
	if season <= 0 || folderSeason != season || first <= 0 || last < first {
		return "", true, fmt.Errorf("invalid or inconsistent TV episode marker in path")
	}

	parts := make([]string, 0, last-first+1)
	for number := first; number <= last; number++ {
		text, _, _, err := fetchOpenSubtitlesTranscript(ctx, cfg, client, tmdbID, season, number)
		if err != nil {
			return "", true, fmt.Errorf("fetch S%02dE%02d reference: %w", season, number, err)
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n"), true, nil
}

func readReferenceTranscript(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	cleaned := opensubtitles.CleanSRT(string(data))
	text := strings.TrimSpace(srtutil.PlainText(srtutil.Parse(cleaned)))
	if text == "" {
		return "", fmt.Errorf("reference subtitle contains no cues")
	}
	return text, nil
}

func warnReferenceUnavailable(logger *slog.Logger, episodeKey, hint string, err error) {
	logger.Warn("subtitle audit reference unavailable",
		"event_type", "subtitle_audit_reference_unavailable",
		"error_hint", hint,
		"impact", "subtitle audit continues without external reference text",
		"error", err,
		"episode_key", episodeKey,
	)
}
