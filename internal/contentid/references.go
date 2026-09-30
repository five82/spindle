package contentid

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/opensubtitles"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/srtutil"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/tmdb"
)

type episodeReference struct {
	text   string
	fileID int
}

func (h *Handler) fetchReferences(ctx context.Context, sess *stage.Session, season *tmdb.Season, dir string) (map[string]episodeReference, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create reference directory: %w", err)
	}
	refs := make(map[string]episodeReference)
	for i, ep := range season.Episodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sess.Activity(queue.Activity{Operation: "references", Message: fmt.Sprintf("Phase 1/3 - Acquiring episode references (%d/%d)", i+1, len(season.Episodes))})
		results, err := h.osClient.Search(ctx, sess.Env.Metadata.ID, sess.Env.Metadata.SeasonNumber, ep.EpisodeNumber, []string{"en"})
		selected := selectReference(results, season, ep.EpisodeNumber)
		result, reason, fileID := "omitted", "no unambiguous canonical title in a single-file English full-subtitle candidate", 0
		if err == nil && selected != nil {
			fileID = selected.Files[0].FileID
			cache := filepath.Join(h.cfg.OpenSubtitlesCacheDir(), fmt.Sprintf("%d.srt", fileID))
			var data []byte
			data, err = os.ReadFile(cache)
			if os.IsNotExist(err) {
				err = h.osClient.DownloadToFile(ctx, fileID, cache)
				if err == nil {
					data, err = os.ReadFile(cache)
				}
			}
			text := dialogueExcerpt(srtutil.Parse(strings.TrimPrefix(string(data), "\ufeff")), 3000)
			if err == nil && text == "" {
				err = fmt.Errorf("reference has no middle-excerpt dialogue")
			}
			if err == nil {
				// Preserve the complete reference for subtitle adoption, which
				// still performs its own full-program cleanup/sync/verification.
				path := filepath.Join(dir, fmt.Sprintf("s%02de%02d-%d.srt", sess.Env.Metadata.SeasonNumber, ep.EpisodeNumber, fileID))
				if err := os.WriteFile(path, data, 0o644); err != nil {
					return nil, fmt.Errorf("stage reference: %w", err)
				}
				refs[fmt.Sprintf("E%02d", ep.EpisodeNumber)] = episodeReference{text: text, fileID: fileID}
				result, reason = "selected", "canonical title present, no competing episode title, single English full-subtitle file; non-HI then downloads then file ID"
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			reason = "reference acquisition failed: " + err.Error()
			sess.Logger.Warn("episode reference unavailable", "event_type", "contentid_reference_failed",
				"error_hint", err.Error(), "impact", "episode omitted from classifier choices", "episode", ep.EpisodeNumber)
		}
		attrs := []any{"decision_type", logs.DecisionReferenceSearch, "decision_result", result, "decision_reason", reason,
			"season", sess.Env.Metadata.SeasonNumber, "episode", ep.EpisodeNumber, "episode_title", ep.Name, "reference_file_id", fileID}
		if selected != nil {
			attrs = append(attrs, "release", selected.Release, "file_name", selected.Files[0].FileName)
		}
		sess.Logger.Info("episode reference decided", attrs...)
	}
	sess.Activity(queue.Activity{Operation: "references", State: "done", Message: fmt.Sprintf("Phase 1/3 - Episode references ready (%d/%d)", len(refs), len(season.Episodes))})
	return refs, nil
}

// API episode numbers and popularity cannot establish canonical identity.
// This is the evaluated title-consistency rule, not a weighted similarity
// rescue: unknown/conflicting labels are excluded, never accepted as suspect.
func selectReference(results []opensubtitles.SubtitleResult, season *tmdb.Season, episode int) *opensubtitles.SubtitleAttributes {
	var best *opensubtitles.SubtitleAttributes
	for i := range results {
		a := &results[i].Attributes
		if a.Language != "en" || a.ForeignPartsOnly || len(a.Files) != 1 || a.Files[0].FileID <= 0 {
			continue
		}
		text := normalizedTitle(a.Release + " " + a.Files[0].FileName)
		matches, conflicts := false, false
		for _, ep := range season.Episodes {
			title := normalizedTitle(ep.Name)
			if title != "  " && strings.Contains(text, title) {
				if ep.EpisodeNumber == episode {
					matches = true
				} else {
					conflicts = true
				}
			}
		}
		if !matches || conflicts {
			continue
		}
		if best == nil || (best.HearingImpaired && !a.HearingImpaired) || (best.HearingImpaired == a.HearingImpaired &&
			(a.DownloadCount > best.DownloadCount || (a.DownloadCount == best.DownloadCount && a.Files[0].FileID < best.Files[0].FileID))) {
			best = a
		}
	}
	return best
}

func normalizedTitle(s string) string {
	return " " + strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ") + " "
}

var excerptTags = regexp.MustCompile(`<[^>]*>|\{[^}]*\}`)

// Only the classifier input is excerpted. Shared SRT/word-timestamp artifacts
// remain full length for commentary analysis and display-subtitle verification.
func dialogueExcerpt(cues []srtutil.Cue, byteCap int) string {
	end := 0.0
	for _, cue := range cues {
		end = max(end, cue.End)
	}
	lo, hi := max(0, end/2-150), end/2+150
	var parts []string
	for _, cue := range cues {
		if cue.End >= lo && cue.Start <= hi {
			parts = append(parts, html.UnescapeString(excerptTags.ReplaceAllString(cue.Text, "")))
		}
	}
	text := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	if len(text) > byteCap {
		text = text[:byteCap]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}
