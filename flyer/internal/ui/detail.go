package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/five82/flyer/internal/spindle"
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
	lines := wrapText(value, max(w.width-detailFieldLabelWidth, 20))
	w.b.WriteString(labelStyle.Render(fmt.Sprintf("%-*s", detailFieldLabelWidth, label)))
	w.b.WriteString(valueStyle.Render(lines[0]))
	w.b.WriteString("\n")
	for _, line := range lines[1:] {
		w.b.WriteString(strings.Repeat(" ", detailFieldLabelWidth))
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
	label := info.label
	if item.IsTerminal() {
		label = info.doneLabel
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
func needsAttention(item spindle.QueueItem) bool {
	if item.NeedsReview || strings.TrimSpace(item.ErrorMessage) != "" {
		return true
	}
	if item.FailedTask() != nil || strings.EqualFold(item.Stage, "failed") {
		return true
	}
	if item.Encoding != nil && item.Encoding.Error != nil {
		return true
	}
	if item.Encoding != nil && strings.TrimSpace(item.Encoding.Warning) != "" {
		return true
	}
	if v := itemValidation(item); v != nil && !v.Passed && len(v.Steps) > 0 {
		return true
	}
	return false
}

func itemValidation(item spindle.QueueItem) *spindle.EncodingValidation {
	if item.Encoding == nil {
		return nil
	}
	return item.Encoding.Validation
}

// renderAttention renders the single home for review/error information.
// Renders nothing when the item is healthy.
func (m *Model) renderAttention(w fieldWriter, item spindle.QueueItem, styles Styles) {
	if !needsAttention(item) {
		return
	}
	m.writeSection(w.b, "Attention", styles, w.width)

	// Review reason(s)
	if item.NeedsReview {
		reason := strings.Join(item.ReviewReasons, "; ")
		if reason == "" {
			reason = "Needs operator review"
		}
		w.fieldStyled("Review", styles.WarningText, reason, styles.Text)
	}

	// Error message
	if msg := strings.TrimSpace(item.ErrorMessage); msg != "" {
		w.fieldStyled("Error", styles.DangerText, msg, styles.Text)
	}

	// Detailed error from Reel
	if item.Encoding != nil && item.Encoding.Error != nil {
		err := item.Encoding.Error
		if title := strings.TrimSpace(err.Title); title != "" && title != strings.TrimSpace(item.ErrorMessage) {
			w.field("Cause", title, styles.Text)
		}
		w.field("Context", strings.TrimSpace(err.Context), styles.Text)
		w.field("Suggest", strings.TrimSpace(err.Suggestion), styles.SuccessText)
	}

	// Non-fatal encoder warning (e.g. a fallback Reel took mid-encode).
	if item.Encoding != nil {
		w.fieldStyled("Warning", styles.WarningText, strings.TrimSpace(item.Encoding.Warning), styles.Text)
	}

	// Failure position for items whose task board can't show it.
	if stage := strings.TrimSpace(item.FailedAtStage); stage != "" && len(item.Tasks) == 0 {
		w.fieldStyled("Failed", styles.DangerText, stageDisplay(stage).label, styles.Text)
	}

	// Leftover file state helps recovery decisions after a failure.
	if item.FailedTask() != nil || strings.EqualFold(item.Stage, "failed") {
		w.field("Files", m.describeItemFileStates(item), styles.Text)
	}

	// Failing validation steps
	if v := itemValidation(item); v != nil && !v.Passed && len(v.Steps) > 0 {
		for _, step := range v.Steps {
			icon, iconStyle := "✓", styles.SuccessText
			if !step.Passed {
				icon, iconStyle = "✗", styles.DangerText
			}
			name := strings.TrimSpace(step.Name)
			if name == "" {
				name = "Check"
			}
			w.b.WriteString(iconStyle.Render(icon))
			w.b.WriteString(" ")
			w.b.WriteString(styles.Text.Render(name))
			if details := strings.TrimSpace(step.Details); details != "" {
				w.b.WriteString(" ")
				w.b.WriteString(styles.FaintText.Render(details))
			}
			w.b.WriteString("\n")
		}
	}
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
// internal "main" episode that has no list value of its own.
func isEpisodicItem(item spindle.QueueItem) bool {
	episodes, _ := item.EpisodeSnapshot()
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
	if value == "" && src.TitleID > 0 {
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
