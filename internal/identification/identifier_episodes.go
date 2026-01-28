package identification

import (
	"fmt"
	"sort"
	"strings"

	"spindle/internal/disc"
	"spindle/internal/identification/tmdb"
	"spindle/internal/queue"
)

func mapEpisodesToTitles(titles []disc.Title, episodes []tmdb.Episode, discNumber int) (map[int]episodeAnnotation, []int) {
	if len(titles) == 0 || len(episodes) == 0 {
		return nil, nil
	}
	assigned := make(map[int]episodeAnnotation)
	used := make([]bool, len(episodes))
	epTitles := make([]disc.Title, 0, len(titles))
	for _, title := range titles {
		if isEpisodeRuntime(title.Duration) {
			epTitles = append(epTitles, title)
		}
	}
	if len(epTitles) == 0 {
		return nil, nil
	}
	start := estimateEpisodeStart(discNumber, len(epTitles), len(episodes))
	for _, title := range epTitles {
		idx := chooseEpisodeForTitle(title.Duration, episodes, used, start)
		if idx == -1 {
			continue
		}
		used[idx] = true
		ep := episodes[idx]
		assigned[title.ID] = episodeAnnotation{
			Season:  ep.SeasonNumber,
			Episode: ep.EpisodeNumber,
			Title:   strings.TrimSpace(ep.Name),
			Air:     strings.TrimSpace(ep.AirDate),
		}
	}
	if len(assigned) == 0 {
		return nil, nil
	}
	numbers := make([]int, 0, len(assigned))
	for _, ann := range assigned {
		if ann.Episode > 0 {
			numbers = append(numbers, ann.Episode)
		}
	}
	sort.Ints(numbers)
	return assigned, numbers
}

func estimateEpisodeStart(discNumber int, discEpisodes int, totalEpisodes int) int {
	if discNumber <= 1 || discEpisodes <= 0 || totalEpisodes == 0 {
		return 0
	}
	start := (discNumber - 1) * discEpisodes
	if start >= totalEpisodes {
		start = totalEpisodes - discEpisodes
		if start < 0 {
			start = 0
		}
	}
	return start
}

func chooseEpisodeForTitle(durationSeconds int, episodes []tmdb.Episode, used []bool, startIndex int) int {
	if len(episodes) == 0 {
		return -1
	}
	bestIdx := -1
	bestDelta := int(^uint(0) >> 1)
	if startIndex < 0 {
		startIndex = 0
	}
	if startIndex > len(episodes) {
		startIndex = len(episodes)
	}
	for idx := startIndex; idx < len(episodes); idx++ {
		ep := episodes[idx]
		if idx < len(used) && used[idx] {
			continue
		}
		if ep.SeasonNumber <= 0 {
			continue
		}
		runtime := ep.Runtime
		if runtime <= 0 {
			runtime = durationSeconds / 60
			if runtime == 0 {
				runtime = 22
			}
		}
		delta := absInt(runtime*60 - durationSeconds)
		if delta < bestDelta {
			bestDelta = delta
			bestIdx = idx
		}
	}
	const maxAcceptableDelta = 5 * 60
	if bestIdx != -1 && bestDelta <= maxAcceptableDelta {
		return bestIdx
	}
	for idx := 0; idx < len(episodes); idx++ {
		if idx < startIndex {
			if idx < len(used) && used[idx] {
				continue
			}
			ep := episodes[idx]
			delta := episodeDurationDelta(durationSeconds, ep)
			if delta < bestDelta {
				bestDelta = delta
				bestIdx = idx
			}
		}
	}
	if bestIdx != -1 && bestDelta <= maxAcceptableDelta {
		return bestIdx
	}
	for idx := range episodes {
		if idx < len(used) && used[idx] {
			continue
		}
		return idx
	}
	return -1
}

func episodeDurationDelta(durationSeconds int, ep tmdb.Episode) int {
	runtime := ep.Runtime
	if runtime <= 0 {
		runtime = durationSeconds / 60
		if runtime == 0 {
			runtime = 22
		}
	}
	return absInt(runtime*60 - durationSeconds)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// EpisodeOutputBasename generates a standard episode filename from show, season, and episode.
func EpisodeOutputBasename(show string, season, episode int) string {
	show = strings.TrimSpace(show)
	if show == "" {
		show = "Manual Import"
	}
	display := fmt.Sprintf("%s Season %02d", show, season)
	meta := queue.NewTVMetadata(show, season, []int{episode}, display)
	name := meta.GetFilename()
	if strings.TrimSpace(name) == "" {
		return fmt.Sprintf("%s - S%02dE%02d", strings.TrimSpace(show), season, episode)
	}
	return name
}
