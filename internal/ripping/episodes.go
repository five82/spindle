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

func assignEpisodeAssets(env *ripspec.Envelope, dir string, logger *slog.Logger) int {
	if env == nil || len(env.Episodes) == 0 {
		return 0
	}
	titleFiles := make(map[int]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if logger != nil {
			logger.Warn("failed to inspect rip directory", logging.String("dir", dir), logging.Error(err))
		}
		return 0
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
	assigned := 0
	for _, episode := range env.Episodes {
		if episode.TitleID < 0 {
			continue
		}
		path, ok := titleFiles[episode.TitleID]
		if !ok {
			continue
		}
		env.Assets.AddAsset("ripped", ripspec.Asset{EpisodeKey: episode.Key, TitleID: episode.TitleID, Path: path})
		assigned++
	}
	return assigned
}

func episodeAssetPaths(env ripspec.Envelope) []string {
	if len(env.Episodes) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(env.Episodes))
	paths := make([]string, 0, len(env.Episodes))
	for _, episode := range env.Episodes {
		asset, ok := env.Assets.FindAsset("ripped", episode.Key)
		if !ok {
			continue
		}
		clean := strings.TrimSpace(asset.Path)
		if clean == "" {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	return paths
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
