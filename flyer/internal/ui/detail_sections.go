package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/flyer/internal/spindle"
)

// renderEstimatedSize renders the estimated output size during encoding.
// Only displays when progress >= 10% for estimate accuracy.
func renderEstimatedSize(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil {
		return
	}
	// Only show after 10% progress for accuracy
	if enc.Percent < 10 {
		return
	}
	if enc.EstimatedTotalBytes <= 0 {
		return
	}
	// Once the final size is known the estimate is stale noise.
	if enc.EncodedSize > 0 {
		return
	}

	value := "~" + formatBytes(enc.EstimatedTotalBytes)
	if enc.CurrentOutputBytes > 0 {
		value += fmt.Sprintf(" (%s written)", formatBytes(enc.CurrentOutputBytes))
	}
	w.field("Est", value, w.styles.AccentText)
}

// renderSizeResult renders the file size comparison (input -> output with reduction %).
func renderSizeResult(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil || enc.OriginalSize <= 0 || enc.EncodedSize <= 0 {
		return
	}

	value := formatBytes(enc.OriginalSize) + " -> " + formatBytes(enc.EncodedSize) +
		fmt.Sprintf(" (%.0f%% reduction)", enc.SizeReductionPercent)
	w.field("Size", value, w.styles.Text)
}

// renderVideoSpecs renders the video specs line: source resolution, the
// cropped resolution when a crop was applied, and HDR status. One honest
// row; the raw crop filter stays in the logs.
func renderVideoSpecs(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil || enc.Resolution == "" {
		return
	}

	res := enc.Resolution
	cropped := ""
	if enc.CropRequired && enc.CropFilter != "" {
		if dims := strings.TrimPrefix(enc.CropFilter, "crop="); dims != enc.CropFilter {
			if fields := strings.SplitN(dims, ":", 3); len(fields) >= 2 {
				cropped = fields[0] + "x" + fields[1]
			}
		}
	}

	var parts []string
	if cropped != "" && cropped != res {
		parts = append(parts, res+" -> "+cropped)
	} else {
		parts = append(parts, res)
	}
	if enc.DynamicRange != "" {
		parts = append(parts, strings.ToUpper(enc.DynamicRange))
	}
	if cropped != "" && cropped != res {
		parts = append(parts, "(cropped)")
	}

	w.field("Video", strings.Join(parts, " "), w.styles.AccentText)
}

// renderAudioInfo renders the source audio format. The daemon reports it
// pipe-separated; normalize to the middot the rest of the overview uses.
func renderAudioInfo(w fieldWriter, item spindle.QueueItem) {
	desc := strings.ReplaceAll(item.PrimaryAudioDescription, " | ", " · ")
	desc = strings.ReplaceAll(desc, "|", "·")
	w.field("Audio", desc, w.styles.Text)
}

// renderEncodingConfig renders the encoding config: a scannable headline row
// (encoder + preset + tune) with reel's verbose quality string on its own row.
func renderEncodingConfig(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil || enc.Preset == "" {
		return
	}
	var parts []string

	if enc.Encoder != "" {
		parts = append(parts, enc.Encoder)
	}
	parts = append(parts, fmt.Sprintf("Preset %s", enc.Preset))
	if enc.Tune != "" {
		parts = append(parts, fmt.Sprintf("Tune %s", enc.Tune))
	}

	w.field("Config", strings.Join(parts, " · "), w.styles.AccentText)
	w.field("Quality", summarizeQuality(enc.Quality), w.styles.AccentText)
}

// summarizeQuality trims reel's quality description to what an operator
// cares about: the metric and its target band, or the fixed CRF and tier.
// Target-quality mode appends a parenthetical describing the CRF search
// machinery (initial CRF, adaptive priors, probe strategy, worker count);
// that is dropped. Parentheticals without search machinery -- fixed-CRF
// mode's resolution tier in "CRF 26 (UHD)" -- are kept.
func summarizeQuality(q string) string {
	q = strings.TrimSpace(q)
	open := strings.LastIndex(q, "(")
	if open <= 0 || !strings.HasSuffix(q, ")") {
		return q
	}
	paren := q[open:]
	for _, marker := range []string{"initial CRF", "CRF search", "metric workers"} {
		if strings.Contains(paren, marker) {
			return strings.TrimSpace(q[:open])
		}
	}
	return q
}

// renderContentID renders the episode identification summary: method and
// match counts, the reference corpus, and sequence problems a completed
// identification left behind.
func renderContentID(w fieldWriter, item spindle.QueueItem) {
	cid := item.ContentID
	if cid == nil || strings.TrimSpace(cid.Method) == "" {
		return
	}
	value := cid.Method
	if cid.TranscribedEpisodes > 0 || cid.MatchedEpisodes > 0 {
		value += fmt.Sprintf(" · %d matched · %d unresolved · %d low confidence",
			cid.MatchedEpisodes, cid.UnresolvedEpisodes, cid.LowConfidenceCount)
	}
	w.field("ID", value, w.styles.Text)

	if src := strings.TrimSpace(cid.ReferenceSource); src != "" {
		ref := src
		if cid.ReferenceEpisodes > 0 {
			ref += fmt.Sprintf(" · %d reference episodes", cid.ReferenceEpisodes)
		}
		w.field("Ref", ref, w.styles.Text)
	}

	// The flags only mean something once identification has finished.
	if cid.Completed {
		if !cid.SequenceContiguous {
			w.fieldStyled("", w.styles.MutedText, "⚠ Episode sequence not contiguous", w.styles.WarningText)
		}
		if !cid.EpisodesSynchronized {
			w.fieldStyled("", w.styles.MutedText, "⚠ Episodes not synchronized", w.styles.WarningText)
		}
	}
}

// renderEncodeStats renders duration and average speed (for completed).
func renderEncodeStats(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil || (enc.EncodeDurationSeconds <= 0 && enc.AverageSpeed <= 0) {
		return
	}

	var parts []string
	if enc.EncodeDurationSeconds > 0 {
		dur := time.Duration(enc.EncodeDurationSeconds * float64(time.Second))
		parts = append(parts, humanizeDurationLong(dur))
	}
	if enc.AverageSpeed > 0 {
		parts = append(parts, fmt.Sprintf("%.1fx avg", enc.AverageSpeed))
	}

	w.field("Encode", strings.Join(parts, " @ "), w.styles.Text)
}

// renderValidationSummary renders the validation result. Passed runs also
// list the named checks -- "what was actually verified" without a tab
// switch; failing runs get their step list in the Attention section, so
// only the summary row renders here.
func renderValidationSummary(w fieldWriter, item spindle.QueueItem) {
	if item.Encoding == nil || item.Encoding.Validation == nil {
		return
	}
	v := item.Encoding.Validation
	total := len(v.Steps)
	if total == 0 {
		return
	}

	passed := 0
	for _, step := range v.Steps {
		if step.Passed {
			passed++
		}
	}

	value := fmt.Sprintf("%d/%d", passed, total)
	if !v.Passed {
		w.field("Checks", "Failed · "+value, w.styles.DangerText)
		return
	}
	w.field("Checks", "Passed · "+value, w.styles.SuccessText)
	for _, step := range v.Steps {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			continue
		}
		icon, iconStyle := "✓", w.styles.SuccessText
		if !step.Passed {
			icon, iconStyle = "✗", w.styles.DangerText
		}
		w.b.WriteString(strings.Repeat(" ", detailFieldLabelWidth))
		w.b.WriteString(iconStyle.Render(icon))
		w.b.WriteString(" ")
		w.b.WriteString(w.styles.MutedText.Render(name))
		if details := strings.TrimSpace(step.Details); details != "" {
			w.b.WriteString(" ")
			w.b.WriteString(w.styles.FaintText.Render(details))
		}
		w.b.WriteString("\n")
	}
}

// renderFinalPath renders where finished files landed: the file path for
// single-file items, the shared directory once a batch has final files.
func renderFinalPath(w fieldWriter, item spindle.QueueItem) {
	episodes, _ := item.EpisodeSnapshot()
	var paths []string
	for _, ep := range episodes {
		if p := strings.TrimSpace(ep.FinalPath); p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return
	}
	value := paths[0]
	if len(paths) > 1 {
		value = filepath.Dir(paths[0]) + "/"
	}
	w.field("Path", value, w.styles.Text)
}

// renderSubtitleSummary renders the subtitle source summary: a count for
// multi-episode items, a plain source label for movies and single items.
func renderSubtitleSummary(w fieldWriter, item spindle.QueueItem) {
	episodes, _ := item.EpisodeSnapshot()
	if len(episodes) == 0 {
		return
	}

	count := 0
	for _, ep := range episodes {
		if strings.EqualFold(strings.TrimSpace(ep.SubtitleSource), "whisperx") {
			count++
		}
	}
	if count == 0 {
		return
	}

	if len(episodes) == 1 {
		w.field("Subs", "WhisperX", w.styles.AccentText)
		return
	}
	w.field("Subs", fmt.Sprintf("%d WhisperX", count), w.styles.AccentText)
}
