package contentid

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/textutil"
)

// srtTimestampRe matches SRT timestamp lines like "00:01:23,456 --> 00:01:25,789".
var srtTimestampRe = regexp.MustCompile(`^\d{2}:\d{2}:\d{2},\d{3}\s*-->`)

// readSRTText reads an SRT file and extracts only the text lines,
// skipping sequence numbers, timestamps, and empty lines.
func readSRTText(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	var sb strings.Builder
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip sequence numbers (pure digits).
		if isDigitsOnly(line) {
			continue
		}
		// Skip timestamp lines.
		if srtTimestampRe.MatchString(line) {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(line)
	}
	return sb.String()
}

// isDigitsOnly returns true if s contains only ASCII digits.
func isDigitsOnly(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// downloadReferences downloads reference subtitles from OpenSubtitles
// for each episode in the season. Returns a map of episode number to fingerprint.
func (h *Handler) downloadReferences(
	ctx context.Context,
	logger *slog.Logger,
	env *ripspec.Envelope,
) (map[int]*textutil.Fingerprint, error) {
	if h.osClient == nil {
		return nil, fmt.Errorf("OpenSubtitles client not configured")
	}

	tmdbID := env.Metadata.ID
	season := env.Metadata.SeasonNumber
	if season <= 0 {
		season = 1
	}

	// Determine candidate episode numbers from the envelope.
	// Use all episodes that exist in the envelope.
	var episodeNums []int
	for _, ep := range env.Episodes {
		if ep.Episode > 0 {
			episodeNums = append(episodeNums, ep.Episode)
		}
	}

	// If no resolved episodes, generate a range based on disc count.
	if len(episodeNums) == 0 {
		numRips := len(env.Episodes)
		if numRips == 0 {
			numRips = 4
		}
		for i := range numRips {
			episodeNums = append(episodeNums, i+1)
		}
	}

	refFPs := make(map[int]*textutil.Fingerprint)
	languages := []string{"en"}

	for _, epNum := range episodeNums {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		results, err := h.osClient.Search(ctx, tmdbID, season, epNum, languages)
		if err != nil {
			logger.Warn("OpenSubtitles search failed",
				"episode", epNum,
				"error", err,
			)
			logger.Debug("reference subtitle search",
				"decision_type", "opensubtitles_reference_search",
				"decision_result", "error",
				"decision_reason", fmt.Sprintf("S%02dE%02d search_error=%v", season, epNum, err),
			)
			continue
		}

		logger.Debug("reference subtitle search",
			"decision_type", "opensubtitles_reference_search",
			"decision_result", func() string {
				if len(results) == 0 {
					return "empty"
				}
				return "found"
			}(),
			"decision_reason", fmt.Sprintf("S%02dE%02d results=%d", season, epNum, len(results)),
		)

		if len(results) == 0 {
			logger.Info("no reference subtitles found",
				"decision_type", "reference_search",
				"decision_result", "none",
				"decision_reason", fmt.Sprintf("no results for S%02dE%02d", season, epNum),
			)
			continue
		}

		// Pick best candidate: prefer non-HI, highest download count.
		best := selectBestCandidate(logger, results)
		if best == nil || len(best.Attributes.Files) == 0 {
			continue
		}

		// Download to temp location and read.
		destDir := os.TempDir()
		destPath := fmt.Sprintf("%s/spindle_ref_s%02de%02d.srt", destDir, season, epNum)
		if err := h.osClient.DownloadToFile(ctx, best.Attributes.Files[0].FileID, destPath); err != nil {
			logger.Warn("reference subtitle download failed",
				"episode", epNum,
				"error", err,
			)
			continue
		}

		text := readSRTText(destPath)
		fp := textutil.NewFingerprint(text)
		if fp != nil {
			refFPs[epNum] = fp
			logger.Info("reference subtitle downloaded",
				"decision_type", "reference_download",
				"decision_result", "success",
				"decision_reason", fmt.Sprintf("S%02dE%02d from OpenSubtitles", season, epNum),
			)
		}

		// Clean up temp file.
		_ = os.Remove(destPath)
	}

	return refFPs, nil
}

// selectBestCandidate picks the best subtitle result, preferring non-HI
// subtitles with the highest download count.
func selectBestCandidate(logger *slog.Logger, results []opensubtitles.SubtitleResult) *opensubtitles.SubtitleResult {
	if len(results) == 0 {
		return nil
	}

	// Separate HI and non-HI.
	var nonHI, hi []opensubtitles.SubtitleResult
	for _, r := range results {
		if r.Attributes.HearingImpaired {
			hi = append(hi, r)
		} else {
			nonHI = append(nonHI, r)
		}
	}

	// Prefer non-HI, fall back to HI.
	candidates := nonHI
	usedHI := len(candidates) == 0
	if usedHI {
		candidates = hi
	}

	// Pick highest download count.
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Attributes.DownloadCount > best.Attributes.DownloadCount {
			best = &candidates[i]
		}
	}

	for _, r := range results {
		selected := r.Attributes.DownloadCount == best.Attributes.DownloadCount && r.Attributes.HearingImpaired == best.Attributes.HearingImpaired
		result := "skipped"
		if selected {
			result = "selected"
		}
		logger.Debug("content ID candidate",
			"decision_type", "contentid_candidates",
			"decision_result", result,
			"decision_reason", fmt.Sprintf("hi=%v downloads=%d used_hi_pool=%v", r.Attributes.HearingImpaired, r.Attributes.DownloadCount, usedHI),
		)
	}

	return best
}

// matchEpisodes builds a similarity matrix between disc fingerprints and
// reference fingerprints, runs the Hungarian algorithm, checks contiguity,
// and filters by threshold.
func (h *Handler) matchEpisodes(
	logger *slog.Logger,
	discFPs map[string]*textutil.Fingerprint,
	refFPs map[int]*textutil.Fingerprint,
	_ *ripspec.Envelope,
) []Match {
	// Build ordered key lists for stable indexing.
	discKeys := make([]string, 0, len(discFPs))
	for k := range discFPs {
		discKeys = append(discKeys, k)
	}
	sort.Strings(discKeys)

	refEps := make([]int, 0, len(refFPs))
	for ep := range refFPs {
		refEps = append(refEps, ep)
	}
	sort.Ints(refEps)

	// Build similarity matrix.
	scores := make([][]float64, len(discKeys))
	for i, dk := range discKeys {
		scores[i] = make([]float64, len(refEps))
		for j, ep := range refEps {
			scores[i][j] = textutil.CosineSimilarity(discFPs[dk], refFPs[ep])
			logger.Debug("episode similarity score",
				"decision_type", "contentid_matches",
				"decision_result", fmt.Sprintf("%s -> E%02d", dk, ep),
				"decision_reason", fmt.Sprintf("cosine=%.3f threshold=%.2f", scores[i][j], minSimilarityScore),
			)
		}
	}

	// Run assignment.
	assignments := hungarian(scores)

	// Build matches.
	var matches []Match
	for i, col := range assignments {
		if col < 0 || col >= len(refEps) {
			continue
		}
		score := scores[i][col]
		if score < minSimilarityScore {
			continue
		}
		matches = append(matches, Match{
			DiscKey:    discKeys[i],
			EpisodeNum: refEps[col],
			Score:      score,
		})
		logger.Info("episode matched",
			"decision_type", "episode_match",
			"decision_result", fmt.Sprintf("%s -> E%02d", discKeys[i], refEps[col]),
			"decision_reason", fmt.Sprintf("cosine similarity %.3f", score),
		)
	}

	// Check contiguity.
	if !checkContiguity(matches) {
		logger.Warn("matched episodes are non-contiguous",
			"event_type", "contiguity_check_failed",
			"error_hint", "episode numbers have gaps",
			"impact", "matches may be incorrect",
		)
	}

	return matches
}

// applyMatches updates episode records in the envelope with matched episode
// numbers and confidence scores. Flags unresolved episodes for review.
func (h *Handler) applyMatches(
	logger *slog.Logger,
	env *ripspec.Envelope,
	matches []Match,
	item *queue.Item,
) {
	// Index matches by disc key.
	matchMap := make(map[string]Match, len(matches))
	for _, m := range matches {
		matchMap[m.DiscKey] = m
	}

	unresolvedCount := 0
	lowConfCount := 0

	for i := range env.Episodes {
		ep := &env.Episodes[i]
		m, ok := matchMap[ep.Key]
		if !ok {
			unresolvedCount++
			continue
		}

		ep.Episode = m.EpisodeNum
		ep.MatchConfidence = m.Score
		ep.Key = ripspec.EpisodeKey(ep.Season, m.EpisodeNum)

		if m.Score < lowConfidenceReviewThreshold {
			lowConfCount++
			logger.Warn("low confidence episode match",
				"event_type", "low_confidence_match",
				"error_hint", fmt.Sprintf("%s matched E%02d with score %.3f", ep.Key, m.EpisodeNum, m.Score),
				"impact", "match may be incorrect",
			)
		}
	}

	if unresolvedCount > 0 {
		item.AppendReviewReason(
			fmt.Sprintf("Episode ID: %d of %d episodes unresolved", unresolvedCount, len(env.Episodes)),
		)
		logger.Warn("unresolved episodes remain",
			"event_type", "unresolved_episodes",
			"error_hint", fmt.Sprintf("%d episodes could not be matched", unresolvedCount),
			"impact", "episodes remain with placeholder keys",
		)
	}

	if lowConfCount > 0 {
		item.AppendReviewReason(
			fmt.Sprintf("Episode ID: %d matches below confidence threshold %.2f", lowConfCount, lowConfidenceReviewThreshold),
		)
	}
}

// checkContiguity verifies that matched episode numbers form a contiguous
// sequence with no gaps. Returns true if contiguous or if there are fewer
// than 2 matches.
func checkContiguity(matches []Match) bool {
	if len(matches) < 2 {
		return true
	}

	eps := make([]int, len(matches))
	for i, m := range matches {
		eps[i] = m.EpisodeNum
	}
	sort.Ints(eps)

	for i := 1; i < len(eps); i++ {
		if eps[i]-eps[i-1] != 1 {
			return false
		}
	}
	return true
}
