package subtitle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
)

// maxSubtitleCandidates bounds how many downloads one episode may burn from
// the OpenSubtitles quota before the job skips.
const maxSubtitleCandidates = 3

// subtitleCandidate is one downloadable (or already-downloaded) subtitle in
// adoption preference order.
type subtitleCandidate struct {
	FileID    int
	LocalPath string // non-empty when the file is already on disk
	Language  string
	Origin    string // "contentid_reference" | "opensubtitles"
}

func (c subtitleCandidate) label() string {
	return fmt.Sprintf("%s file_id=%d", c.Origin, c.FileID)
}

// listSubtitleCandidates returns adoption candidates for the job in
// preference order, or a skip reason when the item cannot have any.
func (h *Handler) listSubtitleCandidates(ctx context.Context, sess *stage.Session, key string) ([]subtitleCandidate, string) {
	if h.osClient == nil {
		return nil, "OpenSubtitles is not configured"
	}
	meta := sess.Env.Metadata
	if meta.ID <= 0 {
		return nil, "TMDB identity is unavailable"
	}

	season, episode := 0, 0
	var candidates []subtitleCandidate
	switch strings.ToLower(strings.TrimSpace(meta.MediaType)) {
	case "movie":
	case "tv":
		ep := sess.Env.EpisodeByKey(key)
		if ep == nil || ep.Season <= 0 || ep.Episode <= 0 {
			return nil, "TV episode identity is unresolved"
		}
		if ep.EpisodeLast() > ep.Episode {
			return nil, "multi-episode rip has no single-episode subtitle source"
		}
		season, episode = ep.Season, ep.Episode
		if ref, ok := h.contentIDReference(sess, ep); ok {
			candidates = append(candidates, ref)
		}
	default:
		return nil, "media type has no subtitle download policy"
	}

	results, err := h.osClient.Search(ctx, meta.ID, season, episode, h.searchLanguages())
	if err != nil {
		sess.Logger.Warn("OpenSubtitles candidate search failed",
			"event_type", "subtitle_candidate_search_failed",
			"error_hint", "opensubtitles api error",
			"impact", "search candidates unavailable for this episode",
			"error", err,
			"episode_key", key,
		)
		if len(candidates) == 0 {
			return nil, "OpenSubtitles search failed"
		}
		return candidates, ""
	}

	seen := make(map[int]bool, len(candidates))
	for _, c := range candidates {
		seen[c.FileID] = true
	}
	for _, c := range rankSearchCandidates(results, season, episode) {
		if seen[c.FileID] {
			continue
		}
		candidates = append(candidates, c)
		if len(candidates) >= maxSubtitleCandidates {
			break
		}
	}
	if len(candidates) == 0 {
		return nil, "OpenSubtitles returned no usable candidates"
	}
	return candidates, ""
}

// contentIDReference reuses the reference subtitle episode identification
// already downloaded into staging for this episode, when exactly one exists.
func (h *Handler) contentIDReference(sess *stage.Session, ep *ripspec.Episode) (subtitleCandidate, bool) {
	stagingRoot, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		return subtitleCandidate{}, false
	}
	pattern := filepath.Join(stagingRoot, "contentid", "references", fmt.Sprintf("s%02de%02d-*.srt", ep.Season, ep.Episode))
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) != 1 {
		return subtitleCandidate{}, false
	}
	name := strings.TrimSuffix(filepath.Base(matches[0]), ".srt")
	fileID, _ := strconv.Atoi(name[strings.LastIndex(name, "-")+1:])
	return subtitleCandidate{FileID: fileID, LocalPath: matches[0], Language: "en", Origin: "contentid_reference"}, true
}

// rankSearchCandidates orders downloadable search results: full (not
// foreign-parts-only) tracks, exact-episode-marker names first for TV, then
// non-hearing-impaired, then by download count, with file ID as the
// deterministic tiebreak. TV candidates whose release/file names carry a
// marker for a DIFFERENT episode are dropped outright: OpenSubtitles episode
// metadata is sometimes wrong, and a conflicting name is the one signal of a
// mislabeled upload available before download (mirrors contentid's reference
// vetting, which stage isolation keeps us from importing).
func rankSearchCandidates(results []opensubtitles.SubtitleResult, season, episode int) []subtitleCandidate {
	type ranked struct {
		candidate   subtitleCandidate
		exactMarker bool
		nonHI       bool
		downloads   int
	}
	var all []ranked
	for i := range results {
		attrs := results[i].Attributes
		if attrs.ForeignPartsOnly {
			continue
		}
		for _, file := range attrs.Files {
			if file.FileID <= 0 {
				continue
			}
			exactMarker := false
			if season > 0 && episode > 0 {
				// The file name is the truth-teller for pack uploads whose
				// release name spans a range (e.g. "S01E01-06"): vet each
				// file by its own name, falling back to the release name.
				markers := episodeMarkers(strings.ToLower(file.FileName))
				if len(markers) == 0 {
					markers = episodeMarkers(strings.ToLower(attrs.Release))
				}
				exactMarker = markers[[2]int{season, episode}]
				if len(markers) > 0 && !exactMarker {
					continue
				}
			}
			all = append(all, ranked{
				candidate:   subtitleCandidate{FileID: file.FileID, Language: attrs.Language, Origin: "opensubtitles"},
				exactMarker: exactMarker,
				nonHI:       !attrs.HearingImpaired,
				downloads:   attrs.DownloadCount,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].exactMarker != all[j].exactMarker {
			return all[i].exactMarker
		}
		if all[i].nonHI != all[j].nonHI {
			return all[i].nonHI
		}
		if all[i].downloads != all[j].downloads {
			return all[i].downloads > all[j].downloads
		}
		return all[i].candidate.FileID < all[j].candidate.FileID
	})
	candidates := make([]subtitleCandidate, len(all))
	for i, r := range all {
		candidates[i] = r.candidate
	}
	return candidates
}

// episodeMarkerPatterns match SxxEyy / NxM episode markers in release and
// file names.
var episodeMarkerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[^a-z0-9])s0*(\d{1,2})e0*(\d{1,3})(?:$|[^a-z0-9])`),
	regexp.MustCompile(`(?:^|[^a-z0-9])0*(\d{1,2})[x.]0*(\d{1,3})(?:$|[^a-z0-9])`),
}

// episodeMarkers returns every (season, episode) marker found in lowercased
// candidate name text.
func episodeMarkers(text string) map[[2]int]bool {
	markers := make(map[[2]int]bool)
	for _, pattern := range episodeMarkerPatterns {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			season, seasonErr := strconv.Atoi(match[1])
			episode, episodeErr := strconv.Atoi(match[2])
			if seasonErr == nil && episodeErr == nil {
				markers[[2]int{season, episode}] = true
			}
		}
	}
	return markers
}

func (h *Handler) searchLanguages() []string {
	if h.cfg != nil && len(h.cfg.Subtitles.OpenSubtitlesLanguages) > 0 {
		return h.cfg.Subtitles.OpenSubtitlesLanguages
	}
	return []string{"en"}
}

// fetchCandidate returns a local path for the candidate's SRT, downloading
// through the shared quota-aware cache when it is not already on disk.
func (h *Handler) fetchCandidate(ctx context.Context, candidate subtitleCandidate) (string, error) {
	if candidate.LocalPath != "" {
		return candidate.LocalPath, nil
	}
	cachePath := filepath.Join(h.cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", candidate.FileID))
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}
	if err := h.osClient.DownloadToFile(ctx, candidate.FileID, cachePath); err != nil {
		return "", err
	}
	return cachePath, nil
}
