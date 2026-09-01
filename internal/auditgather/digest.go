package auditgather

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/five82/spindle/internal/ripspec"
)

// Rendering caps keep pathological inputs bounded. Every cap prints an
// explicit "(+N more in JSON)" note so omissions are never silent.
const (
	maxEntriesPerGroup = 30
	maxTimestampsShown = 12
	maxExtraValueLen   = 200
)

// RenderDigest renders a deterministic, compact text digest of a report.
// It is the primary agent-facing output of `spindle queue audit`; the full
// JSON report at jsonPath remains the drill-down source of record. Sections
// appear only when the underlying data exists.
func RenderDigest(r *Report, jsonPath string) string {
	var b strings.Builder

	writeDigestHeader(&b, r, jsonPath)
	writeDigestAnomalies(&b, r)
	writeDigestErrorsWarnings(&b, r)
	writeDigestStageTimings(&b, r)
	writeDigestEvents(&b, r)
	writeDigestDecisions(&b, r)
	writeDigestRipCache(&b, r)
	writeDigestTitleSelection(&b, r)
	writeDigestEpisodeID(&b, r)
	writeDigestEncoding(&b, r)
	writeDigestGrainTreatment(&b, r)
	writeDigestFinalValidation(&b, r)
	writeDigestOutputMedia(&b, r)
	writeDigestAudio(&b, r)
	writeDigestSubtitles(&b, r)
	writeDigestRouting(&b, r)
	writeDigestAssets(&b, r)
	writeDigestTrailer(&b, jsonPath)

	return b.String()
}

func writeDigestHeader(b *strings.Builder, r *Report, jsonPath string) {
	fmt.Fprintf(b, "# Audit digest — item %d: %s\n\n", r.Item.ID, r.Item.DiscTitle)
	fmt.Fprintf(b, "Stage: %s | NeedsReview: %v | Media: %s | Source: %s | Furthest: %s\n",
		r.Item.Stage, r.Item.NeedsReview, r.StageGate.MediaType, r.StageGate.DiscSource, r.StageGate.FurthestStage)
	if r.StageGate.MediaHint != "" && r.StageGate.MediaHint != r.StageGate.MediaType {
		fmt.Fprintf(b, "Media hint: %s\n", r.StageGate.MediaHint)
	}
	if len(r.Item.ReviewReasons) > 0 {
		fmt.Fprintf(b, "Review reasons: %s\n", strings.Join(r.Item.ReviewReasons, "; "))
	}
	if r.Item.FailedAtStage != "" || r.Item.ErrorMessage != "" {
		fmt.Fprintf(b, "FAILED at %s: %s\n", r.Item.FailedAtStage, r.Item.ErrorMessage)
	}
	for _, t := range r.Item.Tasks {
		line := fmt.Sprintf("Task %s: %s", t.Type, t.State)
		if t.Attempts > 1 {
			line += fmt.Sprintf(" (attempts=%d)", t.Attempts)
		}
		if t.State == "running" {
			line += fmt.Sprintf(" %.1f%% %q", t.ProgressPercent, t.ProgressMessage)
			if t.ActiveAssetKey != "" {
				line += " asset=" + t.ActiveAssetKey
			}
		}
		if t.Error != "" {
			line += " error=" + t.Error
		}
		fmt.Fprintln(b, line)
	}
	phases := applicablePhases(r.StageGate)
	fmt.Fprintf(b, "Applicable phases: %s\n", strings.Join(phases, ", "))
	if r.Logs != nil {
		fmt.Fprintf(b, "Logs: %d lines scanned, debug=%v, files: %s\n",
			r.Logs.LinesScanned, r.Logs.IsDebug, strings.Join(r.Logs.Paths, ", "))
	}
	fmt.Fprintf(b, "Full JSON: %s\n", jsonPath)
	if len(r.Errors) > 0 {
		fmt.Fprintf(b, "\nGATHERING ERRORS (data below may be incomplete):\n")
		for _, e := range r.Errors {
			fmt.Fprintf(b, "- %s\n", e)
		}
	}
}

func applicablePhases(g StageGate) []string {
	var out []string
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(g.PhaseLogs, "logs")
	add(g.PhaseRipCache, "rip_cache")
	add(g.PhaseEpisodeID, "episode_id")
	add(g.PhaseEncoded, "encoded")
	add(g.PhaseCrop, "crop")
	add(g.PhaseSubtitles, "subtitles")
	add(g.PhaseCommentary, "commentary")
	add(g.PhaseExtVal, "external_validation")
	return out
}

func writeDigestAnomalies(b *strings.Builder, r *Report) {
	fmt.Fprintf(b, "\n## Anomalies (pre-flagged)\n")
	if r.Analysis == nil || len(r.Analysis.Anomalies) == 0 {
		fmt.Fprintln(b, "None pre-flagged. Detection is heuristic — still review every section below.")
		return
	}
	for _, a := range r.Analysis.Anomalies {
		fmt.Fprintf(b, "- [%s] %s: %s\n", strings.ToUpper(a.Severity), a.Category, a.Message)
	}
}

func writeDigestErrorsWarnings(b *strings.Builder, r *Report) {
	if r.Logs == nil {
		return
	}
	writeLogEntries(b, "Errors", r.Logs.Errors)
	writeLogEntries(b, "Warnings", r.Logs.Warnings)
}

func writeLogEntries(b *strings.Builder, label string, entries []LogEntry) {
	fmt.Fprintf(b, "\n## %s (%d)\n", label, len(entries))
	if len(entries) == 0 {
		fmt.Fprintln(b, "none")
		return
	}
	for _, e := range entries {
		line := fmt.Sprintf("- %s", shortTS(e.TS))
		if e.EventType != "" {
			line += " [" + e.EventType + "]"
		}
		line += " " + e.Message
		if e.ErrorHint != "" {
			line += " | hint: " + e.ErrorHint
		}
		if x := formatExtras(e.Extras); x != "" {
			line += " | " + x
		}
		fmt.Fprintln(b, line)
	}
}

func writeDigestStageTimings(b *strings.Builder, r *Report) {
	if r.Analysis == nil || len(r.Analysis.StageTimings) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Stage timings\n")
	for _, st := range r.Analysis.StageTimings {
		line := fmt.Sprintf("- %-24s %s -> %s", st.Stage, shortTS(st.StartedAt), shortTS(st.CompletedAt))
		if st.DurationSeconds > 0 {
			line += "  " + fmtSeconds(st.DurationSeconds)
		}
		if st.Starts > 1 || st.Completions > 1 || st.Starts != st.Completions {
			line += fmt.Sprintf("  (starts=%d completions=%d)", st.Starts, st.Completions)
		}
		fmt.Fprintln(b, line)
	}
}

func writeDigestEvents(b *strings.Builder, r *Report) {
	if r.Logs == nil || len(r.Logs.Events) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Events (%d shown", len(r.Logs.Events))
	if r.Logs.EventsOmitted > 0 {
		fmt.Fprintf(b, "; %d progress ticks omitted by downsampling — normal on long stages", r.Logs.EventsOmitted)
	}
	fmt.Fprintf(b, ")\n")
	for _, e := range r.Logs.Events {
		line := fmt.Sprintf("- %s", shortTS(e.TS))
		if e.EventType != "" {
			line += " [" + e.EventType + "]"
		}
		line += " " + e.Message
		if x := formatExtras(e.Extras); x != "" {
			line += " | " + x
		}
		fmt.Fprintln(b, line)
	}
}

func writeDigestDecisions(b *strings.Builder, r *Report) {
	if r.Analysis == nil || len(r.Analysis.DecisionGroups) == 0 {
		return
	}
	total := 0
	for _, g := range r.Analysis.DecisionGroups {
		total += g.Count
	}
	fmt.Fprintf(b, "\n## Decisions (%d in %d groups; grouped by type/result/reason, log order)\n",
		total, len(r.Analysis.DecisionGroups))
	for _, g := range r.Analysis.DecisionGroups {
		head := g.DecisionType + ": " + g.DecisionResult
		if g.DecisionReason != "" {
			head += " (" + g.DecisionReason + ")"
		}
		switch {
		case g.Count == 1 && len(g.Entries) == 1:
			e := g.Entries[0]
			line := fmt.Sprintf("- %s @ %s", head, shortTS(e.TS))
			if x := formatExtras(e.Extras); x != "" {
				line += " | " + x
			}
			fmt.Fprintln(b, line)
		case !messagesVary(g.Entries):
			// Identical repeats: one line, message/extras once, all timestamps
			// so retry spacing stays visible.
			var ts []string
			for i, e := range g.Entries {
				if i >= maxTimestampsShown {
					ts = append(ts, fmt.Sprintf("(+%d more in JSON)", len(g.Entries)-maxTimestampsShown))
					break
				}
				ts = append(ts, shortTS(e.TS))
			}
			line := fmt.Sprintf("- %s x%d @ %s", head, g.Count, strings.Join(ts, ", "))
			if len(g.Entries) > 0 {
				if x := formatExtras(g.Entries[0].Extras); x != "" {
					line += " | " + x
				}
			}
			fmt.Fprintln(b, line)
		default:
			fmt.Fprintf(b, "- %s x%d:\n", head, g.Count)
			for i, e := range g.Entries {
				if i >= maxEntriesPerGroup {
					fmt.Fprintf(b, "    (+%d more in JSON)\n", len(g.Entries)-maxEntriesPerGroup)
					break
				}
				line := "    " + shortTS(e.TS) + " " + e.Message
				if x := formatExtras(e.Extras); x != "" {
					line += " | " + x
				}
				fmt.Fprintln(b, line)
			}
		}
	}
}

func writeDigestRipCache(b *strings.Builder, r *Report) {
	rc := r.RipCache
	if rc == nil {
		return
	}
	fmt.Fprintf(b, "\n## Rip cache\n")
	switch {
	case rc.Disabled:
		fmt.Fprintln(b, "disabled in config (not a pruned entry)")
	case !rc.Found:
		fmt.Fprintf(b, "not found at %s (possibly pruned)\n", rc.Path)
	case rc.Metadata != nil:
		m := rc.Metadata
		fmt.Fprintf(b, "found: %q cached %s | %d titles | %s | %s\n",
			m.DiscTitle, m.CachedAt.Format("2006-01-02 15:04"), m.TitleCount, fmtBytes(m.TotalBytes), rc.Path)
	default:
		fmt.Fprintf(b, "found at %s (no metadata)\n", rc.Path)
	}
}

func writeDigestTitleSelection(b *strings.Builder, r *Report) {
	if r.Analysis == nil || r.Analysis.TitleSelection == nil {
		return
	}
	ts := r.Analysis.TitleSelection
	fmt.Fprintf(b, "\n## Title selection (movie)\n")
	fmt.Fprintf(b, "Selected title %d (%ds)", ts.SelectedID, ts.SelectedDurationSeconds)
	if ts.DecisionResult != "" {
		fmt.Fprintf(b, " via %s", ts.DecisionResult)
	}
	if ts.DecisionReason != "" {
		fmt.Fprintf(b, " (%s)", ts.DecisionReason)
	}
	fmt.Fprintf(b, " | %d feature-length candidates, %d similar runtime\n",
		ts.FeatureCandidateCount, ts.SimilarRuntimeCount)
	for _, c := range ts.Candidates {
		marker := " "
		if c.Selected {
			marker = "*"
		}
		line := fmt.Sprintf("%s title %d: %ds, %d chapters", marker, c.ID, c.DurationSeconds, c.Chapters)
		if c.Playlist != "" {
			line += ", " + c.Playlist
		}
		if c.SegmentCount > 0 {
			line += fmt.Sprintf(", %d segments", c.SegmentCount)
		}
		fmt.Fprintln(b, line)
	}
}

func writeDigestEpisodeID(b *strings.Builder, r *Report) {
	if r.Envelope == nil || len(r.Envelope.Episodes) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Episode manifest (%d episodes)", len(r.Envelope.Episodes))
	placeholder := r.Analysis != nil && r.Analysis.EpisodeStats != nil &&
		r.Analysis.EpisodeStats.PlaceholderOnly && !r.StageGate.PhaseEpisodeID
	if placeholder {
		fmt.Fprintf(b, " — PLACEHOLDER INVENTORY, episode identification has not run")
	}
	fmt.Fprintln(b)
	if cid := r.Envelope.Attributes.ContentID; cid != nil {
		fmt.Fprintf(b, "Content ID: method=%s references=%s (%d) | transcribed=%d matched=%d unresolved=%d low_conf=%d | synchronized=%v completed=%v\n",
			cid.Method, cid.ReferenceSource, cid.ReferenceEpisodes, cid.TranscribedEpisodes,
			cid.MatchedEpisodes, cid.UnresolvedEpisodes, cid.LowConfidenceCount,
			cid.EpisodesSynchronized, cid.Completed)
	}
	if es := statsOrNil(r); es != nil {
		fmt.Fprintf(b, "Stats: %d matched, %d unresolved | confidence min=%.2f mean=%.2f max=%.2f | <0.70: %d, <0.80: %d, <0.90: %d | contiguous=%v range=%s\n",
			es.Matched, es.Unresolved, es.ConfidenceMin, es.ConfidenceMean, es.ConfidenceMax,
			es.Below070, es.Below080, es.Below090, es.SequenceContiguous, es.EpisodeRange)
	}
	for _, ep := range r.Envelope.Episodes {
		se := fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
		if ep.EpisodeEnd > ep.Episode {
			se += fmt.Sprintf("-E%02d", ep.EpisodeEnd)
		}
		if ep.Episode == 0 {
			se = "UNRESOLVED"
		}
		line := fmt.Sprintf("- %s title_id=%d %s conf=%.2f score=%.2f",
			ep.Key, ep.TitleID, se, ep.MatchConfidence, ep.MatchScore)
		if ep.EpisodeTitle != "" {
			line += fmt.Sprintf(" %q", ep.EpisodeTitle)
		}
		if ep.NeedsReview {
			line += " REVIEW: " + ep.ReviewReason
		}
		fmt.Fprintln(b, line)
	}
}

func statsOrNil(r *Report) *EpisodeStats {
	if r.Analysis == nil {
		return nil
	}
	return r.Analysis.EpisodeStats
}

func writeDigestEncoding(b *strings.Builder, r *Report) {
	if r.Encoding == nil {
		return
	}
	s := r.Encoding.Snapshot
	fmt.Fprintf(b, "\n## Encoding snapshot")
	if r.StageGate.MediaType == "tv" {
		fmt.Fprintf(b, " (last episode encoded; config/crop/validation consistent per disc)")
	}
	fmt.Fprintln(b)
	fmt.Fprintf(b, "Encoder: %s | quality: %s | preset: %s | tune: %s | audio: %s\n",
		s.Encoder, s.Quality, s.Preset, s.Tune, s.AudioCodec)
	if s.Resolution != "" || s.DynamicRange != "" {
		fmt.Fprintf(b, "Input: %s %s\n", s.Resolution, s.DynamicRange)
	}
	if s.CropFilter != "" || s.CropRequired || s.CropMessage != "" {
		fmt.Fprintf(b, "Crop: %s required=%v", s.CropFilter, s.CropRequired)
		if ca := cropOrNil(r); ca != nil {
			fmt.Fprintf(b, " -> %dx%d %.2f:1 (%s)", ca.OutputWidth, ca.OutputHeight, ca.AspectRatio, ca.StandardRatio)
		}
		if s.CropMessage != "" {
			fmt.Fprintf(b, " | %s", s.CropMessage)
		}
		fmt.Fprintln(b)
	}
	if s.OriginalSize > 0 || s.EncodedSize > 0 {
		fmt.Fprintf(b, "Size: %s -> %s (-%.1f%%)", fmtBytes(s.OriginalSize), fmtBytes(s.EncodedSize), s.SizeReductionPercent)
		if s.EncodeDurationSeconds > 0 {
			fmt.Fprintf(b, " | encode time %s", fmtSeconds(s.EncodeDurationSeconds))
		}
		if s.AverageSpeed > 0 {
			fmt.Fprintf(b, " | avg speed %.2fx", s.AverageSpeed)
		}
		fmt.Fprintln(b)
	}
	if s.Warning != "" {
		fmt.Fprintf(b, "WARNING: %s\n", s.Warning)
	}
	if s.Error != nil {
		fmt.Fprintf(b, "ERROR: %s — %s", s.Error.Title, s.Error.Message)
		if s.Error.Suggestion != "" {
			fmt.Fprintf(b, " (suggestion: %s)", s.Error.Suggestion)
		}
		fmt.Fprintln(b)
	}
	if v := s.Validation; v != nil {
		if v.Passed {
			names := make([]string, 0, len(v.Steps))
			for _, st := range v.Steps {
				names = append(names, st.Name)
			}
			fmt.Fprintf(b, "Validation: PASSED (%s)\n", strings.Join(names, ", "))
		} else {
			fmt.Fprintln(b, "Validation: FAILED")
			for _, st := range v.Steps {
				status := "pass"
				if !st.Passed {
					status = "FAIL"
				}
				fmt.Fprintf(b, "  - %s: %s %s\n", st.Name, status, st.Details)
			}
		}
	}
}

// writeDigestGrainTreatment renders Reel's per-encode grain-gate verdict: what
// the fixed-CRF probe measured, the treatment it chose, and the denoise
// ceiling that caps what the reported target-quality scores can mean.
func writeDigestGrainTreatment(b *strings.Builder, r *Report) {
	if r.Analysis == nil || len(r.Analysis.GrainTreatments) == 0 {
		return
	}
	fmt.Fprintln(b, "\n## Grain treatment (Reel grain gate; ceiling JOD is the honest cap on reported target-quality scores)")
	for _, g := range r.Analysis.GrainTreatments {
		name := g.EpisodeKey
		if name == "" {
			name = "encode"
		}
		status := "untreated"
		if g.Treated {
			status = "TREATED " + g.Tier
		}
		line := fmt.Sprintf("- %s: %s | %s", name, status, g.ResolutionClass)
		if g.Mode != "" && g.Mode != "auto" {
			line += " | mode=" + g.Mode
		}
		if g.Denoise != "" {
			line += " | denoise " + g.Denoise
		}
		if g.GrainTable != "" {
			line += " | table " + g.GrainTable
		}
		if g.MedianBPP > 0 {
			line += fmt.Sprintf(" | median bpp %.4f vs cutoffs light %.4f / med %.4f",
				g.MedianBPP, g.LightBPPCutoff, g.MedBPPCutoff)
		}
		if g.GateCRF > 0 {
			line += fmt.Sprintf(" | gate crf %.0f, %d chunks, %s", g.GateCRF, len(g.SampleChunks), fmtSeconds(g.GateSeconds))
		}
		if g.Reason != "" {
			line += " | " + g.Reason
		}
		if g.Reused {
			line += " | verdict reused from resume"
		}
		fmt.Fprintln(b, line)
		if !g.Treated {
			continue
		}
		if g.DenoiseCeilingJODMean == nil || g.DenoiseCeilingJODMin == nil {
			reason := ""
			if g.CeilingError != "" {
				reason = ": " + g.CeilingError
			}
			fmt.Fprintf(b, "  denoise ceiling: NOT MEASURED (reported scores have no honest cap%s)\n", reason)
			continue
		}
		fmt.Fprintf(b, "  denoise ceiling: JOD mean %.2f min %.2f (measured in %s)\n",
			*g.DenoiseCeilingJODMean, *g.DenoiseCeilingJODMin, fmtSeconds(g.CeilingSeconds))
	}
}

func cropOrNil(r *Report) *CropAnalysis {
	if r.Analysis == nil {
		return nil
	}
	return r.Analysis.CropAnalysis
}

// writeDigestFinalValidation renders the apply stage's persisted verdict on
// the delivered outputs, including its independent A/V sync comparison
// against the ripped source.
func writeDigestFinalValidation(b *strings.Builder, r *Report) {
	if r.Analysis == nil || r.Analysis.FinalValidation == nil {
		return
	}
	fmt.Fprintln(b, "\n## Final output validation (apply stage, against ripped source)")
	for _, entry := range r.Analysis.FinalValidation.Entries {
		name := entry.EpisodeKey
		if name == "" {
			name = filepath.Base(entry.OutputPath)
		}
		switch {
		case entry.Error != "":
			fmt.Fprintf(b, "- %s: UNAVAILABLE (%s)\n", name, entry.Error)
		case len(entry.FailedChecks) > 0:
			fmt.Fprintf(b, "- %s: FAILED | %s\n", name, strings.Join(entry.FailedChecks, "; "))
		default:
			fmt.Fprintf(b, "- %s: PASSED\n", name)
		}
		writeDigestAVSync(b, entry.AVSync)
	}
}

func writeDigestAVSync(b *strings.Builder, check *ripspec.AVSyncCheck) {
	if check == nil {
		return
	}
	if check.Error != "" {
		fmt.Fprintf(b, "  A/V sync: UNAVAILABLE (%s)\n", check.Error)
		return
	}
	status := "PASSED"
	if !check.Passed {
		status = "FAILED"
	}
	drift := check.DriftMilliseconds
	direction := "later"
	if drift < 0 {
		drift = -drift
		direction = "earlier"
	}
	fmt.Fprintf(b, "  A/V sync: %s | source offset %+.0fms -> output %+.0fms | audio %.0fms %s\n",
		status, check.SourceAudioOffsetSec*1000, check.OutputAudioOffsetSec*1000, drift, direction)
}

func writeDigestOutputMedia(b *strings.Builder, r *Report) {
	hasSummaries := r.Analysis != nil && len(r.Analysis.OutputMedia) > 0
	var probeErrors []MediaFileProbe
	for _, m := range r.Media {
		if m.Error != "" {
			probeErrors = append(probeErrors, m)
		}
	}
	if !hasSummaries && len(probeErrors) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Output files")
	if r.MediaOmitted > 0 {
		fmt.Fprintf(b, " (%d clean probes omitted — representative probe covers them; consistency below)", r.MediaOmitted)
	}
	fmt.Fprintln(b)
	if hasSummaries {
		for _, m := range r.Analysis.OutputMedia {
			name := m.Path
			if m.EpisodeKey != "" {
				name = m.EpisodeKey + " " + name
			}
			fmt.Fprintf(b, "- %s | %.0fs | %s\n", name, m.DurationSeconds, fmtBytes(m.SizeBytes))
			if v := m.Video; v != nil {
				hdr := "SDR"
				if v.HDR {
					hdr = fmt.Sprintf("HDR (%s/%s)", v.ColorTransfer, v.ColorPrimaries)
				}
				fmt.Fprintf(b, "  video: %s %dx%d %s\n", v.Codec, v.Width, v.Height, hdr)
			}
			for _, a := range m.Audio {
				fmt.Fprintf(b, "  audio #%d: %s %s %dch (%s) %q%s\n",
					a.Index, a.Language, a.Codec, a.Channels, a.Layout, a.Title, audioFlags(a))
			}
			for _, s := range m.Subtitles {
				flags := ""
				if s.Default {
					flags += " DEFAULT"
				}
				if s.Forced {
					flags += " FORCED"
				}
				fmt.Fprintf(b, "  sub #%d: %s %s %q%s\n", s.Index, s.Language, s.Codec, s.Title, flags)
			}
		}
	}
	for _, m := range probeErrors {
		fmt.Fprintf(b, "- PROBE ERROR %s (%s): %s\n", m.Path, m.EpisodeKey, m.Error)
	}
	if r.Analysis != nil && r.Analysis.MediaStats != nil {
		ms := r.Analysis.MediaStats
		fmt.Fprintf(b, "Across %d files: duration %.0f-%.0fs | size %s-%s\n",
			ms.FileCount, ms.DurationMinSec, ms.DurationMaxSec, fmtBytes(ms.SizeMinBytes), fmtBytes(ms.SizeMaxBytes))
	}
	if r.Analysis != nil && r.Analysis.EpisodeConsistency != nil {
		ec := r.Analysis.EpisodeConsistency
		p := ec.MajorityProfile
		fmt.Fprintf(b, "Consistency: %d/%d episodes match majority profile %s %dx%d, %d audio, %d subs\n",
			ec.MajorityCount, ec.TotalEpisodes, p.VideoCodec, p.Width, p.Height,
			len(p.AudioStreams), len(p.SubtitleStreams))
		for _, d := range ec.Deviations {
			fmt.Fprintf(b, "  DEVIATION %s: %s\n", d.EpisodeKey, strings.Join(d.Differences, "; "))
		}
	}
}

func audioFlags(a AudioStreamSummary) string {
	var flags string
	if a.Default {
		flags += " DEFAULT"
	}
	if a.Commentary {
		flags += " COMMENTARY"
	}
	return flags
}

func writeDigestAudio(b *strings.Builder, r *Report) {
	if r.Analysis == nil || r.Analysis.AudioSummary == nil {
		return
	}
	a := r.Analysis.AudioSummary
	fmt.Fprintf(b, "\n## Audio\n")
	fmt.Fprintf(b, "Primary: track %d %s | output tracks: %d (commentary: %d)\n",
		a.PrimaryTrackIndex, a.PrimaryDescription, a.OutputAudioTracks, a.OutputCommentaryTracks)
	for _, ex := range a.ExcludedTracks {
		line := fmt.Sprintf("- excluded track %d: %s", ex.Index, ex.Reason)
		if ex.Similarity > 0 {
			line += fmt.Sprintf(" (similarity %.2f)", ex.Similarity)
		}
		fmt.Fprintln(b, line)
	}
}

func writeDigestSubtitles(b *strings.Builder, r *Report) {
	if r.Analysis == nil || r.Analysis.SubtitleSummary == nil {
		return
	}
	s := r.Analysis.SubtitleSummary
	fmt.Fprintf(b, "\n## Subtitles\n")
	fmt.Fprintf(b, "Validation: %d passed, %d needs_review, %d failed, %d skipped | output tracks: %d\n",
		s.ValidationPassed, s.ValidationNeedsReview, s.ValidationFailed, s.Skipped, s.OutputSubtitleTracks)
	for _, res := range s.Results {
		line := fmt.Sprintf("- %s: %s (%s, %d segments)", res.EpisodeKey, res.ValidationResult, res.Source, res.Segments)
		if len(res.SevereIssues) > 0 {
			line += " | SEVERE: " + strings.Join(res.SevereIssues, "; ")
		}
		if len(res.ReviewIssues) > 0 {
			line += " | review: " + strings.Join(res.ReviewIssues, "; ")
		}
		if len(res.QCObservations) > 0 {
			line += " | qc (telemetry): " + strings.Join(res.QCObservations, "; ")
		}
		fmt.Fprintln(b, line)
	}
}

func writeDigestRouting(b *strings.Builder, r *Report) {
	if r.Analysis == nil || r.Analysis.RoutingSummary == nil || len(r.Analysis.RoutingSummary.Entries) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Final routing\n")
	for _, e := range r.Analysis.RoutingSummary.Entries {
		status := "as expected"
		if !e.MatchesExpected {
			status = "MISMATCH"
		}
		expected := "library"
		if e.ExpectedReview {
			expected = "review"
		}
		name := e.Path
		if e.EpisodeKey != "" {
			name = e.EpisodeKey + " " + name
		}
		fmt.Fprintf(b, "- %s -> %s (expected %s) [%s]\n", name, e.Destination, expected, status)
	}
}

func writeDigestAssets(b *strings.Builder, r *Report) {
	if r.Analysis == nil || r.Analysis.AssetHealth == nil {
		return
	}
	h := r.Analysis.AssetHealth
	var parts []string
	add := func(name string, c *AssetCounts) {
		if c == nil {
			return
		}
		p := fmt.Sprintf("%s %d/%d ok", name, c.OK, c.Total)
		if c.Failed > 0 {
			p += fmt.Sprintf(" (%d FAILED)", c.Failed)
		}
		if c.Muxed > 0 {
			p += fmt.Sprintf(" (muxed %d)", c.Muxed)
		}
		parts = append(parts, p)
	}
	add("ripped", h.Ripped)
	add("encoded", h.Encoded)
	add("subtitled", h.Subtitled)
	add("final", h.Final)
	add("transcript", h.Transcript)
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Asset health\n%s\n", strings.Join(parts, " | "))
	if r.Analysis.SourceSummary != nil {
		s := r.Analysis.SourceSummary
		fmt.Fprintf(b, "Source: %s uhd_likely=%v | input %s [%s] -> output %s %s | dynamic range: %s hdr=%v\n",
			s.DiscSource, s.UHDLikely, s.InputResolution, strings.Join(s.InputCodecs, ","),
			s.OutputResolution, s.OutputCodec, s.DynamicRange, s.HDR)
	}
}

func writeDigestTrailer(b *strings.Builder, jsonPath string) {
	fmt.Fprintf(b, "\n## Digest limits\n")
	fmt.Fprintf(b, "Not in this digest: raw ffprobe stream fields (media[].probe), full envelope titles/assets, "+
		"and extras values longer than %d chars (truncated above). All of it is in the full JSON: %s\n",
		maxExtraValueLen, jsonPath)
	fmt.Fprintln(b, "This digest is a starting point, not the audit. Investigate every anomaly, warning, error, and suspicious value in the full JSON before reporting.")
}

// shortTS compacts an RFC3339 timestamp to "01-02 15:04:05" for display.
// Unparseable or empty inputs are returned as-is.
func shortTS(ts string) string {
	if ts == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.Format("01-02 15:04:05")
}

func fmtSeconds(s float64) string {
	d := time.Duration(s * float64(time.Second))
	return d.Truncate(time.Second).String()
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n > 0:
		return fmt.Sprintf("%d B", n)
	default:
		return "0 B"
	}
}

// formatExtras renders an extras map as sorted "k=v" pairs. Long values are
// truncated with an explicit marker pointing at the full JSON.
func formatExtras(extras map[string]any) string {
	if len(extras) == 0 {
		return ""
	}
	keys := make([]string, 0, len(extras))
	for k := range extras {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := fmt.Sprint(extras[k])
		if len(v) > maxExtraValueLen {
			v = v[:maxExtraValueLen] + "…(truncated, full value in JSON)"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}
