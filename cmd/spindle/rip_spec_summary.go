package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ripSpecSummary struct {
	ContentKey string                `json:"content_key"`
	Metadata   map[string]any        `json:"metadata"`
	Titles     []ripSpecTitleSummary `json:"titles"`
}

type ripSpecTitleSummary struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Duration           int    `json:"duration"`
	ContentFingerprint string `json:"content_fingerprint"`
	Season             int    `json:"season"`
	Episode            int    `json:"episode"`
	EpisodeTitle       string `json:"episode_title"`
}

func parseRipSpecSummary(raw string) (ripSpecSummary, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ripSpecSummary{}, nil
	}
	var summary ripSpecSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		return ripSpecSummary{}, err
	}
	return summary, nil
}

func printRipSpecFingerprints(out io.Writer, summary ripSpecSummary) {
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
		fp := strings.TrimSpace(title.ContentFingerprint)
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
}
