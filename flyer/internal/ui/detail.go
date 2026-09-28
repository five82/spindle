package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/five82/spindle/flyer/internal/spindle"
)

// fieldWriter renders aligned label/value rows with word-wrapped values.
type fieldWriter struct {
	b      *strings.Builder
	styles Styles
	width  int
}

// detailFieldLabelWidth is the fixed label column of the overview rows,
// sized so the widest labels ("Quality", "Context") keep a trailing space.
const detailFieldLabelWidth = 9

// field writes one label/value row with a muted label.
func (w fieldWriter) field(label, value string, valueStyle lipgloss.Style) {
	w.fieldStyled(label, w.styles.MutedText, value, valueStyle)
}

// fieldStyled writes one label/value row; long values wrap with
// continuation lines indented under the value column.
func (w fieldWriter) fieldStyled(label string, labelStyle lipgloss.Style, value string, valueStyle lipgloss.Style) {
	if strings.TrimSpace(value) == "" {
		return
	}
	labelWidth := max(detailFieldLabelWidth, len(label)+1)
	lines := wrapText(value, max(w.width-labelWidth, 20))
	w.b.WriteString(labelStyle.Render(fmt.Sprintf("%-*s", labelWidth, label)))
	w.b.WriteString(valueStyle.Render(lines[0]))
	w.b.WriteString("\n")
	for _, line := range lines[1:] {
		w.b.WriteString(strings.Repeat(" ", labelWidth))
		w.b.WriteString(valueStyle.Render(line))
		w.b.WriteString("\n")
	}
}

// renderDetailContent renders the inspector Overview tab. The layout is a
// fixed skeleton -- same sections, same order, for every item state; rows
// appear or disappear by data presence, never by state branching:
//
//	Attention review/error/warning details (only when something needs the operator)
//	Pipeline  scheduler task board
//	Media     source, video, audio, crop, encoder config, identification
//	Output    size estimate/result, encode stats, validation, subtitles, path
//	Episodes  batch summary (full list lives on the Episodes tab)
//	Meta      absolute timestamps (faint footer; the item band carries the age)
func (m *Model) renderDetailContent(item spindle.QueueItem, width int) string {
	if width <= 0 {
		width = m.width
	}
	styles := m.theme.Styles()
	var b strings.Builder
	w := fieldWriter{b: &b, styles: styles, width: width}

	m.renderAttention(w, item, styles)

	m.writeSection(&b, "Pipeline", styles, width)
	m.renderTaskBoard(&b, item, styles, width)

	m.renderMedia(w, item, styles)
	m.renderOutput(w, item, styles)
	m.renderEpisodeSummarySection(&b, item, styles)

	m.renderDetailMeta(&b, item, styles)

	return strings.TrimPrefix(b.String(), "\n")
}

// renderDetailMeta renders the absolute created/updated timestamps as a
// faint footer; the item band already carries the live "updated Xm ago".
func (m *Model) renderDetailMeta(b *strings.Builder, item spindle.QueueItem, styles Styles) {
	now := time.Now()
	var parts []string

	if created := parseTimestamp(item.CreatedAt); !created.IsZero() {
		parts = append(parts, "created "+formatTimestamp(created, now))
	}
	if updated := parseTimestamp(item.UpdatedAt); !updated.IsZero() {
		parts = append(parts, "updated "+formatTimestamp(updated, now))
	}
	if len(parts) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(styles.FaintText.Render(strings.Join(parts, " · ")))
	b.WriteString("\n")
}

// renderStatusChips renders the status badges for an item.
func (m *Model) renderStatusChips(item spindle.QueueItem, styles Styles) string {
	var chips []string

	// Status chip: role-colored text from the task/stage registry, so an
	// unrecognized stage name renders neutrally instead of crashing or
	// falling back to a hardcoded color table.
	info := stageDisplay(itemDisplayStage(item))
	var label string
	if item.IsTerminal() {
		label = info.doneLabel
	} else if item.UserStopped {
		label = "Stopped"
	} else {
		var active []string
		for _, task := range item.WorkingTasks() {
			active = append(active, stageDisplay(task.Type).label)
		}
		if len(active) > 0 {
			label = strings.Join(active, " + ")
		} else {
			label = "Waiting"
		}
	}
	chips = append(chips, roleStyle(info.role, styles).Bold(true).Render(strings.ToUpper(label)))

	// Media type chip
	if mediaType := detectMediaType(item.Metadata); mediaType != "" {
		label := "MOVIE"
		if mediaType == "tv" {
			label = "TV"
		}
		chips = append(chips, chip(label, m.theme.Accent, m.theme))
	}

	// Review badge
	if item.NeedsReview {
		chips = append(chips, chip("REVIEW", m.theme.Warning, m.theme))
	}

	// Error badge
	if strings.TrimSpace(item.ErrorMessage) != "" {
		chips = append(chips, chip("ERROR", m.theme.Danger, m.theme))
	}

	// Operator-stopped badge: a deliberate stop must not read as a stall.
	if item.UserStopped {
		chips = append(chips, chip("STOPPED", m.theme.Muted, m.theme))
	}

	// CACHE badge (rip cache hit, reported via the ripping task's message)
	if isRipCacheHit(item) {
		chips = append(chips, chip("CACHE", m.theme.Info, m.theme))
	}

	return strings.Join(chips, styles.Band.Render(" "))
}

// isRipCacheHit reports whether any task's progress message indicates a rip
// cache hit.
func isRipCacheHit(item spindle.QueueItem) bool {
	for _, t := range item.Tasks {
		if strings.Contains(strings.ToLower(t.Progress.Message), "rip cache hit") {
			return true
		}
	}
	return false
}

// writeSection writes a section header as a width-adaptive titled rule,
// matching the top-level view rules.
func (m *Model) writeSection(b *strings.Builder, title string, styles Styles, width int) {
	if width <= 0 {
		width = m.width
	}
	b.WriteString("\n")
	b.WriteString(renderRule(titleCase(title), width, styles))
	b.WriteString("\n")
}

// needsAttention reports whether the item has anything for the operator.
func needsAttention(item spindle.QueueItem) bool { return len(itemProblems(item)) > 0 }

// A single ordered issue list drives Attention, Problems, and the badge. Raw
// diagnostics remain separate, since a log line does not prove a current fault.
func itemProblems(item spindle.QueueItem) []string {
	var problems []string
	seen := make(map[string]bool)
	add := func(scope, text string) {
		text = strings.TrimSpace(text)
		if text != "" && !seen[text] {
			problems = append(problems, scope+text)
			seen[text] = true
		}
	}
	for _, task := range item.Tasks {
		if task.IsFailed() {
			scope := stageDisplay(task.Type).label + " failed"
			if task.Attempts > 1 {
				scope += fmt.Sprintf(" (attempt %d)", task.Attempts)
			}
			add(scope+": ", task.Error)
		}
	}
	add("", item.ErrorMessage)
	if item.FailedAtStage != "" && len(item.Tasks) == 0 {
		add("", stageDisplay(item.FailedAtStage).label+" failed")
	}
	for _, ep := range item.Episodes {
		if ep.IsFailed() {
			add("", ep.Key+": "+ep.ErrorMessage)
		}
		if v := ep.FinalValidation; v.Verdict() == "failed" || v.Verdict() == "unavailable" {
			text := "Final checks " + v.Verdict() + ": " + strings.Join(v.FailedChecks, "; ") + v.Error
			if v.AVSync != nil && v.AVSync.Error != "" {
				text += " A/V sync: " + v.AVSync.Error
			}
			add("", ep.Key+": "+text)
		}
		if ep.NeedsReview {
			add(ep.Key+": ", ep.ReviewReason)
		}
		if ep.SubtitleSource == "none" {
			add("", ep.Key+": subtitles skipped: "+ep.SubtitleSkipReason+"; no display SRT")
		}
		for _, issue := range append(append([]string{}, ep.SubtitleReviewIssues...), ep.SubtitleSevereIssues...) {
			add(ep.Key+": ", issue)
		}
	}
	if item.NeedsReview {
		for _, reason := range item.ReviewReasons {
			add("Review: ", reason)
		}
		if len(item.ReviewReasons) == 0 {
			add("", "Needs operator review")
		}
	}
	if e := item.Encoding; e != nil {
		if e.Error != nil {
			add("Encode: ", e.Error.Message)
			add("Encode: ", e.Error.Title)
			add("Context: ", e.Error.Context)
			add("Suggestion: ", e.Error.Suggestion)
		}
		add("Warning: ", e.Warning)
		if e.Validation != nil && !e.Validation.Passed {
			for _, check := range e.Validation.Steps {
				if !check.Passed {
					add("Reel intermediate: ", check.Name+": "+check.Details)
				}
			}
		}
	}
	if len(problems) == 0 && item.Stage == "failed" {
		add("", "Failed at "+item.FailedAtStage)
	}
	return problems
}

// renderAttention renders the single home for review/error information.
// Renders nothing when the item is healthy.
func (m *Model) renderAttention(w fieldWriter, item spindle.QueueItem, styles Styles) {
	problems := itemProblems(item)
	if len(problems) == 0 {
		return
	}
	m.writeSection(w.b, "Attention", styles, w.width)
	w.field("Issue", truncate(problems[0], max(w.width-10, 20)), styles.WarningText)
	w.field("Details", fmt.Sprintf("%d issue(s); see 3 Problems", len(problems)), styles.MutedText)
}

// renderMedia renders the stable media facts block: what is being processed
// and how it will be encoded. Identical shape whether running or done.
func (m *Model) renderMedia(w fieldWriter, item spindle.QueueItem, styles Styles) {
	var b strings.Builder
	inner := fieldWriter{b: &b, styles: w.styles, width: w.width}

	inner.field("Source", sourceSummary(item.Source), styles.Text)
	if item.DiscNumber > 0 {
		inner.field("Disc", fmt.Sprintf("%d", item.DiscNumber), styles.Text)
	}
	renderVideoSpecs(inner, item)
	renderAudioInfo(inner, item)
	if item.CommentaryCount > 0 {
		inner.field("Tracks", fmt.Sprintf("%d commentary track(s) detected", item.CommentaryCount), styles.Text)
	}
	renderEncodingConfig(inner, item)
	renderContentID(inner, item)

	// Identification metadata (year, ids, ...) when present.
	for _, r := range summarizeMetadata(item.Metadata) {
		label := metadataFieldLabel(r.key)
		if label == "" {
			continue
		}
		inner.field(label, truncate(r.value, 60), styles.AccentText)
	}

	if b.Len() == 0 {
		return
	}
	m.writeSection(w.b, "Media", styles, w.width)
	w.b.WriteString(b.String())
}

// metadataFieldLabel maps a metadata key to a compact row label. Returns ""
// for keys already carried by the item line and chips.
func metadataFieldLabel(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "title", "show_title", "media_type", "year":
		// Year is identity, not metadata: it lives in the item band
		// (or inside the display title itself).
		return ""
	case "id":
		return "TMDB"
	case "season_number":
		return "Season"
	default:
		return truncate(titleCase(key), detailFieldLabelWidth-1)
	}
}

// renderOutput renders produced-artifact facts: size estimate while
// encoding, then results, stats, validation, and subtitles once available.
func (m *Model) renderOutput(w fieldWriter, item spindle.QueueItem, styles Styles) {
	var b strings.Builder
	inner := fieldWriter{b: &b, styles: w.styles, width: w.width}

	renderEstimatedSize(inner, item)
	renderSizeResult(inner, item)
	renderEncodeStats(inner, item)
	renderValidationSummary(inner, item)
	renderSubtitleSummary(inner, item)
	renderFinalPath(inner, item)
	if !strings.EqualFold(item.Stage, "failed") {
		inner.field("Files", m.describeItemFileStates(item), styles.Text)
	}

	if b.Len() == 0 {
		return
	}
	m.writeSection(w.b, "Output", styles, w.width)
	w.b.WriteString(b.String())
}

// isEpisodicItem reports whether the item carries episode-level content
// worth a list: a multi-episode batch or anything TV. Movies track a single
// internal "main" asset displayed in the File tab.
func isEpisodicItem(item spindle.QueueItem) bool {
	episodes, _ := item.EpisodeSnapshot()
	for _, ep := range episodes {
		if ep.Season > 0 || ep.Episode > 0 {
			return true
		}
	}
	return len(episodes) > 1 || detectMediaType(item.Metadata) == "tv"
}

// renderEpisodeSummarySection renders the episode batch summary; the full
// per-episode list lives on the Episodes tab.
func (m *Model) renderEpisodeSummarySection(b *strings.Builder, item spindle.QueueItem, styles Styles) {
	if !isEpisodicItem(item) {
		return
	}
	episodes, totals := item.EpisodeSnapshot()

	m.writeSection(b, "Episodes", styles, 0)
	m.renderEpisodeSummary(b, item, episodes, totals, styles)
	if matched := matchedEpisodeCount(item, episodes); matched > 0 && matched < len(episodes) {
		b.WriteString(styles.WarningText.Render("⚠ Episode numbers not confirmed"))
		b.WriteString("\n")
	}
	b.WriteString(styles.FaintText.Render("Press 2 for the episode list"))
	b.WriteString("\n")
}

// sourceSummary formats a movie's primary source title, e.g.
// "Title 02 (118m)". Returns "" when no source info is available.
func sourceSummary(src *spindle.SourceTitle) string {
	if src == nil {
		return ""
	}
	value := strings.TrimSpace(src.Name)
	if value == "" {
		value = fmt.Sprintf("Title %02d", src.TitleID)
	}
	if value == "" {
		return ""
	}
	if src.DurationSeconds > 0 {
		value += " (" + formatRuntime(src.DurationSeconds) + ")"
	}
	return value
}
