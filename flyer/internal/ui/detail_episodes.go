package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/spindle"
)

// renderEpisodeList renders the episode list for TV content.
// Includes episode sync warning, per-asset grids, and episode extras.
func (m *Model) renderEpisodeList(b *strings.Builder, item spindle.QueueItem, styles Styles, totals spindle.EpisodeTotals) {
	episodes, _ := item.EpisodeSnapshot()
	if len(episodes) == 0 {
		return
	}

	collapsed := m.isEpisodesCollapsed(item, episodes, totals)

	m.renderEpisodeSummary(b, item, episodes, totals, styles)

	// Episode sync warning (show when episodes don't have resolved keys)
	if matched := matchedEpisodeCount(item, episodes); matched > 0 && matched < len(episodes) {
		b.WriteString(styles.WarningText.Render("⚠ Episode numbers not confirmed"))
		b.WriteString("\n")
	}

	if collapsed {
		b.WriteString(styles.FaintText.Render("Press t to expand"))
		b.WriteString("\n")
		return
	}

	activeIdx, _ := activeEpisodeIndex(item, episodes)

	// Episode list with enhanced rendering
	for idx, ep := range episodes {
		m.renderEpisodeRow(b, item, ep, idx == activeIdx, styles)
	}
	b.WriteString(styles.FaintText.Render("Press t to collapse"))
	b.WriteString("\n")
}

// isEpisodesCollapsed returns whether episodes are collapsed for an item.
// Defaults to auto-expanding small sets and high-signal states unless explicitly overridden.
func (m *Model) isEpisodesCollapsed(item spindle.QueueItem, episodes []spindle.EpisodeStatus, totals spindle.EpisodeTotals) bool {
	collapsed, ok := m.detailState.episodeCollapsed[item.ID]
	if ok {
		return collapsed
	}
	return !shouldAutoExpandEpisodes(item, episodes, totals)
}

func shouldAutoExpandEpisodes(item spindle.QueueItem, episodes []spindle.EpisodeStatus, totals spindle.EpisodeTotals) bool {
	if len(episodes) <= 8 {
		return true
	}
	if len(spindle.FilterFailed(episodes)) > 0 {
		return true
	}
	if matched := matchedEpisodeCount(item, episodes); matched > 0 && matched < len(episodes) {
		return true
	}
	if strings.EqualFold(item.Stage, "completed") && totals.Planned > 0 && totals.Final < totals.Planned {
		return true
	}
	return false
}

// isEpisodeMapped reports whether an episode has been resolved to a real
// episode number (vs. still being a placeholder awaiting identification).
func isEpisodeMapped(ep spindle.EpisodeStatus) bool {
	if ep.MatchedEpisode > 0 || ep.MatchScore > 0 {
		return true
	}
	return ep.Episode > 0
}

func matchedEpisodeCount(item spindle.QueueItem, episodes []spindle.EpisodeStatus) int {
	if item.EpisodeIdentifiedCount > 0 {
		return min(item.EpisodeIdentifiedCount, len(episodes))
	}
	count := 0
	for _, ep := range episodes {
		if isEpisodeMapped(ep) {
			count++
		}
	}
	return count
}

func (m *Model) renderEpisodeSummary(b *strings.Builder, item spindle.QueueItem, episodes []spindle.EpisodeStatus, totals spindle.EpisodeTotals, styles Styles) {
	parts := []string{fmt.Sprintf("%d planned", totals.Planned)}
	if matched := matchedEpisodeCount(item, episodes); matched > 0 {
		parts = append(parts, fmt.Sprintf("%d matched", matched))
	}
	if totals.Ripped > 0 {
		parts = append(parts, fmt.Sprintf("%d ripped", totals.Ripped))
	}
	if totals.Encoded > 0 {
		parts = append(parts, fmt.Sprintf("%d encoded", totals.Encoded))
	}
	if totals.Subtitled > 0 {
		parts = append(parts, fmt.Sprintf("%d subtitled", totals.Subtitled))
	}
	if totals.Final > 0 {
		parts = append(parts, fmt.Sprintf("%d final", totals.Final))
	}
	if failed := len(spindle.FilterFailed(episodes)); failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	b.WriteString(styles.MutedText.Render(strings.Join(parts, " · ")))
	b.WriteString("\n")
}

// renderEpisodeRow renders a single episode with a per-asset grid and extras.
func (m *Model) renderEpisodeRow(b *strings.Builder, item spindle.QueueItem, ep spindle.EpisodeStatus, active bool, styles Styles) {
	marker := styles.FaintText.Render("·")
	if ep.IsFailed() {
		marker = styles.DangerText.Render("✗")
	} else if active {
		marker = styles.AccentText.Bold(true).Render(">")
	}

	label := formatEpisodeLabel(ep)
	assetActive := ep.Active || item.ActiveAssetKeys()[strings.ToLower(ep.Key)]
	grid := renderEpisodeAssetGrid(ep, assetActive, styles)
	title, extras := describeEpisodeWithExtras(ep)
	titleStyle := styles.Text
	if active {
		titleStyle = styles.Text.Bold(true)
	}

	b.WriteString(marker)
	b.WriteString(" ")
	b.WriteString(styles.MutedText.Render(label))
	b.WriteString(" ")
	b.WriteString(grid)
	b.WriteString(" ")
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n")

	if meta := compactEpisodeMeta(describeEpisodeTrackInfo(&ep), describeEpisodeMapping(ep), extras); meta != "" {
		b.WriteString("    ")
		b.WriteString(styles.FaintText.Render(meta))
		b.WriteString("\n")
	}

	// File states are visible in the asset grid; only issues need a line.
	if issue := describeEpisodeIssue(ep); issue != "" {
		b.WriteString("    ")
		b.WriteString(styles.WarningText.Render(issue))
		b.WriteString("\n")
	}

	if ep.IsFailed() && ep.ErrorMessage != "" {
		errMsg := ep.ErrorMessage
		if len(errMsg) > 80 {
			errMsg = errMsg[:77] + "..."
		}
		b.WriteString("    ")
		b.WriteString(styles.DangerText.Render(errMsg))
		b.WriteString("\n")
	}
}

// describeEpisodeWithExtras returns title and extra info (runtime, language, source).
func describeEpisodeWithExtras(ep spindle.EpisodeStatus) (string, []string) {
	title := episodeDisplayTitle(ep)
	var extras []string

	if runtime := formatRuntime(ep.RuntimeSeconds); runtime != "" {
		extras = append(extras, runtime)
	}

	if lang := strings.TrimSpace(ep.SubtitleLanguage); lang != "" {
		extras = append(extras, strings.ToUpper(lang))
	}

	if strings.EqualFold(strings.TrimSpace(ep.SubtitleSource), "whisperx") {
		extras = append(extras, "AI")
	}

	return title, extras
}

// activeEpisodeIndex returns the index of the first episode the server
// reports as actively being worked on: either its Active flag, or a running
// task's activeAssetKey matching the episode's key. When several episodes
// are active at once (e.g. rip/encode overlap on different episodes), the
// first one found wins.
func activeEpisodeIndex(item spindle.QueueItem, episodes []spindle.EpisodeStatus) (int, bool) {
	keys := item.ActiveAssetKeys()
	for i, ep := range episodes {
		if ep.Active || keys[strings.ToLower(ep.Key)] {
			return i, true
		}
	}
	return -1, false
}

// formatEpisodeLabel formats an episode as S01E01.
func formatEpisodeLabel(ep spindle.EpisodeStatus) string {
	if ep.Season == 0 && ep.Episode == 0 {
		return "S??E??"
	}
	return fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
}

// episodeAssetState is the per-asset-column state within an episode's grid.
type episodeAssetState int

const (
	episodeAssetPending episodeAssetState = iota
	episodeAssetDone
	episodeAssetFailed
	episodeAssetActive
)

// episodeAssetColumns are the grid columns in pipeline order: rip, encode,
// subtitle, final. This is spindle's per-episode asset vocabulary, not a
// pipeline stage list.
var episodeAssetColumns = [4]string{"R", "E", "S", "F"}

// episodeAssetStates derives the per-column state for an episode's asset
// grid, pure from the episode's path fields plus a caller-supplied active
// flag. A column is done when its path is non-empty; the first empty column
// after the last done one is the "next" column, which renders as failed (if
// the episode is failed) or active (if the episode is active); all other
// empty columns are pending.
func episodeAssetStates(ep spindle.EpisodeStatus, active bool) [4]episodeAssetState {
	paths := [4]string{ep.RippedPath, ep.EncodedPath, ep.SubtitledPath, ep.FinalPath}
	var states [4]episodeAssetState
	lastDone := -1
	for i, p := range paths {
		if strings.TrimSpace(p) != "" {
			states[i] = episodeAssetDone
			lastDone = i
		}
	}

	next := lastDone + 1
	failed := ep.IsFailed()
	for i := range states {
		if states[i] == episodeAssetDone {
			continue
		}
		switch {
		case failed && i == next:
			states[i] = episodeAssetFailed
		case !failed && active && i == next:
			states[i] = episodeAssetActive
		default:
			states[i] = episodeAssetPending
		}
	}
	return states
}

// episodeAssetGlyph returns the glyph for an asset cell state.
func episodeAssetGlyph(state episodeAssetState) string {
	switch state {
	case episodeAssetDone:
		return "✓"
	case episodeAssetFailed:
		return "✗"
	case episodeAssetActive:
		return "◉"
	default:
		return "·"
	}
}

// episodeAssetStyle returns the text style for an asset cell state.
func episodeAssetStyle(state episodeAssetState, styles Styles) lipgloss.Style {
	switch state {
	case episodeAssetDone:
		return styles.SuccessText
	case episodeAssetFailed:
		return styles.DangerText
	case episodeAssetActive:
		return styles.AccentText
	default:
		return styles.FaintText
	}
}

// renderEpisodeAssetGrid renders the compact per-asset grid for an episode
// row, e.g. "R✓ E◉ S✓ F·".
func renderEpisodeAssetGrid(ep spindle.EpisodeStatus, active bool, styles Styles) string {
	states := episodeAssetStates(ep, active)
	cells := make([]string, len(episodeAssetColumns))
	for i, col := range episodeAssetColumns {
		cells[i] = episodeAssetStyle(states[i], styles).Render(col + episodeAssetGlyph(states[i]))
	}
	return strings.Join(cells, " ")
}

// episodeDisplayTitle extracts the display title for an episode.
func episodeDisplayTitle(ep spindle.EpisodeStatus) string {
	if title := strings.TrimSpace(ep.Title); title != "" {
		return title
	}
	if title := strings.TrimSpace(ep.OutputBasename); title != "" {
		return title
	}
	if title := strings.TrimSpace(ep.SourceTitle); title != "" {
		return title
	}
	return "Unlabeled"
}

// describeEpisodeTrackInfo returns source track info for an episode.
func describeEpisodeTrackInfo(ep *spindle.EpisodeStatus) string {
	var parts []string
	if ep.SourceTitleID > 0 {
		parts = append(parts, fmt.Sprintf("Title %02d", ep.SourceTitleID))
	}
	if runtime := formatRuntime(ep.RuntimeSeconds); runtime != "" {
		parts = append(parts, runtime)
	}
	return strings.Join(parts, "  ")
}

// describeItemFileStates returns file state info for an item, tallied from
// the item's (for movies, usually single) episode asset paths.
func (m *Model) describeItemFileStates(item spindle.QueueItem) string {
	_, totals := item.EpisodeSnapshot()
	var parts []string
	if totals.Ripped > 0 {
		parts = append(parts, "RIP")
	}
	if totals.Encoded > 0 {
		parts = append(parts, "ENC")
	}
	if totals.Subtitled > 0 {
		parts = append(parts, "SUB")
	}
	if totals.Final > 0 {
		parts = append(parts, "FIN")
	}
	return strings.Join(parts, " ")
}

func describeEpisodeMapping(ep spindle.EpisodeStatus) string {
	switch {
	case ep.MatchScore > 0:
		return fmt.Sprintf("Match %.2f", ep.MatchScore)
	case ep.MatchedEpisode > 0:
		return fmt.Sprintf("Matched E%02d", ep.MatchedEpisode)
	case ep.Episode > 0:
		return "Mapped"
	default:
		return "Unmatched"
	}
}

// describeEpisodeIssue reports the episode's failure or review state.
func describeEpisodeIssue(ep spindle.EpisodeStatus) string {
	if ep.IsFailed() {
		return "failed"
	}
	if ep.NeedsReview {
		if reason := strings.TrimSpace(ep.ReviewReason); reason != "" {
			return reason
		}
		return "needs review"
	}
	return ""
}

// compactEpisodeMeta joins the episode row's secondary line fragments.
func compactEpisodeMeta(track string, mapping string, extras []string) string {
	parts := make([]string, 0, 2)
	if track != "" {
		parts = append(parts, track)
	}
	if mapping != "" {
		parts = append(parts, mapping)
	}
	if len(extras) > 0 {
		parts = append(parts, strings.Join(extras, " · "))
	}
	return strings.Join(parts, "  ·  ")
}
