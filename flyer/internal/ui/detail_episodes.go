package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

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
	// Recorded-file glyphs match the queue's task strip (✓ recorded, ○ not).
	files, filesWidth := "", 0
	for _, f := range []struct{ name, path string }{{"rip", ep.RippedPath}, {"encode", ep.EncodedPath}, {"output", ep.FinalPath}} {
		glyph, style := "○", styles.FaintText
		if f.path != "" {
			glyph, style = "✓", styles.SuccessText
		}
		if files != "" {
			files += "  "
			filesWidth += 2
		}
		files += styles.MutedText.Render(f.name+" ") + style.Render(glyph)
		filesWidth += len(f.name) + 2
	}
	subtitle := "Subtitle: " + subtitleOutcome(ep)
	if filesWidth+5+lipgloss.Width(subtitle) <= panelInnerWidth(m.width)-2 {
		fmt.Fprintf(b, "  %s%s%s\n", files, styles.FaintText.Render("  ·  "), styles.MutedText.Render(subtitle))
	} else {
		fmt.Fprintf(b, "  %s\n  %s\n", files, styles.MutedText.Render(subtitle))
	}
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
			if a.Total > 0 && a.Unit != "" {
				message += fmt.Sprintf(" %.0f%% (%d/%d %s)", 100*float64(a.Completed)/float64(a.Total), a.Completed, a.Total, a.Unit)
			}
			if eta := taskETA(task, m.clock()); a.ID == "video" && eta != "" {
				message += "; " + eta + " remaining"
			}
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
		// One shared label column (widest label + 1) keeps every value aligned.
		w := fieldWriter{b: b, styles: styles, width: panelInnerWidth(m.width), indent: 2, labelWidth: len("Reel quality") + 1}
		w.field("Key", ep.Key, styles.FaintText)
		w.field("Source", describeEpisodeTrackInfo(&ep), styles.Text)
		if isEpisodicItem(item) {
			w.field("Mapping", describeEpisodeMapping(ep), styles.Text)
		}
		w.field("Subs QC", ep.SubtitleValidation, styles.Text)
		w.field("Subs issues", strings.Join(append(append([]string{}, ep.SubtitleReviewIssues...), ep.SubtitleSevereIssues...), "; "), styles.WarningText)
		if audio := ep.AudioAnalysis; audio != nil {
			for _, track := range audio.CommentaryTracks {
				w.field("Commentary", fmt.Sprintf("track %d: %s (confidence %.2f)", track.Index, track.Reason, track.Confidence), styles.Text)
			}
			for _, track := range audio.ExcludedTracks {
				w.field("Excluded", fmt.Sprintf("track %d: %s", track.Index, track.Reason), styles.Text)
			}
		}
		verdict := ep.FinalValidation.Verdict()
		if verdict == "not run" {
			verdict = "pending (run after Apply)" // matches the Overview's Checks row
		}
		w.field("Final checks", verdict, styles.Text)
		if v := ep.FinalValidation; v != nil {
			w.field("Check detail", strings.Join(v.FailedChecks, "; ")+v.Error, styles.WarningText)
			w.field("Out video", strings.TrimSpace(v.VideoCodec+" "+v.Resolution), styles.Text)
			w.field("Out audio", strings.Join(v.Audio, "; "), styles.Text)
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
				w.field("Src video", fmt.Sprintf("%dx%d", s.Width, s.Height), styles.Text)
			}
			if s.Validation != nil {
				for _, check := range s.Validation.Steps {
					w.field("Reel check", fmt.Sprintf("%s: passed=%t; %s", check.Name, check.Passed, check.Details), styles.MutedText)
				}
			}
			for _, line := range summarizeTargetQuality(s.TargetQuality) {
				w.field("Reel quality", line, styles.MutedText)
			}
			w.field("Grain", summarizeGrain(s.GrainTreatment), styles.MutedText)
			w.field("Encoded", fmt.Sprintf("%s (before Apply) in %s; %.1fx speed", formatBytes(s.EncodedSizeBytes), formatDuration(time.Duration(s.EncodeSeconds*float64(time.Second))), s.Speed), styles.Text)
		}
		if ep.FinalSizeBytes > 0 {
			w.field("Delivered", formatBytes(ep.FinalSizeBytes)+" to "+ep.FinalRoute, styles.Text)
		}
		w.field("Destination", ep.FinalPath, styles.Text)
		w.field("Rip file", ep.RippedPath, styles.FaintText)
		w.field("Encode file", ep.EncodedPath, styles.FaintText)
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
	value := fmt.Sprintf("%d/%d ripped; %d/%d encoded; %d/%d published", t.Ripped, t.Planned, t.Encoded, t.Planned, t.Final, t.Planned)
	if isEpisodicItem(item) {
		episodes, _ := item.EpisodeSnapshot()
		value += fmt.Sprintf("; %d/%d matched", matchedEpisodeCount(item, episodes), t.Planned)
	}
	return value
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

// summarizeTargetQuality condenses Reel's CRF-search aggregate to one line
// per metric; metrics.jsonl keeps the full record. Unparseable input falls
// back to one truncated line of raw JSON.
func summarizeTargetQuality(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tq struct {
		Metrics []struct {
			Metric    string  `json:"metric"`
			Target    float64 `json:"target"`
			Tolerance float64 `json:"tolerance"`
			Chunks    int     `json:"chunks"`
			ScoreMin  float64 `json:"score_min"`
			ScoreMean float64 `json:"score_mean"`
			CRFMin    float64 `json:"final_crf_min"`
			CRFMedian float64 `json:"final_crf_median"`
			CRFMax    float64 `json:"final_crf_max"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &tq); err != nil || len(tq.Metrics) == 0 {
		return []string{truncate(string(raw), 80)}
	}
	var lines []string
	for _, q := range tq.Metrics {
		lines = append(lines, fmt.Sprintf("%s %.2f±%.2f: mean %.2f, min %.2f; CRF %g-%g (median %g); %d chunks",
			q.Metric, q.Target, q.Tolerance, q.ScoreMean, q.ScoreMin, q.CRFMin, q.CRFMax, q.CRFMedian, q.Chunks))
	}
	return lines
}

// summarizeGrain condenses the grain gate's verdict: whether treatment ran,
// the bits-per-pixel evidence against its cutoff, and the denoise ceiling
// that caps a treated title's scores.
func summarizeGrain(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var g struct {
		Treated     bool     `json:"treated"`
		Denoise     string   `json:"denoise"`
		Reason      string   `json:"reason"`
		MedianBPP   float64  `json:"median_bpp"`
		Cutoff      float64  `json:"treatment_bpp_cutoff"`
		CeilingMean *float64 `json:"denoise_ceiling_jod_mean"`
		CeilingMin  *float64 `json:"denoise_ceiling_jod_min"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return truncate(string(raw), 80)
	}
	parts := []string{"not treated"}
	if g.Treated {
		parts[0] = "treated"
		if g.Denoise != "" {
			parts[0] += " (" + g.Denoise + ")"
		}
	}
	if g.Reason != "" {
		parts = append(parts, g.Reason)
	}
	if g.MedianBPP > 0 {
		parts = append(parts, fmt.Sprintf("median bpp %.3f vs cutoff %g", g.MedianBPP, g.Cutoff))
	}
	if g.CeilingMean != nil && g.CeilingMin != nil {
		parts = append(parts, fmt.Sprintf("denoise ceiling %.2f JOD (min %.2f)", *g.CeilingMean, *g.CeilingMin))
	}
	return strings.Join(parts, "; ")
}
