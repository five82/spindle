package contentid

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
)

type candidateChoice struct {
	Result      *opensubtitles.SubtitleResult
	Score       float64
	Suspect     bool
	Reason      string
	Fallback    bool
	Diagnostics candidateDiagnostics
}

type candidateDiagnostics struct {
	HasTargetTitle     bool
	HasExactMarker     bool
	HasConflictingTitle bool
	MultiEpisodePack   bool
	NonHI              bool
}

// fetchReferenceFingerprints fetches OpenSubtitles reference subtitles for the
// requested episodes. The loop is intentionally sequential because the shared
// OpenSubtitles client rate-limits requests internally.
func (h *Handler) fetchReferenceFingerprints(
	ctx context.Context,
	item *queue.Item,
	seasonNum int,
	tmdbID int,
	season *tmdb.Season,
	episodes []int,
	cache map[int]referenceFingerprint,
) ([]referenceFingerprint, error) {
	if h.osClient == nil {
		return nil, fmt.Errorf("opensubtitles client not configured")
	}
	if len(episodes) == 0 {
		return nil, nil
	}
	logger := stage.LoggerFromContext(ctx)
	languages := []string{"en"}
	if h.cfg != nil && len(h.cfg.Subtitles.OpenSubtitlesLanguages) > 0 {
		languages = append([]string(nil), h.cfg.Subtitles.OpenSubtitlesLanguages...)
	}
	stagingRoot, err := item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return nil, err
	}
	refDir := filepath.Join(stagingRoot, "contentid", "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		return nil, err
	}
	unique := make([]int, 0, len(episodes))
	seen := make(map[int]struct{}, len(episodes))
	for _, ep := range episodes {
		if _, ok := seen[ep]; ok {
			continue
		}
		seen[ep] = struct{}{}
		unique = append(unique, ep)
	}
	sort.Ints(unique)
	refs := make([]referenceFingerprint, 0, len(unique))
	for _, epNum := range unique {
		if ref, ok := cache[epNum]; ok {
			refs = append(refs, ref)
			continue
		}
		results, err := h.osClient.Search(ctx, tmdbID, seasonNum, epNum, languages)
		if err != nil {
			return nil, fmt.Errorf("opensubtitles search s%02de%02d: %w", seasonNum, epNum, err)
		}
		if len(results) == 0 {
			continue
		}
		choice := selectReferenceCandidate(results, season, seasonNum, epNum)
		if choice.Result == nil || len(choice.Result.Attributes.Files) == 0 {
			continue
		}
		logReferenceSelection(logger, seasonNum, epNum, choice)
		fileID := choice.Result.Attributes.Files[0].FileID
		destPath := filepath.Join(refDir, fmt.Sprintf("s%02de%02d-%d.srt", seasonNum, epNum, fileID))
		if err := h.osClient.DownloadToFile(ctx, fileID, destPath); err != nil {
			return nil, fmt.Errorf("opensubtitles download s%02de%02d file %d: %w", seasonNum, epNum, fileID, err)
		}
		text, err := loadPlainText(destPath)
		if err != nil {
			return nil, fmt.Errorf("normalize opensubtitles payload: %w", err)
		}
		fp := textutil.NewFingerprint(text)
		if fp == nil {
			continue
		}
		ref := referenceFingerprint{
			EpisodeNumber:  epNum,
			Title:          episodeTitle(season, epNum),
			Vector:         fp,
			RawVector:      fp,
			FileID:         fileID,
			Language:       choice.Result.Attributes.Language,
			CachePath:      destPath,
			Suspect:        choice.Suspect,
			SuspectReason:  choice.Reason,
			CandidateScore: choice.Score,
		}
		cache[epNum] = ref
		refs = append(refs, ref)
	}
	return refs, nil
}

func selectReferenceCandidate(results []opensubtitles.SubtitleResult, season *tmdb.Season, seasonNum, episodeNum int) candidateChoice {
	if len(results) == 0 {
		return candidateChoice{}
	}
	evals := make([]candidateChoice, 0, len(results))
	for i := range results {
		score, diag := scoreSubtitleCandidate(results[i], season, seasonNum, episodeNum)
		evals = append(evals, candidateChoice{
			Result:       &results[i],
			Score:        score,
			Suspect:      isSuspectCandidate(diag, score),
			Reason:       suspectReason(diag, score),
			Diagnostics:  diag,
		})
	}
	sort.Slice(evals, func(i, j int) bool {
		if evals[i].Score != evals[j].Score {
			return evals[i].Score > evals[j].Score
		}
		return evals[i].Result.ID < evals[j].Result.ID
	})
	for i := range evals {
		if !evals[i].Suspect {
			if i > 0 {
				evals[i].Fallback = true
				evals[i].Reason = "fallback_to_non_suspect_candidate"
			}
			return evals[i]
		}
	}
	return evals[0]
}

func scoreSubtitleCandidate(result opensubtitles.SubtitleResult, season *tmdb.Season, seasonNum, episodeNum int) (float64, candidateDiagnostics) {
	textRaw := strings.ToLower(candidateSearchText(result))
	textNorm := normalizeCandidateText(textRaw)
	targetTitle := normalizeCandidateText(episodeTitle(season, episodeNum))
	diag := candidateDiagnostics{
		HasTargetTitle:      targetTitle != "" && strings.Contains(textNorm, targetTitle),
		HasExactMarker:      hasExactEpisodeMarker(textRaw, seasonNum, episodeNum),
		HasConflictingTitle: containsOtherEpisodeTitle(textNorm, season, episodeNum),
		MultiEpisodePack:    looksLikeMultiEpisodePack(textRaw, seasonNum),
		NonHI:               !result.Attributes.HearingImpaired,
	}
	score := math.Log10(float64(result.Attributes.DownloadCount)+1) * 12
	if diag.NonHI {
		score += 20
	}
	if diag.HasExactMarker {
		score += 35
	}
	if diag.HasTargetTitle {
		score += 70
	}
	if diag.HasConflictingTitle {
		score -= 140
	}
	if diag.MultiEpisodePack {
		score -= 90
	}
	return score, diag
}

func isSuspectCandidate(diag candidateDiagnostics, score float64) bool {
	switch {
	case diag.HasConflictingTitle:
		return true
	case !diag.HasTargetTitle && !diag.HasExactMarker:
		return true
	case diag.MultiEpisodePack && !diag.HasTargetTitle:
		return true
	case score < 0:
		return true
	default:
		return false
	}
}

func suspectReason(diag candidateDiagnostics, score float64) string {
	switch {
	case diag.HasConflictingTitle:
		return "candidate_names_different_episode_title"
	case !diag.HasTargetTitle && !diag.HasExactMarker:
		return "candidate_lacks_target_title_and_episode_marker"
	case diag.MultiEpisodePack && !diag.HasTargetTitle:
		return "candidate_looks_like_multi_episode_pack"
	case score < 0:
		return "candidate_score_below_trust_floor"
	default:
		return ""
	}
}

func logReferenceSelection(logger *slog.Logger, seasonNum, episodeNum int, choice candidateChoice) {
	if logger == nil || choice.Result == nil {
		return
	}
	result := "selected"
	reason := "highest_scoring_candidate"
	if choice.Fallback {
		result = "fallback_selected"
		reason = choice.Reason
	} else if choice.Suspect {
		result = "selected_suspect"
		reason = choice.Reason
	}
	logger.Info("content ID reference candidate selected",
		"decision_type", logs.DecisionReferenceSearch,
		"decision_result", result,
		"decision_reason", reason,
		"season", seasonNum,
		"episode", episodeNum,
		"subtitle_result_id", choice.Result.ID,
		"candidate_score", choice.Score,
		"target_title_match", choice.Diagnostics.HasTargetTitle,
		"exact_episode_marker", choice.Diagnostics.HasExactMarker,
		"conflicting_episode_title", choice.Diagnostics.HasConflictingTitle,
		"multi_episode_pack", choice.Diagnostics.MultiEpisodePack,
		"reference_suspect", choice.Suspect,
	)
}

func candidateSearchText(result opensubtitles.SubtitleResult) string {
	parts := make([]string, 0, len(result.Attributes.Files)+1)
	if release := strings.TrimSpace(result.Attributes.Release); release != "" {
		parts = append(parts, release)
	}
	for _, file := range result.Attributes.Files {
		if name := strings.TrimSpace(file.FileName); name != "" {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, " ")
}

func normalizeCandidateText(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func hasExactEpisodeMarker(text string, seasonNum, episodeNum int) bool {
	text = strings.ToLower(text)
	patterns := []string{
		fmt.Sprintf("s%02de%02d", seasonNum, episodeNum),
		fmt.Sprintf("s%de%d", seasonNum, episodeNum),
		fmt.Sprintf("%dx%02d", seasonNum, episodeNum),
		fmt.Sprintf("%dx%d", seasonNum, episodeNum),
		fmt.Sprintf("%d.%02d", seasonNum, episodeNum),
		fmt.Sprintf("%d.%d", seasonNum, episodeNum),
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func containsOtherEpisodeTitle(text string, season *tmdb.Season, targetEpisode int) bool {
	if season == nil {
		return false
	}
	for _, ep := range season.Episodes {
		if ep.EpisodeNumber == targetEpisode {
			continue
		}
		title := normalizeCandidateText(ep.Name)
		if title == "" {
			continue
		}
		if strings.Contains(text, title) {
			return true
		}
	}
	return false
}

func looksLikeMultiEpisodePack(text string, seasonNum int) bool {
	text = strings.ToLower(text)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(fmt.Sprintf(`s0?%d[ ._-]*e\d{1,2}\s*[-–]\s*(?:e)?\d{1,2}`, seasonNum)),
		regexp.MustCompile(fmt.Sprintf(`\b0?%d[x.]\d{1,2}\s*[-–]\s*\d{1,2}\b`, seasonNum)),
	}
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func episodeTitle(season *tmdb.Season, episode int) string {
	for _, ep := range season.Episodes {
		if ep.EpisodeNumber == episode {
			return strings.TrimSpace(ep.Name)
		}
	}
	return ""
}

func applyIDFWeighting(rips []ripFingerprint, refs []referenceFingerprint) {
	if len(refs) < 2 {
		return
	}
	corpus := &textutil.Corpus{}
	for _, ref := range refs {
		corpus.Add(ref.RawVector)
	}
	idf := corpus.IDF()
	if len(idf) == 0 {
		return
	}
	for i := range rips {
		rips[i].Vector = rips[i].RawVector.WithIDF(idf)
	}
	for i := range refs {
		refs[i].Vector = refs[i].RawVector.WithIDF(idf)
	}
}
