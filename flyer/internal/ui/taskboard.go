package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// The board answers work, wait, and outcome separately. Only a measured local
// operation earns a bar; task percentages and file positions are not clocks.
func (m *Model) renderTaskBoard(b *strings.Builder, item spindle.QueueItem, styles Styles, width int) {
	if len(item.Tasks) == 0 {
		fmt.Fprintf(b, "  %s (tasks unavailable)\n", styles.MutedText.Render(stageDisplay(itemDisplayStage(item)).label))
		return
	}
	_, totals := item.EpisodeSnapshot()
	for _, task := range item.Tasks {
		m.renderTaskRow(b, item, task, totals, styles, width)
	}
	if elapsed := itemElapsed(item); elapsed >= time.Minute {
		label := "Elapsed " + humanizeDurationLong(elapsed)
		if taskDurationSum(item) > elapsed {
			label += " (stages overlap)"
		}
		fmt.Fprintf(b, "  %s\n", styles.FaintText.Render(label))
	}
}

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

func taskDurationSum(item spindle.QueueItem) time.Duration {
	var sum time.Duration
	for _, t := range item.Tasks {
		sum += t.Duration()
	}
	return sum
}

func (m *Model) renderTaskRow(b *strings.Builder, item spindle.QueueItem, task spindle.Task, totals spindle.EpisodeTotals, styles Styles, width int) {
	info := stageDisplay(task.Type)
	status, style, detail := "Waiting", styles.FaintText, ""
	switch task.State {
	case "done":
		status, style = "Done", styles.SuccessText
	case "failed":
		status, style, detail = "Failed", styles.DangerText, task.Error
	case "running":
		status, style = "Running", styles.AccentText
	}
	if item.UserStopped && task.State != "done" {
		status, detail = "Stopped", "Stopped by operator"
	}
	if task.State == "pending" && !item.UserStopped {
		var unmet []string
		for _, dep := range task.DependsOn {
			done := false
			for _, other := range item.Tasks {
				if other.Type == dep {
					done = other.IsDone()
					break
				}
			}
			if !done {
				unmet = append(unmet, stageDisplay(dep).label)
			}
		}
		switch {
		case len(unmet) > 0:
			detail = "Needs " + strings.Join(unmet, " + ")
		case m.snapshot.Status.Draining:
			detail = "Daemon draining; no new tasks"
		default:
			detail = "Ready; awaiting scheduler"
		}
	}
	now := m.clock()
	if m.snapshot.LastError != nil && !m.snapshot.LastUpdated.IsZero() {
		now = m.snapshot.LastUpdated
	}
	var activities []spindle.Activity
	diskWaiting := false
	for _, a := range task.Activities {
		if a.State == "waiting" && (task.State == "pending" || task.State == "running") && !item.UserStopped {
			status, detail = "Waiting", a.Message
			if a.Operation == "disk_space" {
				diskWaiting = true
				style = styles.WarningText
			}
			if !a.Started().IsZero() {
				detail += " (" + formatDuration(now.Sub(a.Started())) + ")"
			}
		}
		if task.State == "running" && a.State == "running" && !item.UserStopped {
			activities = append(activities, a)
		}
	}
	if len(activities) > 0 && status == "Waiting" && !diskWaiting {
		status, style = "Running", styles.AccentText
	}
	// The queue strip's glyph leads, then fixed status, stage, and files
	// columns keep durations aligned.
	row := fmt.Sprintf("%s %-7s %-12s", taskStateGlyph(strings.ToLower(status)), status, info.label)
	if totals.Planned > 0 {
		files := ""
		if n, ok := stageThroughput(info.totals, item, totals); ok {
			files = fmt.Sprintf("%d/%d files", n, totals.Planned)
		}
		row += fmt.Sprintf(" %-*s", len(fmt.Sprintf("%d/%d files", totals.Planned, totals.Planned)), files)
	}
	if task.State == "done" {
		if d := task.Duration(); d > 0 {
			row += " " + formatDuration(d)
		} else if !task.ParsedStartedAt().IsZero() && !task.ParsedFinishedAt().IsZero() {
			row += " <1s"
		}
		if task.Type == "subtitling" && item.SubtitleGeneration != nil {
			row += fmt.Sprintf(" · %d skipped", item.SubtitleGeneration.Skipped)
		}
	}
	row = strings.TrimRight(row, " ")
	if task.Attempts > 1 {
		row += fmt.Sprintf(" (attempt %d)", task.Attempts)
	}
	for _, a := range activities {
		age := now.Sub(a.Started())
		if a.Started().IsZero() || age >= time.Second && age < 10*time.Second {
			row += " - " + a.Message
		}
	}
	if task.State == "running" && len(activities) == 0 && detail == "" {
		message := strings.TrimSpace(task.Progress.Message)
		if message == "" {
			message = "Worker scheduled; activity not reported"
		}
		row += " - " + message
	}
	fmt.Fprintf(b, "  %s\n", style.Render(truncate(row, max(width-2, 20))))
	if detail != "" {
		for _, line := range wrapText(detail, max(width-6, 20)) {
			fmt.Fprintf(b, "      %s\n", style.Render(line))
		}
	}
	stale := m.snapshot.LastError != nil
	for _, a := range activities {
		age := now.Sub(a.Started())
		if a.Started().IsZero() || age < 10*time.Second {
			continue
		}
		message := a.Message
		if message == "" {
			message = a.Operation
		}
		if a.AssetKey != "" {
			message = a.AssetKey + ": " + message
		}
		message += " (" + formatDuration(age) + ")"
		if stale {
			message = "Stale snapshot: " + message
		}
		lines := wrapText(message, max(width-6, 20))
		if len(lines) > 0 {
			fmt.Fprintf(b, "      %s\n", styles.Text.Render(lines[0]))
		}
		video := a.ID == "video" && !stale
		if a.Total > 0 && a.Unit != "" {
			percent := float64(a.Completed) / float64(a.Total) * 100
			measure := fmt.Sprintf("%.0f%% · %d/%d %s", percent, a.Completed, a.Total, a.Unit)
			if video {
				if eta := taskETA(task, now); eta != "" {
					// Non-breaking spaces keep the scoped ETA on one wrapped line.
					measure += " · " + eta + "\u00a0left\u00a0(this\u00a0file)"
				} else if e := task.Encoding; e != nil && !e.Calibrating && e.TotalFrames > e.CurrentFrame {
					measure += " · ETA unavailable"
				}
			}
			// Wrap the measure beside the 24-cell bar rather than crop it.
			for i, line := range wrapText(measure, max(width-6-25, 20)) {
				bar := strings.Repeat(" ", 24)
				if i == 0 {
					bar = renderProgressBar(percent, 24, styles.AccentText, styles)
				}
				fmt.Fprintf(b, "      %s %s\n", bar, styles.MutedText.Render(line))
			}
		} else {
			fmt.Fprintf(b, "      %s\n", styles.FaintText.Render("No within-operation percentage available"))
		}
		if !a.Updated().IsZero() && now.Sub(a.Updated()) >= 10*time.Second {
			fmt.Fprintf(b, "      %s\n", styles.FaintText.Render("Last report "+formatDuration(now.Sub(a.Updated()))+" ago"))
		}
		if a.Total > 0 {
			advanced := parseTimestamp(a.AdvancedAt)
			if advanced.IsZero() {
				advanced = a.Started()
			}
			if quiet := now.Sub(advanced); quiet >= 30*time.Second && a.Completed < a.Total {
				fmt.Fprintf(b, "      %s\n", styles.FaintText.Render("No new "+a.Unit+" for "+formatDuration(quiet)+"; work may continue"))
			}
		}
		if video {
			for _, extra := range taskExtras(task) {
				for _, line := range wrapText(extra, max(width-6, 20)) {
					fmt.Fprintf(b, "      %s\n", styles.MutedText.Render(line))
				}
			}
		}
	}
}

func stageThroughput(key string, item spindle.QueueItem, totals spindle.EpisodeTotals) (int, bool) {
	switch key {
	case "ripped":
		return totals.Ripped, true
	case "encoded":
		return totals.Encoded, true
	case "subtitle_generated":
		if item.SubtitleGeneration != nil {
			return item.SubtitleGeneration.OpenSubtitles + item.SubtitleGeneration.Skipped, true
		}
	case "final":
		return totals.Final, true
	}
	return 0, false
}

// taskExtras groups encoder internals into short lines (chunk pipeline, then
// throughput) instead of one run-on sentence. The ETA rides on the bar line.
func taskExtras(task spindle.Task) []string {
	e := task.Encoding
	if !task.IsWorking() || e == nil {
		return nil
	}
	var lines []string
	if e.Calibrating {
		lines = append(lines, "Calibrating quality; video ETA unavailable")
	}
	join := func(parts []string) {
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, " · "))
		}
	}
	var chunks []string
	if e.ChunksTotal > 0 {
		chunks = append(chunks, fmt.Sprintf("%d/%d chunks accepted", e.ChunksComplete, e.ChunksTotal))
	}
	if e.Probing+e.Scoring+e.Finishing > 0 {
		chunks = append(chunks, fmt.Sprintf("%d probing / %d scoring / %d finishing", e.Probing, e.Scoring, e.Finishing))
	}
	if e.InFlight > 0 {
		chunks = append(chunks, fmt.Sprintf("%d in flight", e.InFlight))
	}
	join(chunks)
	var rate []string
	if e.FPS > 0 {
		rate = append(rate, fmt.Sprintf("%.1f fps average", e.FPS))
	}
	if e.RecentSpeed > 0 {
		rate = append(rate, fmt.Sprintf("%.2fx recent", e.RecentSpeed))
	}
	if e.TargetWorkers > 0 {
		rate = append(rate, fmt.Sprintf("workers %d/%d (limit %d)", e.ActiveWorkers, e.TargetWorkers, e.MaxWorkers))
	}
	if e.EncodeSlotWaitSeconds > 0 {
		rate = append(rate, fmt.Sprintf("slot wait %.0f worker-s", e.EncodeSlotWaitSeconds))
	}
	join(rate)
	return lines
}

// taskETA returns the fresh, producer-reported video ETA as "~Nm", or "".
func taskETA(task spindle.Task, now time.Time) string {
	if task.Type != "encoding" || !task.IsWorking() || task.Encoding == nil || task.Encoding.Calibrating {
		return ""
	}
	for _, a := range task.Activities {
		if a.ID != "video" || a.State != "running" || a.Updated().IsZero() || now.Sub(a.Updated()) > 10*time.Second {
			continue
		}
		if eta := task.Encoding.ETADuration(); eta > 0 {
			minutes := max(1, int((eta+time.Minute-1)/time.Minute))
			return fmt.Sprintf("~%dm", minutes)
		}
	}
	return ""
}
