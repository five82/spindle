package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/spindle"
)

// renderTaskBoard renders the item's scheduler tasks: one row per task in
// pipeline order. Running tasks show inline progress when the daemon reports
// it. Concurrent branches (rip-and-encode overlap, GPU work during encodes)
// each show their own live row.
func (m *Model) renderTaskBoard(b *strings.Builder, item spindle.QueueItem, styles Styles, width int) {
	if len(item.Tasks) == 0 {
		info := stageDisplay(itemDisplayStage(item))
		glyph, style := "○", styles.MutedText
		if item.IsTerminal() {
			glyph = taskStateGlyph(map[bool]string{true: "failed", false: "done"}[strings.EqualFold(item.Stage, "failed")])
			style = roleStyle(info.role, styles)
		}
		b.WriteString("  ")
		b.WriteString(style.Render(glyph))
		b.WriteString(" ")
		b.WriteString(styles.Text.Render(info.doneLabel))
		if !item.IsTerminal() {
			b.WriteString(" ")
			b.WriteString(styles.FaintText.Render("(tasks pending)"))
		}
		b.WriteString("\n")
		return
	}

	episodes, totals := item.EpisodeSnapshot()
	countWidth := len(strconv.Itoa(max(totals.Planned, 1)))

	for _, task := range item.Tasks {
		m.renderTaskRow(b, item, task, episodes, totals, countWidth, styles, width)
	}

	if elapsed := itemElapsed(item); elapsed >= time.Minute {
		label := "Elapsed " + humanizeDurationLong(elapsed)
		// Task durations can sum past the wall clock when stages ran
		// concurrently (rip/encode overlap); say so, or the arithmetic
		// reads as a bug.
		if taskDurationSum(item) > elapsed {
			label += " (stages overlap)"
		}
		b.WriteString("  ")
		b.WriteString(styles.FaintText.Render(label))
		b.WriteString("\n")
	}
}

// itemElapsed reports the item's wall-clock time in the pipeline: creation
// to the last task finish for terminal items, creation to now otherwise.
// Zero when the creation time is unknown; callers ignore sub-minute values
// -- the figure means nothing that fresh.
func itemElapsed(item spindle.QueueItem) time.Duration {
	created := item.ParsedCreatedAt()
	if created.IsZero() {
		return 0
	}
	end := time.Now()
	if item.IsTerminal() {
		end = item.ParsedUpdatedAt()
		for _, t := range item.Tasks {
			if fin := t.ParsedFinishedAt(); fin.After(end) {
				end = fin
			}
		}
	}
	return end.Sub(created)
}

// taskDurationSum totals the run time of the item's finished tasks.
func taskDurationSum(item spindle.QueueItem) time.Duration {
	var sum time.Duration
	for _, t := range item.Tasks {
		sum += t.Duration()
	}
	return sum
}

func (m *Model) renderTaskRow(b *strings.Builder, item spindle.QueueItem, task spindle.Task, episodes []spindle.EpisodeStatus, totals spindle.EpisodeTotals, countWidth int, styles Styles, width int) {
	info := stageDisplay(task.Type)

	glyph := taskStateGlyph(task.State)
	var glyphStyle, labelStyle lipgloss.Style
	label := info.label

	switch task.State {
	case "done":
		glyphStyle = styles.SuccessText
		label = info.doneLabel
		labelStyle = styles.Text
	case "running":
		glyphStyle = roleStyle(info.role, styles)
		labelStyle = roleStyle(info.role, styles).Bold(true)
	case "failed":
		glyphStyle = styles.DangerText
		labelStyle = styles.DangerText.Bold(true)
	default: // pending
		glyphStyle = styles.FaintText
		labelStyle = styles.FaintText
	}

	b.WriteString("  ")
	b.WriteString(glyphStyle.Render(glyph))
	b.WriteString(" ")
	b.WriteString(labelStyle.Render(fmt.Sprintf("%-12s", label)))

	// Per-episode position for a running task, or completed throughput for
	// every other state. This keeps the row aligned with its active episode:
	// while episode 2 is being ripped the row reads 2/N, not 1/N.
	if count, ok := stageTaskCount(info.totals, item, task, episodes, totals); ok && totals.Planned > 1 {
		b.WriteString(" ")
		b.WriteString(styles.MutedText.Render(fmt.Sprintf("%*d/%d", countWidth, count, totals.Planned)))
	}

	switch task.State {
	case "running":
		// Zero means the daemon has no meaningful progress to report. Keep the
		// task and its activity message visible without showing a false 0% bar.
		if task.Progress.Percent > 0 {
			b.WriteString("  ")
			b.WriteString(renderProgressBar(task.Progress.Percent, 20, roleStyle(info.role, styles), styles))
			b.WriteString(" ")
			b.WriteString(styles.Text.Render(fmt.Sprintf("%3.0f%%", clampPercent(task.Progress.Percent))))
		}
		for _, extra := range taskExtras(item, task, totals) {
			b.WriteString("  ")
			b.WriteString(styles.MutedText.Render(extra))
		}
	case "done":
		// Sub-second tasks render "<1s" rather than a blank cell -- a gap
		// in an otherwise complete duration column reads as missing data.
		if d := task.Duration(); d > 0 {
			b.WriteString("  ")
			b.WriteString(styles.FaintText.Render(formatDuration(d)))
		} else if !task.ParsedStartedAt().IsZero() && !task.ParsedFinishedAt().IsZero() {
			b.WriteString("  ")
			b.WriteString(styles.FaintText.Render("<1s"))
		}
	}

	// Retries are visible on every state: a task that needed more than one
	// attempt is silent flakiness worth seeing even after it succeeds.
	if task.Attempts > 1 {
		b.WriteString("  ")
		b.WriteString(styles.MutedText.Render(fmt.Sprintf("attempt %d", task.Attempts)))
	}
	b.WriteString("\n")

	// Second line: running message / failure error, wrapped.
	wrapWidth := max(width-6, 20)
	switch task.State {
	case "running":
		if msg := runningTaskMessage(task); msg != "" {
			for _, line := range wrapText(msg, wrapWidth) {
				b.WriteString(strings.Repeat(" ", 6))
				b.WriteString(styles.Text.Render(line))
				b.WriteString("\n")
			}
		}
		// Third line: the episode this task reports working on, when named.
		if context := taskEpisodeContext(task, episodes); context != "" {
			b.WriteString(strings.Repeat(" ", 6))
			b.WriteString(styles.FaintText.Render(context))
			b.WriteString("\n")
		}
	case "failed":
		if err := strings.TrimSpace(task.Error); err != "" {
			for _, line := range wrapText(err, wrapWidth) {
				b.WriteString(strings.Repeat(" ", 6))
				b.WriteString(styles.DangerText.Render(line))
				b.WriteString("\n")
			}
		}
	}
}

// taskEpisodeContext describes the episode a running task is working on:
// label, title, and source track info.
func taskEpisodeContext(task spindle.Task, episodes []spindle.EpisodeStatus) string {
	key := strings.ToLower(strings.TrimSpace(task.ActiveAssetKey))
	if key == "" {
		return ""
	}
	for i := range episodes {
		if strings.ToLower(episodes[i].Key) != key {
			continue
		}
		ep := episodes[i]
		parts := []string{strings.TrimSpace(formatEpisodeLabel(ep) + " " + episodeDisplayTitle(ep))}
		if track := describeEpisodeTrackInfo(&ep); track != "" {
			parts = append(parts, track)
		}
		return strings.Join(parts, " · ")
	}
	return ""
}

// stageTaskCount returns a task row's per-episode count. Running rows report
// the one-based position of their active episode; pending, done, and failed
// rows report completed throughput. The completion count is a floor so a
// briefly stale active key cannot make the row move backwards.
func stageTaskCount(key string, item spindle.QueueItem, task spindle.Task, episodes []spindle.EpisodeStatus, totals spindle.EpisodeTotals) (int, bool) {
	completed, ok := stageThroughput(key, item, totals)
	if !ok || !task.IsRunning() {
		return completed, ok
	}

	activeKey := strings.TrimSpace(task.ActiveAssetKey)
	if activeKey == "" {
		return completed, true
	}
	for i := range episodes {
		if strings.EqualFold(episodes[i].Key, activeKey) {
			return min(max(completed, i+1), totals.Planned), true
		}
	}
	return completed, true
}

// stageThroughput maps a catalog totals key to its completed count. Subtitle
// generation is distinct from the subtitled asset: generation finishes before
// apply creates that asset by placing or muxing the generated SRT.
func stageThroughput(key string, item spindle.QueueItem, totals spindle.EpisodeTotals) (int, bool) {
	switch key {
	case "ripped":
		return totals.Ripped, true
	case "encoded":
		return totals.Encoded, true
	case "subtitle_generated":
		if item.SubtitleGeneration == nil {
			return 0, true
		}
		return item.SubtitleGeneration.WhisperX, true
	case "final":
		return totals.Final, true
	default:
		return 0, false
	}
}

// runningTaskMessage composes the running task's message line with its
// active asset key when one is set and not already in the message.
func runningTaskMessage(task spindle.Task) string {
	msg := strings.TrimSpace(task.Progress.Message)
	key := strings.TrimSpace(task.ActiveAssetKey)
	if key != "" && !strings.Contains(strings.ToLower(msg), strings.ToLower(key)) {
		if msg == "" {
			return key
		}
		return msg + " (" + key + ")"
	}
	return msg
}

// taskExtras returns supplemental figures for a running task's row:
// fps and substage for encodes, byte progress for copy-style tasks, an ETA.
func taskExtras(item spindle.QueueItem, task spindle.Task, totals spindle.EpisodeTotals) []string {
	var extras []string
	if task.Type == "encoding" && item.Encoding != nil {
		if sub := strings.TrimSpace(item.Encoding.Substage); sub != "" {
			extras = append(extras, sub)
		}
		if item.Encoding.FPS > 0 {
			extras = append(extras, fmt.Sprintf("%.0f fps", item.Encoding.FPS))
		}
	}
	if task.Progress.TotalBytes > 0 {
		extras = append(extras, fmt.Sprintf("%s / %s",
			formatBytes(task.Progress.BytesCopied),
			formatBytes(task.Progress.TotalBytes)))
	}
	if eta := taskETA(item, task, totals); eta != "" {
		extras = append(extras, eta)
	}
	return extras
}

// taskETA estimates remaining time for a running task. Single-file encodes
// use reel's own ETA; everything else derives from the task's server-side
// start time and percent (no client-side stage tracking needed).
func taskETA(item spindle.QueueItem, task spindle.Task, totals spindle.EpisodeTotals) string {
	if task.Type == "encoding" && totals.Planned <= 1 && item.Encoding != nil {
		if eta := item.Encoding.ETADuration(); eta > 0 {
			return "ETA " + formatDuration(eta)
		}
	}
	percent := clampPercent(task.Progress.Percent)
	if percent < 5 || percent >= 100 {
		return ""
	}
	started := task.ParsedStartedAt()
	if started.IsZero() {
		return ""
	}
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return ""
	}
	remaining := time.Duration(float64(elapsed) * (100 - percent) / percent)
	if remaining <= 0 {
		return ""
	}
	return "ETA " + formatDuration(remaining)
}
