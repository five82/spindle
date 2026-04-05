package contentid

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/tmdb"
)

// fetchReferenceFingerprints fetches OpenSubtitles reference subtitles for the
// requested episodes. The loop is intentionally sequential: the shared
// opensubtitles.Client enforces a 3 s floor between requests via an
// unsynchronized lastCall field, so parallel calls would either race on that
// field or — if a mutex were added — serialize back to the same wall-clock
// latency as this loop. Keep it sequential.
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
		best := selectBestCandidate(results)
		if best == nil || len(best.Attributes.Files) == 0 {
			continue
		}
		fileID := best.Attributes.Files[0].FileID
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
			EpisodeNumber: epNum,
			Title:         episodeTitle(season, epNum),
			Vector:        fp,
			RawVector:     fp,
			FileID:        fileID,
			Language:      best.Attributes.Language,
			CachePath:     destPath,
		}
		cache[epNum] = ref
		refs = append(refs, ref)
	}
	return refs, nil
}

func selectBestCandidate(results []opensubtitles.SubtitleResult) *opensubtitles.SubtitleResult {
	if len(results) == 0 {
		return nil
	}
	var nonHI, hi []opensubtitles.SubtitleResult
	for _, r := range results {
		if r.Attributes.HearingImpaired {
			hi = append(hi, r)
		} else {
			nonHI = append(nonHI, r)
		}
	}
	candidates := nonHI
	if len(candidates) == 0 {
		candidates = hi
	}
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Attributes.DownloadCount > best.Attributes.DownloadCount {
			best = &candidates[i]
		}
	}
	return best
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
