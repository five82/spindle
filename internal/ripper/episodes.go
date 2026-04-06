package ripper

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/ripspec"
)

// titleFilePattern matches title IDs in MakeMKV output filenames such as
// "Batman_t02.mkv" or "title_T15.mkv". Captures 2-3 digit IDs.
var titleFilePattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:title_)?t(\d{2,3})`)

// episodeAssignResult reports the outcome of mapping ripped files to episodes.
type episodeAssignResult struct {
	Assigned int
	Missing  []string // episode keys with no corresponding ripped file
}

// assignEpisodeAssets maps ripped files in dir to episodes by parsing title IDs
// from filenames and matching against each episode's TitleID. If titleFiles is
// non-nil, it is used as the pre-scanned view of dir and no filesystem scan is
// performed; otherwise dir is scanned.
func assignEpisodeAssets(env *ripspec.Envelope, dir string, titleFiles map[int]string, logger *slog.Logger) episodeAssignResult {
	if env == nil || len(env.Episodes) == 0 {
		return episodeAssignResult{}
	}

	if titleFiles == nil {
		scanned, err := scanTitleFiles(dir)
		if err != nil {
			if logger != nil {
				logger.Warn("failed to scan rip directory for episode mapping",
					"dir", dir,
					"error", err,
					"event_type", "rip_dir_scan_failed",
					"error_hint", "check staging directory permissions",
				)
			}
			return episodeAssignResult{}
		}
		titleFiles = scanned
	}

	var result episodeAssignResult
	for _, ep := range env.Episodes {
		if ep.TitleID < 0 {
			result.Missing = append(result.Missing, ep.Key)
			continue
		}
		path, ok := titleFiles[ep.TitleID]
		if !ok {
			result.Missing = append(result.Missing, ep.Key)
			continue
		}
		env.Assets.AddAsset(ripspec.AssetKindRipped, ripspec.Asset{
			EpisodeKey: ep.Key,
			TitleID:    ep.TitleID,
			Path:       path,
			Status:     ripspec.AssetStatusCompleted,
		})
		result.Assigned++
	}
	return result
}

// scanTitleFiles reads dir for MKV files and returns a map of parsed title ID
// to file path.
func scanTitleFiles(dir string) (map[int]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	result := make(map[int]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".mkv") {
			continue
		}
		id, ok := parseTitleID(e.Name())
		if !ok {
			continue
		}
		result[id] = filepath.Join(dir, e.Name())
	}
	return result, nil
}

// parseTitleID extracts a title ID from a MakeMKV output filename.
// Returns the ID and true on success.
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

// cacheHasAllEpisodeFiles checks that every episode's TitleID has a
// corresponding MKV file in dir. It returns the scanned title-file map (so
// callers can thread it into assignEpisodeAssets and avoid a second scan) and
// a list of missing episode keys. On scan error, titleFiles is nil and every
// episode is reported as missing.
func cacheHasAllEpisodeFiles(env *ripspec.Envelope, dir string) (map[int]string, []string) {
	if env == nil || len(env.Episodes) == 0 {
		return nil, nil
	}
	titleFiles, err := scanTitleFiles(dir)
	if err != nil {
		missing := make([]string, len(env.Episodes))
		for i, ep := range env.Episodes {
			missing[i] = ep.Key
		}
		return nil, missing
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
	return titleFiles, missing
}
