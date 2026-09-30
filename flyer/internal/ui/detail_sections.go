package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/flyer/internal/spindle"
)

func renderEstimatedSize(w fieldWriter, item spindle.QueueItem) {
	for _, task := range item.Tasks {
		if e := task.Encoding; task.IsWorking() && e != nil && e.Percent >= 10 && e.EstimatedTotalBytes > 0 && e.EncodedSize == 0 {
			w.field("Est. size", "~"+formatBytes(e.EstimatedTotalBytes)+" (before Apply)", w.styles.AccentText)
		}
	}
}

func renderSizeResult(w fieldWriter, item spindle.QueueItem) {
	var delivered, intermediate, original int64
	var published, measured, originals int
	for _, ep := range item.Episodes {
		if ep.FinalPath != "" {
			published++
			if ep.FinalSizeBytes > 0 {
				delivered += ep.FinalSizeBytes
				measured++
			}
			if s := ep.EncodeStats; s != nil && s.OriginalSizeBytes > 0 {
				original += s.OriginalSizeBytes
				originals++
			}
		}
		if ep.EncodeStats != nil {
			intermediate += ep.EncodeStats.EncodedSizeBytes
		}
	}
	if published > 0 {
		value := pluralize(published, "file") + " published"
		if measured == published {
			value += "; " + formatBytes(delivered) + " delivered"
		} else {
			value += "; total size unavailable"
		}
		if strings.EqualFold(item.Stage, "completed") && originals == published && measured == published && original > delivered {
			value += fmt.Sprintf(" (-%.0f%% vs source)", 100*(1-float64(delivered)/float64(original)))
		}
		w.field("Output", value, w.styles.Text)
	}
	if intermediate > 0 {
		w.field("Encoded", formatBytes(intermediate)+" (before Apply)", w.styles.MutedText)
	}
}

func renderVideoSpecs(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil || enc.Resolution == "" {
		return
	}
	value := enc.Resolution
	if enc.OutputResolution != "" && enc.OutputResolution != enc.Resolution {
		value += " -> " + enc.OutputResolution + " cropped"
	}
	if enc.DynamicRange != "" {
		value += " " + enc.DynamicRange
	}
	w.field("Video", value+" ("+enc.InputFile+")", w.styles.AccentText)
}

func renderAudioInfo(w fieldWriter, item spindle.QueueItem) {
	// Apply can rewrite audio. Never relabel its final facts as source audio.
	for _, ep := range item.Episodes {
		if v := ep.FinalValidation; v != nil && len(v.Audio) > 0 {
			w.field("Audio out", ep.Key+": "+strings.Join(v.Audio, "; "), w.styles.Text)
		}
	}
	if item.PrimaryAudioDescription != "" {
		w.field("Primary", strings.ReplaceAll(item.PrimaryAudioDescription, "|", ";"), w.styles.Text)
	}
}

func renderEncodingConfig(w fieldWriter, item spindle.QueueItem) {
	enc := item.Encoding
	if enc == nil || enc.Preset == "" {
		return
	}
	w.field("Config", fmt.Sprintf("%s preset %s; tune %s", enc.Encoder, enc.Preset, enc.Tune), w.styles.MutedText)
	w.field("Quality", summarizeQuality(enc.Quality), w.styles.MutedText)
}

func summarizeQuality(q string) string {
	q = strings.TrimSpace(q)
	open := strings.LastIndex(q, "(")
	if open <= 0 || !strings.HasSuffix(q, ")") {
		return q
	}
	for _, marker := range []string{"initial CRF", "CRF search", "metric workers"} {
		if strings.Contains(q[open:], marker) {
			return strings.TrimSpace(q[:open])
		}
	}
	return q
}

func renderContentID(w fieldWriter, item spindle.QueueItem) {
	c := item.ContentID
	if c == nil || strings.TrimSpace(c.Method) == "" {
		return
	}
	// Counts lead (zero unresolved/review counts are noise); the daemon's
	// method identifier is provenance, so it trails.
	matching := fmt.Sprintf("%d matched", c.MatchedEpisodes)
	if c.UnresolvedEpisodes > 0 {
		matching += fmt.Sprintf("; %d unresolved", c.UnresolvedEpisodes)
	}
	if c.ReviewEpisodes > 0 {
		matching += fmt.Sprintf("; %d for review", c.ReviewEpisodes)
	}
	w.field("Matching", matching+" (via "+c.Method+")", w.styles.Text)
	w.field("Catalog", fmt.Sprintf("%s; %d candidate episodes", c.ReferenceSource, c.ReferenceEpisodes), w.styles.Text)
	if c.Completed && !c.SequenceContiguous {
		w.field("Sequence", "Episode sequence not contiguous", w.styles.WarningText)
	}
}

func renderEncodeStats(w fieldWriter, item spindle.QueueItem) {
	var seconds float64
	var count int
	for _, ep := range item.Episodes {
		if s := ep.EncodeStats; s != nil && s.EncodeSeconds > 0 {
			seconds += s.EncodeSeconds
			count++
		}
	}
	if count > 0 {
		w.field("Encode", fmt.Sprintf("%s; %s file wall time (excludes queue waits)", pluralize(count, "file"), formatDuration(time.Duration(seconds*float64(time.Second)))), w.styles.Text)
	}
}

func renderValidationSummary(w fieldWriter, item spindle.QueueItem) {
	counts := make(map[string]int)
	for _, ep := range item.Episodes {
		counts[ep.FinalValidation.Verdict()]++
	}
	if len(item.Episodes) == 0 {
		w.field("Checks", "Final checks not run yet", w.styles.MutedText)
		return
	}
	// Final checks run on published files, after Apply; until then they
	// are pending rather than skipped.
	var parts []string
	for _, state := range []string{"failed", "unavailable", "not run", "passed"} {
		if n := counts[state]; n > 0 {
			label := state
			if state == "not run" {
				label = "pending"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
		}
	}
	style := w.styles.SuccessText
	if counts["unavailable"]+counts["not run"] > 0 {
		style = w.styles.MutedText
	}
	if counts["failed"] > 0 {
		style = w.styles.DangerText
	}
	w.field("Checks", strings.Join(parts, "; "), style)
}

func renderFinalPath(w fieldWriter, item spindle.QueueItem) {
	var paths []string
	for _, ep := range item.Episodes {
		if ep.FinalPath != "" {
			paths = append(paths, ep.FinalPath)
		}
	}
	if len(paths) == 0 {
		return
	}
	value := paths[0]
	if len(paths) > 1 {
		value = filepath.Dir(value) + "/ (per-file destinations in tab 2)"
	}
	w.field("Path", value, w.styles.Text)
}

func renderSubtitleSummary(w fieldWriter, item spindle.QueueItem) {
	var adopted, applied, skipped int
	for _, ep := range item.Episodes {
		if ep.SubtitleSource == "opensubtitles" {
			adopted++
		}
		if ep.SubtitledPath != "" {
			applied++
		}
		if ep.SubtitleSource == "none" {
			skipped++
		}
	}
	if adopted+skipped > 0 {
		w.field("Subs", fmt.Sprintf("%d OpenSubtitles SRT adopted; %d applied; %d skipped", adopted, applied, skipped), w.styles.Text)
	}
}
