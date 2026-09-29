package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// Inventory never disappears. t toggles secondary evidence, not the files.
func (m *Model) renderEpisodeList(b *strings.Builder, item spindle.QueueItem, styles Styles, totals spindle.EpisodeTotals) {
	episodes, _ := item.EpisodeSnapshot()
	if len(episodes) == 0 {
		return
	}
	m.renderEpisodeSummary(b, item, episodes, totals, styles)
	keys := item.ActiveAssetKeys()
	for _, ep := range episodes {
		m.renderEpisodeRow(b, item, ep, keys[strings.ToLower(ep.Key)], styles)
	}
	fmt.Fprintln(b, styles.FaintText.Render("t: toggle file details; paths are recorded artifacts, not existence checks"))
}

func (m *Model) isEpisodesCollapsed(item spindle.QueueItem, _ []spindle.EpisodeStatus, _ spindle.EpisodeTotals) bool {
	collapsed, set := m.detailState.episodeCollapsed[item.ID]
	return !set || collapsed
}

func isEpisodeMapped(ep spindle.EpisodeStatus) bool { return ep.MatchedEpisode > 0 || ep.Episode > 0 }

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
	label := fmt.Sprintf("%d source files; %d ripped; %d encoded; %d published", totals.Planned, totals.Ripped, totals.Encoded, totals.Final)
	if isEpisodicItem(item) {
		label += fmt.Sprintf("; %d matched", matchedEpisodeCount(item, episodes))
	}
	fmt.Fprintln(b, styles.MutedText.Render(label))
}

func (m *Model) renderEpisodeRow(b *strings.Builder, item spindle.QueueItem, ep spindle.EpisodeStatus, active bool, styles Styles) {
	marker, markerStyle := " ", styles.AccentText
	if active {
		marker = ">"
	}
	if ep.IsFailed() {
		marker, markerStyle = "!", styles.DangerText
	}
	fmt.Fprintf(b, "%s %s %s\n", markerStyle.Render(marker), styles.Text.Render(formatEpisodeLabel(ep)), styles.Text.Render(episodeDisplayTitle(ep)))
	state := func(path string) string {
		if path != "" {
			return "yes"
		}
		return "no"
	}
	fmt.Fprintf(b, "  %s\n", styles.MutedText.Render(fmt.Sprintf("Recorded files: rip %s | encode %s | output %s", state(ep.RippedPath), state(ep.EncodedPath), state(ep.FinalPath))))
	fmt.Fprintf(b, "  %s\n", styles.MutedText.Render("Subtitle: "+subtitleOutcome(ep)))
	for _, task := range item.Tasks {
		if task.State != "running" || item.UserStopped {
			continue
		}
		found := false
		for _, a := range task.Activities {
			if a.State != "running" || !strings.EqualFold(a.AssetKey, ep.Key) {
				continue
			}
			message := stageDisplay(task.Type).label + ": " + a.Message
			if m.snapshot.LastError != nil {
				message = "Stale: " + message
			}
			for _, line := range wrapText(message, max(panelInnerWidth(m.width)-4, 20)) {
				fmt.Fprintf(b, "  %s\n", styles.AccentText.Render(line))
			}
			found = true
		}
		if !found && strings.EqualFold(task.ActiveAssetKey, ep.Key) {
			fmt.Fprintf(b, "  %s\n", styles.AccentText.Render(stageDisplay(task.Type).label+": activity not reported"))
		}
	}
	if issue := describeEpisodeIssue(ep); issue != "" {
		style := styles.WarningText
		if ep.IsFailed() {
			style = styles.DangerText
		}
		fmt.Fprintf(b, "  %s\n", style.Render(issue))
	}
	if !m.isEpisodesCollapsed(item, nil, spindle.EpisodeTotals{}) {
		w := fieldWriter{b: b, styles: styles, width: max(panelInnerWidth(m.width)-4, 20)}
		w.field("Key", ep.Key, styles.FaintText)
		w.field("Source", describeEpisodeTrackInfo(&ep), styles.Text)
		if isEpisodicItem(item) {
			w.field("Mapping", describeEpisodeMapping(ep), styles.Text)
		}
		w.field("Subs QC", ep.SubtitleValidation, styles.Text)
		w.field("Subs issues", strings.Join(append(append([]string{}, ep.SubtitleReviewIssues...), ep.SubtitleSevereIssues...), "; "), styles.WarningText)
		if audio := ep.AudioAnalysis; audio != nil {
			for _, track := range audio.CommentaryTracks {
				w.field("Comment", fmt.Sprintf("track %d: %s (confidence %.2f)", track.Index, track.Reason, track.Confidence), styles.Text)
			}
			for _, track := range audio.ExcludedTracks {
				w.field("Excluded", fmt.Sprintf("track %d: %s", track.Index, track.Reason), styles.Text)
			}
		}
		w.field("Final checks", ep.FinalValidation.Verdict(), styles.Text)
		if v := ep.FinalValidation; v != nil {
			w.field("Check details", strings.Join(v.FailedChecks, "; ")+v.Error, styles.WarningText)
			w.field("Delivered video", v.VideoCodec+" "+v.Resolution, styles.Text)
			w.field("Delivered audio", strings.Join(v.Audio, "; "), styles.Text)
			if v.AVSync != nil {
				text := v.AVSync.Error
				if text == "" {
					text = fmt.Sprintf("drift %.0fms", v.AVSync.DriftMilliseconds)
				}
				w.field("A/V sync", text, styles.Text)
			}
		}
		if s := ep.EncodeStats; s != nil {
			if s.Width > 0 {
				w.field("Source video", fmt.Sprintf("%dx%d", s.Width, s.Height), styles.Text)
			}
			if s.Validation != nil {
				for _, check := range s.Validation.Steps {
					w.field("Reel check", fmt.Sprintf("%s: passed=%t; %s", check.Name, check.Passed, check.Details), styles.MutedText)
				}
			}
			if len(s.TargetQuality) > 0 {
				w.field("Reel quality", string(s.TargetQuality), styles.MutedText)
			}
			if len(s.GrainTreatment) > 0 {
				w.field("Grain", string(s.GrainTreatment), styles.MutedText)
			}
			w.field("Reel", fmt.Sprintf("%s intermediate; %s; %.1fx", formatBytes(s.EncodedSizeBytes), formatDuration(time.Duration(s.EncodeSeconds*float64(time.Second))), s.Speed), styles.Text)
		}
		if ep.FinalSizeBytes > 0 {
			w.field("Delivered", formatBytes(ep.FinalSizeBytes)+" to "+ep.FinalRoute, styles.Text)
		}
		w.field("Destination", ep.FinalPath, styles.Text)
		w.field("Recorded rip", ep.RippedPath, styles.FaintText)
		w.field("Recorded encode", ep.EncodedPath, styles.FaintText)
	}
}

func subtitleOutcome(ep spindle.EpisodeStatus) string {
	switch {
	case len(ep.SubtitleSevereIssues) > 0:
		return "failed: " + strings.Join(ep.SubtitleSevereIssues, "; ")
	case ep.SubtitleSource == "none":
		return "skipped: " + ep.SubtitleSkipReason
	case ep.SubtitledPath != "":
		return "SRT applied (" + ep.SubtitleSource + ")"
	case ep.SubtitleSource != "":
		return "SRT adopted from " + ep.SubtitleSource + "; waiting for Apply"
	default:
		return "not checked"
	}
}

func formatEpisodeLabel(ep spindle.EpisodeStatus) string {
	if ep.Key == "main" {
		return "File"
	}
	if ep.Episode <= 0 {
		return fmt.Sprintf("Title %02d", ep.SourceTitleID)
	}
	label := fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
	if ep.EpisodeEnd > ep.Episode {
		label += fmt.Sprintf("-E%02d", ep.EpisodeEnd)
	}
	return label
}

func episodeDisplayTitle(ep spindle.EpisodeStatus) string {
	for _, text := range []string{ep.Title, ep.OutputBasename, ep.SourceTitle} {
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return "Title not reported"
}

func describeEpisodeTrackInfo(ep *spindle.EpisodeStatus) string {
	parts := []string{fmt.Sprintf("Title %02d", ep.SourceTitleID)}
	if runtime := formatRuntime(ep.RuntimeSeconds); runtime != "" {
		parts = append(parts, runtime)
	}
	return strings.Join(parts, "  ")
}

func (m *Model) describeItemFileStates(item spindle.QueueItem) string {
	_, t := item.EpisodeSnapshot()
	if t.Planned == 0 {
		return "Selected-file inventory not reported"
	}
	return fmt.Sprintf("%d/%d ripped; %d/%d encoded; %d/%d published", t.Ripped, t.Planned, t.Encoded, t.Planned, t.Final, t.Planned)
}

func describeEpisodeMapping(ep spindle.EpisodeStatus) string {
	if ep.Episode <= 0 {
		return "Unmatched"
	}
	value := formatEpisodeLabel(ep)
	if ep.MatchProbability > 0 {
		value += fmt.Sprintf("; probability %.2f", ep.MatchProbability)
	}
	return value
}

func describeEpisodeIssue(ep spindle.EpisodeStatus) string {
	if ep.IsFailed() {
		return "Failed: " + ep.ErrorMessage
	}
	if v := ep.FinalValidation.Verdict(); v == "failed" || v == "unavailable" {
		return "Final validation " + v
	}
	if ep.NeedsReview {
		return "Needs review: " + ep.ReviewReason
	}
	return ""
}
