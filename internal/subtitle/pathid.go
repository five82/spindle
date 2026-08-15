package subtitle

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
)

var (
	tmdbPathIDPattern  = regexp.MustCompile(`(?i)\[tmdbid-([0-9]+)\]`)
	seasonPathPattern  = regexp.MustCompile(`(?i)(?:^|/)season ([0-9]{1,2})(?:/|$)`)
	episodePathPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s([0-9]{1,2})e([0-9]{1,3})(?:-e([0-9]{1,3}))?(?:[^a-z0-9]|$)`)
)

// PathIdentity is the TMDB identity parsed from a Jellyfin library path.
// Season and Episode are zero for movies; EpisodeEnd > Episode marks a
// multi-episode file.
type PathIdentity struct {
	TMDBID     int
	Season     int
	Episode    int
	EpisodeEnd int
}

// ParseLibraryPathIdentity reads Jellyfin's TMDB provider marker
// ([tmdbid-ID]) plus the Season NN / SxxEyy markers from a library path.
// found is false when the path carries no marker at all; err reports a
// marker that is present but inconsistent.
func ParseLibraryPathIdentity(path string) (identity PathIdentity, found bool, err error) {
	normalizedPath := filepath.ToSlash(path)
	matches := tmdbPathIDPattern.FindAllStringSubmatch(normalizedPath, -1)
	if len(matches) == 0 {
		return PathIdentity{}, false, nil
	}
	tmdbID, convErr := strconv.Atoi(matches[0][1])
	if convErr != nil || tmdbID <= 0 {
		return PathIdentity{}, true, fmt.Errorf("invalid TMDB ID in path")
	}
	for _, match := range matches[1:] {
		if match[1] != matches[0][1] {
			return PathIdentity{}, true, fmt.Errorf("conflicting TMDB IDs in path")
		}
	}
	identity = PathIdentity{TMDBID: tmdbID}

	seasonMatch := seasonPathPattern.FindStringSubmatch(normalizedPath)
	if len(seasonMatch) == 0 {
		return identity, true, nil
	}
	episodeMatch := episodePathPattern.FindStringSubmatch(filepath.Base(path))
	if len(episodeMatch) == 0 {
		return PathIdentity{}, true, fmt.Errorf("TV library path has no episode marker")
	}
	season, _ := strconv.Atoi(episodeMatch[1])
	folderSeason, _ := strconv.Atoi(seasonMatch[1])
	first, _ := strconv.Atoi(episodeMatch[2])
	last := first
	if episodeMatch[3] != "" {
		last, _ = strconv.Atoi(episodeMatch[3])
	}
	if season <= 0 || folderSeason != season || first <= 0 || last < first {
		return PathIdentity{}, true, fmt.Errorf("invalid or inconsistent TV episode marker in path")
	}
	identity.Season = season
	identity.Episode = first
	identity.EpisodeEnd = last
	return identity, true, nil
}
