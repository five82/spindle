package subtitle

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/stage"
)

// maxSubtitleCandidates bounds how many downloads one episode may burn from
// the OpenSubtitles quota before the job skips.
const maxSubtitleCandidates = 3

// subtitleCandidate is one downloadable subtitle in adoption preference order.
type subtitleCandidate struct {
	FileID   int
	Language string
}

func (c subtitleCandidate) label() string {
	return fmt.Sprintf("opensubtitles file_id=%d", c.FileID)
}

// listSubtitleCandidates returns adoption candidates for the job in
// preference order, or a skip reason when the item cannot have any.
func (h *Handler) listSubtitleCandidates(ctx context.Context, sess *stage.Session, key, videoPath string) ([]subtitleCandidate, string) {
	if h.osClient == nil {
		return nil, "OpenSubtitles is not configured"
	}
	meta := sess.Env.Metadata
	if meta.ID <= 0 {
		return nil, "TMDB identity is unavailable"
	}

	season, episode := 0, 0
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
	default:
		return nil, "media type has no subtitle download policy"
	}

	logger := sess.Logger.With("episode_key", key)
	candidates, err := h.searchCandidates(ctx, logger, meta.ID, season, episode, videoPath, meta.DiscSource)
	if err != nil {
		logger.Warn("OpenSubtitles candidate search failed",
			"event_type", "subtitle_candidate_search_failed",
			"error_hint", "opensubtitles api error",
			"impact", "search candidates unavailable for this episode",
			"error", err,
		)
		return nil, "OpenSubtitles search failed"
	}
	if len(candidates) == 0 {
		return nil, "OpenSubtitles returned no usable candidates"
	}
	return candidates, ""
}

// searchCandidates is the one adoption attempt set for a title: the
// OpenSubtitles search ranked against the actual video, capped at
// maxSubtitleCandidates. The daemon and `spindle subtitle` both use it so the
// same title always tries the same files.
func (h *Handler) searchCandidates(ctx context.Context, logger *slog.Logger, tmdbID, season, episode int, videoPath, discSource string) ([]subtitleCandidate, error) {
	results, err := h.osClient.Search(ctx, tmdbID, season, episode, h.searchLanguages())
	if err != nil {
		return nil, err
	}
	profile := subtitleSourceProfile(ctx, logger, videoPath, discSource)
	candidates := rankSearchCandidatesForSource(results, season, episode, profile)
	if len(candidates) > maxSubtitleCandidates {
		candidates = candidates[:maxSubtitleCandidates]
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	fileIDs := make([]int, len(candidates))
	for i, candidate := range candidates {
		fileIDs[i] = candidate.FileID
	}
	logger.Info("subtitle candidate attempt set selected",
		"decision_type", logs.DecisionSubtitleCandidateRanking,
		"decision_result", "selected",
		"decision_reason", "source affinity orders release/file matches before generic or conflicting candidates",
		"source_profile", profile.class,
		"disc_source", discSource,
		"input_resolution", profile.resolution(),
		"candidate_file_ids", fmt.Sprint(fileIDs),
		"attempt_count", len(candidates),
	)
	return candidates, nil
}

// subtitleSource describes the release class expected for the actual video
// being subtitled. UHD comes from the media itself; DiscSource distinguishes
// SD DVD from non-UHD Blu-ray when the resolution alone is ambiguous.
type subtitleSource struct {
	class         string
	width, height int
}

func (s subtitleSource) resolution() string {
	if s.width == 0 || s.height == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%dx%d", s.width, s.height)
}

func subtitleSourceProfile(ctx context.Context, logger *slog.Logger, videoPath, discSource string) subtitleSource {
	var source subtitleSource
	if videoPath != "" {
		probe, err := inspectSubtitleMedia(ctx, "", videoPath)
		if err != nil {
			logger.Warn("subtitle source profile probe failed",
				"event_type", "subtitle_source_profile_probe_failed",
				"error_hint", "ffprobe unavailable",
				"impact", "candidate ranking uses disc source or unknown profile",
				"error", err,
				"video_path", videoPath,
			)
		} else {
			for _, stream := range probe.Streams {
				if stream.CodecType == "video" && stream.Width*stream.Height > source.width*source.height {
					source.width, source.height = stream.Width, stream.Height
				}
			}
		}
	}
	if source.width >= 3840 || source.height >= 2160 {
		source.class = "uhd"
		return source
	}
	switch strings.ToLower(strings.TrimSpace(discSource)) {
	case "dvd":
		source.class = "dvd"
	case "bluray", "blu-ray":
		source.class = "bluray"
	case "":
		if source.height > 0 && source.height <= 576 {
			source.class = "dvd"
		} else if source.height > 576 && source.height <= 1080 {
			source.class = "bluray"
		}
	default:
		source.class = "unknown"
	}
	if source.class == "" {
		source.class = "unknown"
	}
	return source
}

// rankSearchCandidates preserves the default deterministic order for callers
// without a known video source.
func rankSearchCandidates(results []opensubtitles.SubtitleResult, season, episode int) []subtitleCandidate {
	return rankSearchCandidatesForSource(results, season, episode, subtitleSource{class: "unknown"})
}

// rankSearchCandidatesForSource orders downloadable search results: full
// (not partial-display) tracks first, then exact episode markers for TV,
// source release affinity, non-hearing-impaired, download count, and file ID.
// TV candidates whose release/file names carry a marker for a DIFFERENT
// episode are dropped outright: OpenSubtitles episode metadata is sometimes
// wrong, and a conflicting name is the one signal of a mislabeled upload
// available before download.
func rankSearchCandidatesForSource(results []opensubtitles.SubtitleResult, season, episode int, source subtitleSource) []subtitleCandidate {
	type ranked struct {
		candidate   subtitleCandidate
		affinity    int
		partial     bool
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
			metadata := strings.ToLower(attrs.Release + " " + file.FileName)
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
				candidate:   subtitleCandidate{FileID: file.FileID, Language: attrs.Language},
				affinity:    source.affinity(metadata),
				partial:     partialSubtitleNamePattern.MatchString(metadata),
				exactMarker: exactMarker,
				nonHI:       !attrs.HearingImpaired,
				downloads:   attrs.DownloadCount,
			})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].partial != all[j].partial {
			return !all[i].partial
		}
		if all[i].exactMarker != all[j].exactMarker {
			return all[i].exactMarker
		}
		if all[i].affinity != all[j].affinity {
			return all[i].affinity > all[j].affinity
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

// affinity ranks a release/file name against the source without giving a
// generic, highly downloaded upload a path ahead of a positive format match.
func (s subtitleSource) affinity(metadata string) int {
	if s.class == "unknown" {
		return 1
	}
	hasUHD := strings.Contains(metadata, "2160p") || strings.Contains(metadata, "uhd")
	hasBluRay := strings.Contains(metadata, "bluray") || strings.Contains(metadata, "blu-ray") || strings.Contains(metadata, "bdrip") || strings.Contains(metadata, "remux")
	has1080p := strings.Contains(metadata, "1080p")
	hasDVD := strings.Contains(metadata, "dvdrip") || strings.Contains(metadata, "dvd") || strings.Contains(metadata, "480p") || strings.Contains(metadata, "576p")
	switch s.class {
	case "uhd":
		if hasUHD {
			return 2
		}
		if has1080p || hasDVD {
			return 0
		}
	case "bluray":
		if hasUHD {
			return 0
		}
		if hasBluRay || has1080p {
			return 2
		}
		if hasDVD {
			return 0
		}
	case "dvd":
		if hasDVD {
			return 2
		}
		if hasUHD || hasBluRay || has1080p {
			return 0
		}
	}
	return 1
}

var partialSubtitleNamePattern = regexp.MustCompile(`(?:song[\s._-]+)?lyrics[\s._-]+only`)

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
// through the shared quota-aware cache (which episode identification's
// references also populate) when it is not already on disk.
func (h *Handler) fetchCandidate(ctx context.Context, candidate subtitleCandidate) (string, error) {
	cachePath := filepath.Join(h.cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", candidate.FileID))
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}
	if err := h.osClient.DownloadToFile(ctx, candidate.FileID, cachePath); err != nil {
		return "", err
	}
	return cachePath, nil
}
