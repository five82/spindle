package auditgather

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"spindle/internal/encodingstate"
	"spindle/internal/ripspec"
)

// computeAnalysis derives pre-computed summaries from the collected report data.
func computeAnalysis(r *Report) *Analysis {
	a := &Analysis{}

	if r.Logs != nil && len(r.Logs.Decisions) > 0 {
		a.DecisionGroups = aggregateDecisions(r.Logs.Decisions)
	}

	if len(r.Media) > 0 {
		a.EpisodeConsistency = computeEpisodeConsistency(r.Media)
		a.MediaStats = computeMediaStats(r.Media)
	}

	if r.Encoding != nil && r.Encoding.Snapshot.Crop != nil {
		a.CropAnalysis = analyzeCrop(r.Encoding.Snapshot.Crop)
	}

	if r.Envelope != nil && len(r.Envelope.Episodes) > 0 {
		a.EpisodeStats = computeEpisodeStats(r.Envelope.Episodes)
	}

	if r.Envelope != nil {
		a.AssetHealth = computeAssetHealth(&r.Envelope.Assets)
	}

	a.Anomalies = detectAnomalies(r, a)

	// Return nil if everything is empty.
	if len(a.DecisionGroups) == 0 &&
		a.EpisodeConsistency == nil &&
		a.CropAnalysis == nil &&
		a.EpisodeStats == nil &&
		a.MediaStats == nil &&
		a.AssetHealth == nil &&
		len(a.Anomalies) == 0 {
		return nil
	}

	return a
}

// aggregateDecisions groups decisions by (type, result, reason), preserving
// insertion order. Entries are included when count==1 or when messages vary.
func aggregateDecisions(decisions []LogDecision) []DecisionGroup {
	type groupKey struct {
		decType, result, reason string
	}

	var order []groupKey
	groups := make(map[groupKey]*DecisionGroup)

	for _, d := range decisions {
		k := groupKey{d.DecisionType, d.DecisionResult, d.DecisionReason}
		g, ok := groups[k]
		if !ok {
			g = &DecisionGroup{
				DecisionType:   d.DecisionType,
				DecisionResult: d.DecisionResult,
				DecisionReason: d.DecisionReason,
			}
			groups[k] = g
			order = append(order, k)
		}
		g.Count++
		g.Entries = append(g.Entries, d)
	}

	result := make([]DecisionGroup, 0, len(order))
	for _, k := range order {
		g := *groups[k]
		if g.Count > 1 && !messagesVary(g.Entries) {
			// All identical - drop individual entries.
			g.Entries = nil
		}
		result = append(result, g)
	}
	return result
}

// messagesVary returns true if the log messages differ across entries.
func messagesVary(entries []LogDecision) bool {
	if len(entries) <= 1 {
		return false
	}
	first := entries[0].Message
	for _, e := range entries[1:] {
		if e.Message != first {
			return true
		}
	}
	return false
}

// computeEpisodeConsistency compares media profiles across TV episodes.
// Returns nil if fewer than 2 valid probes exist.
func computeEpisodeConsistency(probes []MediaFileProbe) *EpisodeConsistency {
	type profileEntry struct {
		key     string
		summary ProfileSummary
	}

	var entries []profileEntry
	for _, p := range probes {
		if p.Error != "" || p.EpisodeKey == "" {
			continue
		}
		entries = append(entries, profileEntry{
			key:     p.EpisodeKey,
			summary: buildProfileSummary(p),
		})
	}

	if len(entries) < 2 {
		return nil
	}

	// Find majority profile by counting matching summaries.
	type profileCount struct {
		summary ProfileSummary
		count   int
	}
	var counts []profileCount
	for _, e := range entries {
		found := false
		for i := range counts {
			if profilesEqual(counts[i].summary, e.summary) {
				counts[i].count++
				found = true
				break
			}
		}
		if !found {
			counts = append(counts, profileCount{summary: e.summary, count: 1})
		}
	}

	// Find the most common profile.
	best := 0
	for i, c := range counts {
		if c.count > counts[best].count {
			best = i
		}
	}
	majority := counts[best].summary
	majorityCount := counts[best].count

	// Find deviations.
	var deviations []ProfileDeviation
	for _, e := range entries {
		if profilesEqual(majority, e.summary) {
			continue
		}
		diffs := describeProfileDifferences(majority, e.summary)
		if len(diffs) > 0 {
			deviations = append(deviations, ProfileDeviation{
				EpisodeKey:  e.key,
				Differences: diffs,
			})
		}
	}

	return &EpisodeConsistency{
		MajorityProfile: majority,
		MajorityCount:   majorityCount,
		TotalEpisodes:   len(entries),
		Deviations:      deviations,
	}
}

func buildProfileSummary(p MediaFileProbe) ProfileSummary {
	var ps ProfileSummary
	var subtitleCount int
	for _, s := range p.Probe.Streams {
		switch strings.ToLower(s.CodecType) {
		case "video":
			ps.VideoCodec = s.CodecName
			ps.Width = s.Width
			ps.Height = s.Height
		case "audio":
			ap := AudioProfile{
				Codec:         s.CodecName,
				Channels:      s.Channels,
				ChannelLayout: s.ChannelLayout,
				Language:      s.Tags["language"],
				IsDefault:     s.Disposition["default"] == 1,
			}
			title := strings.ToLower(s.Tags["title"])
			if strings.Contains(title, "commentary") {
				ap.IsCommentary = true
			}
			ps.AudioStreams = append(ps.AudioStreams, ap)
		case "subtitle":
			subtitleCount++
		}
	}
	ps.SubtitleCount = subtitleCount
	return ps
}

func profilesEqual(a, b ProfileSummary) bool {
	if a.VideoCodec != b.VideoCodec || a.Width != b.Width || a.Height != b.Height || a.SubtitleCount != b.SubtitleCount {
		return false
	}
	if len(a.AudioStreams) != len(b.AudioStreams) {
		return false
	}
	for i := range a.AudioStreams {
		if a.AudioStreams[i] != b.AudioStreams[i] {
			return false
		}
	}
	return true
}

func describeProfileDifferences(majority, other ProfileSummary) []string {
	var diffs []string
	if other.VideoCodec != majority.VideoCodec {
		diffs = append(diffs, fmt.Sprintf("video codec: %s (expected %s)", other.VideoCodec, majority.VideoCodec))
	}
	if other.Width != majority.Width || other.Height != majority.Height {
		diffs = append(diffs, fmt.Sprintf("resolution: %dx%d (expected %dx%d)", other.Width, other.Height, majority.Width, majority.Height))
	}
	if len(other.AudioStreams) != len(majority.AudioStreams) {
		diffs = append(diffs, fmt.Sprintf("audio streams: %d (expected %d)", len(other.AudioStreams), len(majority.AudioStreams)))
	}
	if other.SubtitleCount != majority.SubtitleCount {
		diffs = append(diffs, fmt.Sprintf("subtitle streams: %d (expected %d)", other.SubtitleCount, majority.SubtitleCount))
	}
	return diffs
}

// analyzeCrop parses a crop filter string and computes aspect ratio.
func analyzeCrop(crop *encodingstate.Crop) *CropAnalysis {
	ca := &CropAnalysis{
		Required: crop.Required,
		Disabled: crop.Disabled,
	}

	filter := crop.Crop
	ca.Filter = filter

	w, h, ok := encodingstate.ParseCropFilter(filter)
	if ok {
		ca.OutputWidth = w
		ca.OutputHeight = h
		if h > 0 {
			ca.AspectRatio = math.Round(float64(w)/float64(h)*100) / 100
			ca.StandardRatio = encodingstate.MatchStandardRatio(ca.AspectRatio)
		}
	}

	return ca
}

// computeEpisodeStats summarizes confidence and coverage for episodes.
func computeEpisodeStats(episodes []ripspec.Episode) *EpisodeStats {
	if len(episodes) == 0 {
		return nil
	}

	stats := &EpisodeStats{
		Count: len(episodes),
	}

	var confidences []float64
	var episodeNumbers []int

	for _, ep := range episodes {
		if ep.Episode > 0 {
			stats.Matched++
			episodeNumbers = append(episodeNumbers, ep.Episode)
		} else {
			stats.Unresolved++
		}

		if ep.MatchConfidence > 0 {
			confidences = append(confidences, ep.MatchConfidence)
		}
	}

	// Confidence stats from non-zero values only.
	if len(confidences) > 0 {
		stats.ConfidenceMin = slices.Min(confidences)
		stats.ConfidenceMax = slices.Max(confidences)
		var sum float64
		for _, c := range confidences {
			sum += c
		}
		stats.ConfidenceMean = math.Round(sum/float64(len(confidences))*1000) / 1000

		// Cumulative threshold counts.
		for _, c := range confidences {
			if c < 0.90 {
				stats.Below090++
			}
			if c < 0.80 {
				stats.Below080++
			}
			if c < 0.70 {
				stats.Below070++
			}
		}
	}

	// Sequence contiguity check.
	if len(episodeNumbers) > 0 {
		sort.Ints(episodeNumbers)
		stats.SequenceContiguous = isContiguous(episodeNumbers)
		stats.EpisodeRange = fmt.Sprintf("%d-%d", episodeNumbers[0], episodeNumbers[len(episodeNumbers)-1])
	}

	return stats
}

func isContiguous(sorted []int) bool {
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1]+1 {
			return false
		}
	}
	return true
}

// computeMediaStats derives min/max of duration and size from probes.
func computeMediaStats(probes []MediaFileProbe) *MediaStats {
	var durations []float64
	var sizes []int64

	for _, p := range probes {
		if p.Error != "" {
			continue
		}
		if p.DurationSec > 0 {
			durations = append(durations, p.DurationSec)
		}
		if p.SizeBytes > 0 {
			sizes = append(sizes, p.SizeBytes)
		}
	}

	count := 0
	for _, p := range probes {
		if p.Error == "" {
			count++
		}
	}

	if count == 0 {
		return nil
	}

	ms := &MediaStats{FileCount: count}
	if len(durations) > 0 {
		ms.DurationMinSec = slices.Min(durations)
		ms.DurationMaxSec = slices.Max(durations)
	}
	if len(sizes) > 0 {
		ms.SizeMinBytes = slices.Min(sizes)
		ms.SizeMaxBytes = slices.Max(sizes)
	}
	return ms
}

// computeAssetHealth counts ok/failed/muxed assets per pipeline stage.
func computeAssetHealth(assets *ripspec.Assets) *AssetHealth {
	if assets == nil {
		return nil
	}

	ripped := countAssets(assets.Ripped, false)
	encoded := countAssets(assets.Encoded, false)
	subtitled := countAssets(assets.Subtitled, true)
	final := countAssets(assets.Final, false)

	if ripped == nil && encoded == nil && subtitled == nil && final == nil {
		return nil
	}

	return &AssetHealth{
		Ripped:    ripped,
		Encoded:   encoded,
		Subtitled: subtitled,
		Final:     final,
	}
}

func countAssets(list []ripspec.Asset, trackMuxed bool) *AssetCounts {
	if len(list) == 0 {
		return nil
	}
	ac := &AssetCounts{Total: len(list)}
	for _, a := range list {
		if a.IsFailed() {
			ac.Failed++
		} else {
			ac.OK++
		}
		if trackMuxed && a.SubtitlesMuxed {
			ac.Muxed++
		}
	}
	return ac
}

// detectAnomalies produces pre-computed red flags from the report and analysis.
func detectAnomalies(r *Report, a *Analysis) []Anomaly {
	var anomalies []Anomaly

	// Item error/review state.
	if r.Item.ErrorMessage != "" {
		anomalies = append(anomalies, Anomaly{
			Severity: "critical",
			Category: "item_state",
			Message:  fmt.Sprintf("item failed at %s: %s", r.Item.FailedAtStatus, r.Item.ErrorMessage),
		})
	}
	if r.Item.NeedsReview {
		anomalies = append(anomalies, Anomaly{
			Severity: "warning",
			Category: "item_state",
			Message:  fmt.Sprintf("item needs review: %s", r.Item.ReviewReason),
		})
	}

	// Log error/warning counts.
	if r.Logs != nil {
		if n := len(r.Logs.Errors); n > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "logs",
				Message:  fmt.Sprintf("%d error(s) in item log", n),
			})
		}
		if n := len(r.Logs.Warnings); n > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "logs",
				Message:  fmt.Sprintf("%d warning(s) in item log", n),
			})
		}
	}

	// Episode stats anomalies.
	if a.EpisodeStats != nil {
		if a.EpisodeStats.Unresolved > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "episodes",
				Message:  fmt.Sprintf("%d unresolved episode(s)", a.EpisodeStats.Unresolved),
			})
		}
		// Tiered confidence: report additional count per tier.
		below070 := a.EpisodeStats.Below070
		below080only := a.EpisodeStats.Below080 - a.EpisodeStats.Below070
		below090only := a.EpisodeStats.Below090 - a.EpisodeStats.Below080
		if below070 > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode(s) with confidence below 0.70", below070),
			})
		}
		if below080only > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode(s) with confidence below 0.80", below080only),
			})
		}
		if below090only > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "info",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode(s) with confidence below 0.90", below090only),
			})
		}
		if !a.EpisodeStats.SequenceContiguous && a.EpisodeStats.Matched > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "episodes",
				Message:  fmt.Sprintf("non-contiguous episode sequence: %s", a.EpisodeStats.EpisodeRange),
			})
		}
	}

	// Encoding anomalies.
	if r.Encoding != nil {
		snap := r.Encoding.Snapshot
		if snap.Validation != nil && !snap.Validation.Passed {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "encoding",
				Message:  "encoding validation failed",
			})
		}
		if snap.Error != nil {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "encoding",
				Message:  fmt.Sprintf("encoding error: %s", snap.Error.Message),
			})
		}
		if snap.Warning != "" {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "encoding",
				Message:  fmt.Sprintf("encoding warning: %s", snap.Warning),
			})
		}
	}

	// Failed assets at any stage.
	if a.AssetHealth != nil {
		checkAssetStage := func(name string, ac *AssetCounts) {
			if ac != nil && ac.Failed > 0 {
				anomalies = append(anomalies, Anomaly{
					Severity: "critical",
					Category: "assets",
					Message:  fmt.Sprintf("%d failed %s asset(s)", ac.Failed, name),
				})
			}
		}
		checkAssetStage("ripped", a.AssetHealth.Ripped)
		checkAssetStage("encoded", a.AssetHealth.Encoded)
		checkAssetStage("subtitled", a.AssetHealth.Subtitled)
		checkAssetStage("final", a.AssetHealth.Final)
	}

	// Cross-episode deviations.
	if a.EpisodeConsistency != nil && len(a.EpisodeConsistency.Deviations) > 0 {
		anomalies = append(anomalies, Anomaly{
			Severity: "warning",
			Category: "consistency",
			Message:  fmt.Sprintf("%d episode(s) deviate from majority media profile", len(a.EpisodeConsistency.Deviations)),
		})
	}

	// Media probe failures.
	probeErrors := 0
	for _, p := range r.Media {
		if p.Error != "" {
			probeErrors++
		}
	}
	if probeErrors > 0 {
		anomalies = append(anomalies, Anomaly{
			Severity: "warning",
			Category: "media",
			Message:  fmt.Sprintf("%d media probe(s) failed", probeErrors),
		})
	}

	// Crop disabled.
	if a.CropAnalysis != nil && a.CropAnalysis.Disabled {
		anomalies = append(anomalies, Anomaly{
			Severity: "info",
			Category: "encoding",
			Message:  "crop detection was disabled",
		})
	}

	if len(anomalies) == 0 {
		return nil
	}
	return anomalies
}
