package main

import (
	"fmt"
	"io"
	"strings"

	"spindle/internal/ripspec"
)

func parseRipSpecSummary(raw string) (ripspec.Envelope, error) {
	return ripspec.Parse(raw)
}

func printRipSpecFingerprints(out io.Writer, summary ripspec.Envelope) {
	if out == nil {
		return
	}
	fmt.Fprintln(out, "\n🧬 Content Fingerprints:")
	if summary.ContentKey != "" {
		fmt.Fprintf(out, "  Content Key: %s\n", summary.ContentKey)
	}
	if len(summary.Titles) == 0 {
		fmt.Fprintln(out, "  (no titles reported)")
		return
	}
	for _, title := range summary.Titles {
		name := strings.TrimSpace(title.Name)
		if name == "" {
			name = "(untitled)"
		}
		if title.Season > 0 && title.Episode > 0 {
			episodeTitle := strings.TrimSpace(title.EpisodeTitle)
			if episodeTitle != "" {
				name = fmt.Sprintf("S%02dE%02d – %s", title.Season, title.Episode, episodeTitle)
			} else {
				name = fmt.Sprintf("S%02dE%02d", title.Season, title.Episode)
			}
		}
		fp := strings.TrimSpace(title.TitleHash)
		if len(fp) > 24 {
			fp = fp[:24]
		}
		fmt.Fprintf(
			out,
			"  - Title %d: %s | Duration %dm %ds | Fingerprint %s\n",
			title.ID,
			name,
			title.Duration/60,
			title.Duration%60,
			fp,
		)
	}
	if len(summary.Episodes) > 0 {
		fmt.Fprintln(out, "\n📺 Episode Targets:")
		for _, episode := range summary.Episodes {
			label := fmt.Sprintf("S%02dE%02d", episode.Season, episode.Episode)
			if strings.TrimSpace(episode.EpisodeTitle) != "" {
				label = fmt.Sprintf("%s – %s", label, episode.EpisodeTitle)
			}
			fmt.Fprintf(
				out,
				"  - %s | Title ID %d | Fingerprint %s\n",
				label,
				episode.TitleID,
				truncateFingerprint(episode.TitleHash),
			)
		}
	}
}

func truncateFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 24 {
		return value
	}
	return value[:24]
}
