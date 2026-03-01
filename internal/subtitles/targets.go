package subtitles

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

// subtitleTarget represents a single video file requiring subtitle generation.
type subtitleTarget struct {
	SourcePath   string
	WorkDir      string
	OutputDir    string
	BaseName     string
	EpisodeKey   string
	EpisodeTitle string
	TitleID      int
	Season       int
	Episode      int
}

func (g *Generator) buildSubtitleTargets(item *queue.Item) []subtitleTarget {
	if item == nil || g == nil || g.service == nil {
		return nil
	}
	stagingRoot := strings.TrimSpace(item.StagingRoot(g.service.config.Paths.StagingDir))
	if stagingRoot == "" {
		stagingRoot = filepath.Dir(strings.TrimSpace(item.EncodedFile))
	}
	if stagingRoot == "" {
		stagingRoot = "."
	}
	baseWorkDir := filepath.Join(stagingRoot, "subtitles")

	env, err := ripspec.Parse(item.RipSpecData)
	if err != nil && g.logger != nil {
		g.logger.Warn("failed to parse rip spec for subtitle targets; continuing with encoded file fallback",
			logging.Error(err),
			logging.String(logging.FieldEventType, "rip_spec_parse_failed"),
			logging.String(logging.FieldErrorHint, "rerun identification if subtitle targets look wrong"),
			logging.String(logging.FieldImpact, "subtitle targets determined from encoded file instead of rip spec"),
		)
	}

	var targets []subtitleTarget
	for idx, asset := range env.Assets.Encoded {
		source := strings.TrimSpace(asset.Path)
		if source == "" {
			continue
		}
		episodeKey := strings.TrimSpace(asset.EpisodeKey)
		season, episode := parseEpisodeKey(episodeKey)
		var episodeTitle string
		if ep := env.EpisodeByKey(episodeKey); ep != nil {
			if ep.Season > 0 {
				season = ep.Season
			}
			if ep.Episode > 0 {
				episode = ep.Episode
			}
			if strings.TrimSpace(ep.Key) != "" && episodeKey == "" {
				episodeKey = strings.TrimSpace(ep.Key)
			}
			episodeTitle = strings.TrimSpace(ep.EpisodeTitle)
		}
		targets = append(targets, subtitleTarget{
			SourcePath:   source,
			WorkDir:      filepath.Join(baseWorkDir, sanitizeEpisodeToken(episodeKey, idx)),
			OutputDir:    filepath.Dir(source),
			BaseName:     baseNameWithoutExt(source),
			EpisodeKey:   episodeKey,
			EpisodeTitle: episodeTitle,
			TitleID:      asset.TitleID,
			Season:       season,
			Episode:      episode,
		})
	}

	// Fall back to single encoded file if no episode assets
	if len(targets) == 0 {
		source := strings.TrimSpace(item.EncodedFile)
		if source == "" {
			return nil
		}
		targets = append(targets, subtitleTarget{
			SourcePath: source,
			WorkDir:    filepath.Join(baseWorkDir, "primary"),
			OutputDir:  filepath.Dir(source),
			BaseName:   baseNameWithoutExt(source),
		})
	}
	return targets
}

var episodeKeyPattern = regexp.MustCompile(`s?(\d+)[ex](\d+)`)

func parseEpisodeKey(key string) (int, int) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return 0, 0
	}
	matches := episodeKeyPattern.FindStringSubmatch(key)
	if len(matches) != 3 {
		return 0, 0
	}
	season, _ := strconv.Atoi(matches[1])
	episode, _ := strconv.Atoi(matches[2])
	return season, episode
}

func sanitizeEpisodeToken(key string, idx int) string {
	token := strings.TrimSpace(key)
	if token == "" {
		token = fmt.Sprintf("episode-%d", idx+1)
	}
	token = strings.ToLower(token)
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"..", "_",
	)
	return replacer.Replace(token)
}

// normalizeEpisodeKey returns a lowercase, trimmed episode key or "primary" if empty.
func normalizeEpisodeKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return "primary"
	}
	return normalized
}
