package ripping

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

const (
	minPrimaryRuntimeSeconds = 20 * 60
	durationToleranceSeconds = 2
)

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
		if logger != nil {
			result := "selected"
			reason := "media_type_tv"
			if len(ids) == 0 {
				result = "skipped"
				reason = "no_episode_titles"
			}
			attrs := []logging.Attr{
				logging.String(logging.FieldDecisionType, "episode_title_selection"),
				logging.String("decision_result", result),
				logging.String("decision_reason", reason),
				logging.String("decision_options", "select, skip"),
				logging.Int("selected_count", len(ids)),
			}
			for _, id := range ids {
				attrs = append(attrs, logging.String(fmt.Sprintf("selected_%d", id), fmt.Sprintf("%d", id)))
			}
			logger.Info(
				"episode title selection decision",
				logging.Args(attrs...)...,
			)
		}
		if len(ids) == 0 {
			return nil
		}
		sort.Ints(ids)
		return ids
	}
	if selection, ok := ChoosePrimaryTitle(env.Titles); ok {
		if logger != nil {
			_, _, candidates, rejects := PrimaryTitleDecisionSummary(env.Titles)
			attrs := []logging.Attr{
				logging.String(logging.FieldDecisionType, "primary_title"),
				logging.String("decision_result", "selected"),
				logging.String("decision_reason", "primary_title_selector"),
				logging.String("decision_options", "select, skip"),
				logging.String("decision_selected", fmt.Sprintf("%d:%ds", selection.ID, selection.Duration)),
				logging.Int("candidate_count", len(candidates)),
				logging.Int("rejected_count", len(rejects)),
				logging.Int("title_id", selection.ID),
				logging.Int("duration_seconds", selection.Duration),
				logging.String("title_name", strings.TrimSpace(selection.Name)),
			}
			attrs = appendDecisionLines(attrs, "candidate", candidates)
			attrs = appendDecisionLines(attrs, "rejected", rejects)
			logger.Info("primary title decision", logging.Args(attrs...)...)
		}
		return []int{selection.ID}
	}
	if logger != nil {
		logger.Info(
			"primary title decision",
			logging.String(logging.FieldDecisionType, "primary_title"),
			logging.String("decision_result", "skipped"),
			logging.String("decision_reason", "no_candidates"),
			logging.String("decision_options", "select, skip"),
		)
	}
	return nil
}

func appendDecisionLines(attrs []logging.Attr, prefix string, items []string) []logging.Attr {
	for idx, item := range items {
		key := fmt.Sprintf("%s_%d", prefix, idx+1)
		if id, ok := decisionItemID(item); ok {
			key = fmt.Sprintf("%s_%d", prefix, id)
		}
		attrs = append(attrs, logging.String(key, item))
	}
	return attrs
}

func decisionItemID(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) == 0 {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, false
	}
	return id, true
}

// ChoosePrimaryTitle exposes the selector for other packages (e.g. logging during identification).
func ChoosePrimaryTitle(titles []ripspec.Title) (ripspec.Title, bool) {
	if len(titles) == 0 {
		return ripspec.Title{}, false
	}

	candidates := make([]ripspec.Title, 0, len(titles))
	for _, t := range titles {
		if t.ID < 0 || t.Duration <= 0 {
			continue
		}
		candidates = append(candidates, t)
	}
	if len(candidates) == 0 {
		return ripspec.Title{}, false
	}

	// Early check for Disney/Pixar multi-language discs.
	// These have 00800.mpls (English), 00801.mpls (Spanish), 00802.mpls (French) with
	// the English version often being slightly shorter due to language-specific credits.
	// If we find feature-length 800-series playlists, prefer 00800.mpls before duration filtering.
	if preferred := filterPreferredPlaylistFeatureLength(candidates, minPrimaryRuntimeSeconds); len(preferred) > 0 {
		candidates = preferred
	}

	// Prefer feature-length runtimes within a small tolerance window.
	maxDuration := 0
	for _, t := range candidates {
		if t.Duration > maxDuration {
			maxDuration = t.Duration
		}
	}
	window := make([]ripspec.Title, 0, len(candidates))
	for _, t := range candidates {
		if t.Duration >= maxDuration-durationToleranceSeconds {
			window = append(window, t)
		}
	}
	featureLength := window
	tmp := make([]ripspec.Title, 0, len(window))
	for _, t := range window {
		if t.Duration >= minPrimaryRuntimeSeconds {
			tmp = append(tmp, t)
		}
	}
	if len(tmp) > 0 {
		featureLength = tmp
	}

	// Prefer titles with chapter metadata.
	withChapters := bestByInt(featureLength, func(t ripspec.Title) int { return t.Chapters })
	if len(withChapters) > 0 {
		featureLength = withChapters
	}

	// Prefer MPLS playlists over raw M2TS entries.
	mplsOnly := make([]ripspec.Title, 0, len(featureLength))
	for _, t := range featureLength {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(t.Playlist)), ".mpls") {
			mplsOnly = append(mplsOnly, t)
		}
	}
	if len(mplsOnly) > 0 {
		featureLength = mplsOnly
	}

	// Prefer playlists with more segments (helps dodge dummy/short playlists).
	withSegments := bestByInt(featureLength, func(t ripspec.Title) int { return t.SegmentCount })
	if len(withSegments) > 0 {
		featureLength = withSegments
	}

	// Prefer the most common fingerprint if duplicates exist.
	fingerprintFreq := make(map[string]int)
	for _, t := range titles {
		fp := strings.TrimSpace(t.TitleHash)
		if fp != "" {
			fingerprintFreq[fp]++
		}
	}
	bestFreq := 0
	for _, t := range featureLength {
		if freq := fingerprintFreq[strings.TrimSpace(t.TitleHash)]; freq > bestFreq {
			bestFreq = freq
		}
	}
	if bestFreq > 1 {
		filtered := make([]ripspec.Title, 0, len(featureLength))
		for _, t := range featureLength {
			if fingerprintFreq[strings.TrimSpace(t.TitleHash)] == bestFreq {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			featureLength = filtered
		}
	}

	sort.Slice(featureLength, func(i, j int) bool {
		left := featureLength[i]
		right := featureLength[j]
		if left.Duration == right.Duration {
			return left.ID < right.ID
		}
		return left.Duration > right.Duration
	})
	return featureLength[0], true
}

// PrimaryTitleDecisionSummary returns the primary selection plus candidate and rejection summaries.
func PrimaryTitleDecisionSummary(titles []ripspec.Title) (ripspec.Title, bool, []string, []string) {
	selection, ok := ChoosePrimaryTitle(titles)
	candidates := make([]string, 0, len(titles))
	rejects := make([]string, 0)
	for _, t := range titles {
		if t.ID < 0 {
			rejects = append(rejects, fmt.Sprintf("%d:invalid_id", t.ID))
			continue
		}
		if t.Duration <= 0 {
			rejects = append(rejects, fmt.Sprintf("%d:duration<=0", t.ID))
			continue
		}
		candidates = append(candidates, fmt.Sprintf("%d:%ds ch=%d seg=%d playlist=%s", t.ID, t.Duration, t.Chapters, t.SegmentCount, strings.TrimSpace(t.Playlist)))
	}
	sort.Strings(candidates)
	sort.Strings(rejects)
	return selection, ok, candidates, rejects
}

func bestByInt(list []ripspec.Title, score func(ripspec.Title) int) []ripspec.Title {
	best := 0
	for _, t := range list {
		if v := score(t); v > best {
			best = v
		}
	}
	if best == 0 {
		return nil
	}
	out := make([]ripspec.Title, 0, len(list))
	for _, t := range list {
		if score(t) == best {
			out = append(out, t)
		}
	}
	return out
}

// filterPreferredPlaylistFeatureLength returns feature-length titles with the preferred 800-series playlist.
// Disney/Pixar discs use 00800.mpls for English, 00801 for Spanish, 00802 for French.
// The English version is often slightly shorter due to language-specific credits.
// Only applies when multiple feature-length 800-series playlists exist (indicating a multi-language disc).
// Returns empty slice if not a multi-language disc pattern.
func filterPreferredPlaylistFeatureLength(titles []ripspec.Title, minRuntime int) []ripspec.Title {
	// Collect feature-length titles with 800-series playlists
	type scored struct {
		title ripspec.Title
		num   int
	}
	var candidates []scored
	for _, t := range titles {
		if t.Duration < minRuntime {
			continue
		}
		pl := strings.ToLower(strings.TrimSpace(t.Playlist))
		// Match 008XX.mpls pattern (800-899 range)
		if strings.HasPrefix(pl, "008") && strings.HasSuffix(pl, ".mpls") {
			numStr := strings.TrimPrefix(pl, "00")
			numStr = strings.TrimSuffix(numStr, ".mpls")
			if num, err := strconv.Atoi(numStr); err == nil && num >= 800 && num < 900 {
				candidates = append(candidates, scored{title: t, num: num})
			}
		}
	}

	// Only apply if we have multiple 800-series playlists (indicating multi-language disc)
	if len(candidates) < 2 {
		return nil
	}

	// Check if we have different playlist numbers (e.g., 800, 801, 802)
	playlistNums := make(map[int]bool)
	for _, c := range candidates {
		playlistNums[c.num] = true
	}
	if len(playlistNums) < 2 {
		// All same playlist number, not a multi-language pattern
		return nil
	}

	// Find minimum playlist number (00800 = English)
	minNum := candidates[0].num
	for _, c := range candidates[1:] {
		if c.num < minNum {
			minNum = c.num
		}
	}

	// Return all titles with that minimum playlist number
	result := make([]ripspec.Title, 0)
	for _, c := range candidates {
		if c.num == minNum {
			result = append(result, c.title)
		}
	}
	return result
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
