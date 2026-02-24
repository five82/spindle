package ripping

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/ripspec"
)

var titleFilePattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:title_)?t(\d{2,3})`)

// EpisodeAssignResult holds the result of episode asset assignment.
type EpisodeAssignResult struct {
	Assigned int
	Missing  []string // Episode keys that could not be matched to ripped files
}

func assignEpisodeAssets(env *ripspec.Envelope, dir string, logger *slog.Logger) EpisodeAssignResult {
	if env == nil || len(env.Episodes) == 0 {
		return EpisodeAssignResult{}
	}
	titleFiles := make(map[int]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to inspect rip directory; episode mapping skipped",
				logging.String("dir", dir),
				logging.Error(err),
				logging.String(logging.FieldEventType, "rip_dir_scan_failed"),
				logging.String(logging.FieldErrorHint, "check staging directory permissions"),
			)
		}
		return EpisodeAssignResult{}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".mkv") {
			continue
		}
		id, ok := parseTitleID(name)
		if !ok {
			continue
		}
		titleFiles[id] = filepath.Join(dir, name)
	}
	result := EpisodeAssignResult{}
	for _, episode := range env.Episodes {
		if episode.TitleID < 0 {
			result.Missing = append(result.Missing, episode.Key)
			continue
		}
		path, ok := titleFiles[episode.TitleID]
		if !ok {
			result.Missing = append(result.Missing, episode.Key)
			continue
		}
		env.Assets.AddAsset("ripped", ripspec.Asset{EpisodeKey: episode.Key, TitleID: episode.TitleID, Path: path})
		result.Assigned++
	}
	return result
}

func episodeAssetPaths(env ripspec.Envelope) []string {
	if len(env.Episodes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(env.Episodes))
	var paths []string
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("ripped", episode.Key)
		if !ok {
			continue
		}
		path := strings.TrimSpace(asset.Path)
		if path == "" {
			continue
		}
		if _, dup := seen[path]; dup {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

// cacheHasAllEpisodeFiles checks whether dir contains MKV files for every
// episode title ID in the envelope. Returns the list of missing episode keys,
// or nil if all are present.
func cacheHasAllEpisodeFiles(env *ripspec.Envelope, dir string) []string {
	if env == nil || len(env.Episodes) == 0 {
		return nil
	}
	titleFiles := make(map[int]struct{})
	entries, err := os.ReadDir(dir)
	if err != nil {
		// If we can't read the directory, report all episodes as missing.
		missing := make([]string, len(env.Episodes))
		for i, ep := range env.Episodes {
			missing[i] = ep.Key
		}
		return missing
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".mkv") {
			continue
		}
		id, ok := parseTitleID(entry.Name())
		if !ok {
			continue
		}
		titleFiles[id] = struct{}{}
	}
	var missing []string
	for _, ep := range env.Episodes {
		if ep.TitleID < 0 {
			missing = append(missing, ep.Key)
			continue
		}
		if _, ok := titleFiles[ep.TitleID]; !ok {
			missing = append(missing, ep.Key)
		}
	}
	return missing
}

func parseTitleID(name string) (int, bool) {
	match := titleFilePattern.FindStringSubmatch(name)
	if len(match) != 2 {
		return 0, false
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return value, true
}
