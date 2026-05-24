package auditgather

import (
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/five82/spindle/internal/contentid"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

// computeAnalysis derives pre-computed summaries from the collected report data.
func normalizeAuditPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func pathWithinRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	path = normalizeAuditPath(path)
	root = normalizeAuditPath(root)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func detectTVRoutingAnomalies(r *Report) []Anomaly {
	if r == nil || r.Envelope == nil || r.Envelope.Metadata.MediaType != "tv" {
		return nil
	}
	finalByKey := make(map[string]ripspec.Asset)
	for _, asset := range r.Envelope.Assets.Final {
		if asset.EpisodeKey != "" {
			finalByKey[strings.ToLower(asset.EpisodeKey)] = asset
		}
	}

	var anomalies []Anomaly
	misroutedToReview := 0
	misroutedToLibrary := 0
	missingFinal := 0
	cleanEpisodes := 0
	for _, ep := range r.Envelope.Episodes {
		if ep.Key == "" {
			continue
		}
		expectedReview := ep.Episode <= 0 || ep.NeedsReview
		if !expectedReview {
			cleanEpisodes++
		}
		asset, ok := finalByKey[strings.ToLower(ep.Key)]
		if !ok || !asset.IsCompleted() || asset.Path == "" {
			missingFinal++
			continue
		}
		inReview := pathWithinRoot(asset.Path, r.Paths.ReviewDir)
		if expectedReview && !inReview {
			misroutedToLibrary++
		}
		if !expectedReview && inReview {
			misroutedToReview++
		}
	}
	if misroutedToReview > 0 {
		severity := "warning"
		if cleanEpisodes > 0 && misroutedToReview == cleanEpisodes {
			severity = "critical"
		}
		anomalies = append(anomalies, Anomaly{
			Severity: severity,
			Category: "organization",
			Message:  fmt.Sprintf("%d clean resolved episode(s) routed to review", misroutedToReview),
		})
	}
	if misroutedToLibrary > 0 {
		anomalies = append(anomalies, Anomaly{
			Severity: "critical",
			Category: "organization",
			Message:  fmt.Sprintf("%d review-required episode(s) routed to library", misroutedToLibrary),
		})
	}
	if missingFinal > 0 && len(finalByKey) > 0 {
		anomalies = append(anomalies, Anomaly{
			Severity: "warning",
			Category: "organization",
			Message:  fmt.Sprintf("%d episode(s) missing final routed asset", missingFinal),
		})
	}
	return anomalies
}

func detectDefaultAudioLanguageAnomalies(r *Report) []Anomaly {
	if r == nil {
		return nil
	}

	var anomalies []Anomaly
	for _, media := range r.Media {
		if media.Probe == nil {
			continue
		}
		audioStreams := media.Probe.AudioStreams()
		if len(audioStreams) < 2 {
			continue
		}

		hasEnglish := false
		defaultLang := ""
		for _, st := range audioStreams {
			langCode := language.ToISO2(language.ExtractFromTags(st.Tags))
			if strings.HasPrefix(langCode, "en") {
				hasEnglish = true
			}
			if st.Disposition["default"] == 1 && defaultLang == "" {
				defaultLang = langCode
			}
		}
		if !hasEnglish || defaultLang == "" || strings.HasPrefix(defaultLang, "en") {
			continue
		}

		target := media.Path
		if media.EpisodeKey != "" {
			target = media.EpisodeKey
		}
		anomalies = append(anomalies, Anomaly{
			Severity: "warning",
			Category: "media",
			Message:  fmt.Sprintf("non-English default audio language %q despite English audio present: %s", defaultLang, target),
		})
	}
	return anomalies
}

func computeAnalysis(r *Report) *Analysis {
	a := &Analysis{}

	if r.Logs != nil && len(r.Logs.Decisions) > 0 {
		a.DecisionGroups = aggregateDecisions(r.Logs.Decisions)
		a.NotableDecisions = selectNotableDecisions(r.Logs.Decisions)
	}
	if r.Logs != nil && len(r.Logs.Stages) > 0 {
		a.StageTimings = computeStageTimings(r.Logs.Stages)
	}

	a.SourceSummary = computeSourceSummary(r)
	a.TitleSelection = computeTitleSelection(r)
	a.OutputMedia = computeOutputMedia(r.Media)
	a.AudioSummary = computeAudioSummary(r)
	a.SubtitleSummary = computeSubtitleSummary(r)
	a.RoutingSummary = computeRoutingSummary(r)

	if len(r.Media) > 0 {
		a.EpisodeConsistency = computeEpisodeConsistency(r.Media)
		a.MediaStats = computeMediaStats(r.Media)
	}

	if r.Encoding != nil {
		snap := r.Encoding.Snapshot
		if snap.CropFilter != "" || snap.CropRequired {
			a.CropAnalysis = analyzeCrop(&snap)
		}
	}

	if r.Envelope != nil && len(r.Envelope.Episodes) > 0 {
		a.EpisodeStats = computeEpisodeStats(r.Envelope.Episodes)
	}

	if r.Envelope != nil {
		a.AssetHealth = computeAssetHealth(&r.Envelope.Assets)
	}

	a.Anomalies = detectAnomalies(r, a)
	a.Anomalies = append(a.Anomalies, detectFallbackTitleSelectionAnomalies(r)...)
	a.Anomalies = append(a.Anomalies, detectTVRoutingAnomalies(r)...)
	a.Anomalies = append(a.Anomalies, detectDefaultAudioLanguageAnomalies(r)...)

	// Return nil if everything is empty.
	if len(a.DecisionGroups) == 0 &&
		len(a.NotableDecisions) == 0 &&
		len(a.StageTimings) == 0 &&
		a.SourceSummary == nil &&
		a.TitleSelection == nil &&
		len(a.OutputMedia) == 0 &&
		a.AudioSummary == nil &&
		a.SubtitleSummary == nil &&
		a.RoutingSummary == nil &&
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
			g.Entries = nil
		}
		result = append(result, g)
	}
	return result
}

func messagesVary(entries []LogDecision) bool {
	if len(entries) <= 1 {
		return false
	}
	firstMsg := entries[0].Message
	firstExtras := maps.Clone(entries[0].Extras)
	for _, e := range entries[1:] {
		if e.Message != firstMsg {
			return true
		}
		if !reflect.DeepEqual(e.Extras, firstExtras) {
			return true
		}
	}
	return false
}

func selectNotableDecisions(decisions []LogDecision) []LogDecision {
	notable := map[string]bool{
		logs.DecisionTMDBMatch:                true,
		logs.DecisionTMDBMatchPreference:      true,
		logs.DecisionTitleResolution:          true,
		logs.DecisionTitleSelection:           true,
		logs.DecisionFileProbe:                true,
		logs.DecisionCropDetection:            true,
		logs.DecisionEncodingValidation:       true,
		logs.DecisionValidationFailureRoute:   true,
		logs.DecisionAudioSelection:           true,
		logs.DecisionAudioRefinement:          true,
		logs.DecisionCommentaryStereoFilter:   true,
		logs.DecisionCommentaryClassification: true,
		logs.DecisionCommentaryRemapping:      true,
		logs.DecisionCommentaryDisposition:    true,
		logs.DecisionSubtitleFormatting:       true,
		logs.DecisionSRTValidation:            true,
		logs.DecisionSourceStageSelection:     true,
		logs.DecisionEpisodeIDSkip:            true,
		logs.DecisionEpisodePlaceholders:      true,
		logs.DecisionEpisodeMatch:             true,
		logs.DecisionContentIDCandidates:      true,
		logs.DecisionContentIDMatches:         true,
		logs.DecisionReferenceSearch:          true,
		logs.DecisionTranscriptionAsset:       true,
		logs.DecisionAssetMapping:             true,
		logs.DecisionAudioRemux:               true,
	}
	out := make([]LogDecision, 0, len(decisions))
	for _, d := range decisions {
		if notable[d.DecisionType] {
			out = append(out, d)
		}
	}
	return out
}

func computeStageTimings(events []StageEvent) []StageTiming {
	byStage := make(map[string]*StageTiming)
	var order []string
	for _, e := range events {
		if e.Stage == "" {
			continue
		}
		st := byStage[e.Stage]
		if st == nil {
			st = &StageTiming{Stage: e.Stage}
			byStage[e.Stage] = st
			order = append(order, e.Stage)
		}
		switch e.EventType {
		case "stage_start":
			st.Starts++
			if st.StartedAt == "" {
				st.StartedAt = e.TS
			}
		case "stage_complete":
			st.Completions++
			st.CompletedAt = e.TS
			if e.DurationSeconds > 0 {
				st.DurationSeconds = e.DurationSeconds
			}
		}
	}
	result := make([]StageTiming, 0, len(order))
	for _, stage := range order {
		result = append(result, *byStage[stage])
	}
	return result
}

func computeSourceSummary(r *Report) *SourceSummary {
	if r == nil {
		return nil
	}
	ss := &SourceSummary{DiscSource: r.StageGate.DiscSource}
	if r.Encoding != nil {
		snap := r.Encoding.Snapshot
		ss.OutputResolution = strings.TrimSpace(snap.Resolution)
		ss.DynamicRange = strings.TrimSpace(snap.DynamicRange)
		ss.HDR = strings.EqualFold(ss.DynamicRange, "HDR")
	}
	if len(r.Media) > 0 && r.Media[0].Probe != nil {
		for _, s := range r.Media[0].Probe.Streams {
			if s.CodecType != "video" {
				continue
			}
			ss.OutputCodec = s.CodecName
			if ss.OutputResolution == "" && s.Width > 0 && s.Height > 0 {
				ss.OutputResolution = fmt.Sprintf("%dx%d", s.Width, s.Height)
			}
			if mediaStreamHDR(s) {
				ss.HDR = true
			}
			break
		}
	}
	if r.Logs != nil {
		for _, d := range r.Logs.Decisions {
			if d.DecisionType != "file_probe" {
				continue
			}
			if ss.InputResolution == "" {
				if v, ok := d.Extras["resolution"].(string); ok {
					ss.InputResolution = v
				} else if v := decisionReasonValue(d.DecisionReason, "resolution"); v != "" {
					ss.InputResolution = v
				}
			}
			if codecs, ok := d.Extras["codecs"].(string); ok && codecs != "" {
				ss.InputCodecs = strings.Split(codecs, ",")
			} else if codecs := decisionReasonValue(d.DecisionReason, "codecs"); codecs != "" {
				ss.InputCodecs = strings.Split(codecs, ",")
			}
		}
	}
	ss.UHDLikely = strings.Contains(strings.ToUpper(r.Item.DiscTitle), "UHD") || strings.HasPrefix(ss.InputResolution, "3840x2160") || strings.HasPrefix(ss.OutputResolution, "3840x")
	if ss.DiscSource == "" && ss.InputResolution == "" && ss.OutputResolution == "" && len(ss.InputCodecs) == 0 && ss.OutputCodec == "" && ss.DynamicRange == "" && !ss.HDR && !ss.UHDLikely {
		return nil
	}
	return ss
}

func decisionReasonValue(reason, key string) string {
	prefix := key + "="
	for _, field := range strings.Fields(reason) {
		if strings.HasPrefix(field, prefix) {
			return strings.Trim(strings.TrimPrefix(field, prefix), ",")
		}
	}
	return ""
}

func computeTitleSelection(r *Report) *TitleSelectionSummary {
	if r == nil || r.Envelope == nil || r.Envelope.Metadata.MediaType != "movie" || len(r.Envelope.Titles) == 0 {
		return nil
	}
	ts := &TitleSelectionSummary{SelectedID: -1}
	if r.Logs != nil {
		for _, d := range r.Logs.Decisions {
			if d.DecisionType == "title_selection" {
				ts.DecisionResult = d.DecisionResult
				ts.DecisionReason = d.DecisionReason
				if id, ok := parseSelectedTitleID(d.DecisionResult); ok {
					ts.SelectedID = id
				}
				break
			}
		}
	}
	if ts.SelectedID < 0 {
		for _, a := range r.Envelope.Assets.Ripped {
			if a.IsCompleted() {
				ts.SelectedID = a.TitleID
				break
			}
		}
	}
	for _, title := range r.Envelope.Titles {
		if title.Duration <= 3600 || title.Chapters <= 1 {
			continue
		}
		candidate := TitleCandidate{
			ID:              title.ID,
			DurationSeconds: title.Duration,
			Chapters:        title.Chapters,
			Playlist:        title.Playlist,
			SegmentCount:    title.SegmentCount,
			Selected:        title.ID == ts.SelectedID,
		}
		if candidate.Selected {
			ts.SelectedDurationSeconds = title.Duration
		}
		ts.Candidates = append(ts.Candidates, candidate)
	}
	ts.FeatureCandidateCount = len(ts.Candidates)
	if ts.SelectedDurationSeconds > 0 {
		for _, c := range ts.Candidates {
			if math.Abs(float64(c.DurationSeconds-ts.SelectedDurationSeconds)) <= 30 {
				ts.SimilarRuntimeCount++
			}
		}
	}
	return ts
}

func parseSelectedTitleID(result string) (int, bool) {
	fields := strings.Fields(result)
	for i, f := range fields {
		if strings.EqualFold(f, "title") && i+1 < len(fields) {
			idText := strings.Trim(fields[i+1], "():")
			id, err := strconv.Atoi(idText)
			return id, err == nil
		}
	}
	return 0, false
}

func computeOutputMedia(probes []MediaFileProbe) []MediaSummary {
	out := make([]MediaSummary, 0, len(probes))
	for _, p := range probes {
		if p.Probe == nil || p.Error != "" {
			continue
		}
		ms := MediaSummary{
			Path:            p.Path,
			Role:            p.Role,
			EpisodeKey:      p.EpisodeKey,
			DurationSeconds: p.DurationSeconds,
			SizeBytes:       p.SizeBytes,
		}
		for _, s := range p.Probe.Streams {
			switch s.CodecType {
			case "video":
				if ms.Video == nil {
					ms.Video = &VideoSummary{Codec: s.CodecName, Width: s.Width, Height: s.Height, HDR: mediaStreamHDR(s), ColorTransfer: s.ColorTransfer, ColorPrimaries: s.ColorPrimaries}
				}
			case "audio":
				commentary := s.Disposition["comment"] == 1 || strings.Contains(strings.ToLower(s.Tags["title"]), "commentary")
				ms.Audio = append(ms.Audio, AudioStreamSummary{
					Index:        s.Index,
					Codec:        s.CodecName,
					Channels:     s.Channels,
					Layout:       s.ChannelLayout,
					Language:     s.Tags["language"],
					Title:        s.Tags["title"],
					Default:      s.Disposition["default"] == 1,
					Commentary:   commentary,
					LabelCorrect: !commentary || strings.Contains(strings.ToLower(s.Tags["title"]), "commentary"),
				})
			case "subtitle":
				forced := s.Disposition["forced"] == 1
				ms.Subtitles = append(ms.Subtitles, SubtitleStreamSummary{
					Index:        s.Index,
					Codec:        s.CodecName,
					Language:     s.Tags["language"],
					Title:        s.Tags["title"],
					Default:      s.Disposition["default"] == 1,
					Forced:       forced,
					LabelCorrect: subtitleLabelCorrect(s.Tags["language"], s.Tags["title"], forced),
				})
			}
		}
		out = append(out, ms)
	}
	return out
}

func mediaStreamHDR(s ffprobe.Stream) bool {
	transfer := strings.ToLower(s.ColorTransfer)
	primaries := strings.ToLower(s.ColorPrimaries)
	if strings.Contains(transfer, "smpte2084") || strings.Contains(transfer, "arib-std-b67") || strings.Contains(primaries, "bt2020") {
		return true
	}
	for _, sideData := range s.SideDataList {
		kind := strings.ToLower(sideData.Type)
		if strings.Contains(kind, "mastering display") || strings.Contains(kind, "content light") {
			return true
		}
	}
	return false
}

func subtitleLabelCorrect(lang, title string, forced bool) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	if forced && !strings.Contains(strings.ToLower(title), "forced") {
		return false
	}
	if lang == "" {
		return true
	}
	display := strings.ToLower(language.DisplayName(lang))
	return display == "" || strings.Contains(strings.ToLower(title), display) || strings.Contains(strings.ToLower(title), strings.ToLower(lang))
}

func computeAudioSummary(r *Report) *AudioSummary {
	if r == nil || r.Envelope == nil {
		return nil
	}
	summary := &AudioSummary{CommentaryLabelsCorrect: true}
	if aa := r.Envelope.Attributes.AudioAnalysis; aa != nil {
		summary.PrimaryDescription = aa.PrimaryDescription
		summary.PrimaryTrackIndex = aa.PrimaryTrack.Index
		for _, tr := range aa.ExcludedTracks {
			summary.ExcludedTracks = append(summary.ExcludedTracks, ExcludedTrack{Index: tr.Index, Reason: tr.Reason, Similarity: tr.Similarity})
		}
	}
	for _, media := range computeOutputMedia(r.Media) {
		for _, audio := range media.Audio {
			summary.OutputAudioTracks++
			if audio.Commentary {
				summary.OutputCommentaryTracks++
				if !audio.LabelCorrect {
					summary.CommentaryLabelsCorrect = false
				}
			}
		}
	}
	if r.Logs != nil {
		for _, d := range r.Logs.Decisions {
			switch d.DecisionType {
			case "commentary_classification", "commentary_stereo_filter", "commentary_remapping", "commentary_disposition":
				summary.CommentaryDecisions = append(summary.CommentaryDecisions, d)
			}
		}
	}
	if summary.PrimaryDescription == "" && summary.OutputAudioTracks == 0 && len(summary.ExcludedTracks) == 0 && len(summary.CommentaryDecisions) == 0 {
		return nil
	}
	return summary
}

func computeSubtitleSummary(r *Report) *SubtitleSummary {
	if r == nil || r.Envelope == nil {
		return nil
	}
	summary := &SubtitleSummary{SubtitleLabelsCorrect: true}
	for _, rec := range r.Envelope.Attributes.SubtitleGenerationResults {
		summary.Results = append(summary.Results, SubtitleResultSummary{
			EpisodeKey:       rec.EpisodeKey,
			Source:           rec.Source,
			Language:         rec.Language,
			Segments:         rec.Segments,
			ValidationResult: rec.ValidationResult,
			ReviewIssues:     rec.ReviewIssues,
			SevereIssues:     rec.SevereIssues,
			QCObservations:   rec.QCObservations,
		})
		switch rec.ValidationResult {
		case "passed":
			summary.ValidationPassed++
		case "needs_review":
			summary.ValidationNeedsReview++
		case "failed":
			summary.ValidationFailed++
		}
	}
	for _, media := range computeOutputMedia(r.Media) {
		for _, sub := range media.Subtitles {
			summary.OutputSubtitleTracks++
			if !sub.LabelCorrect {
				summary.SubtitleLabelsCorrect = false
			}
		}
	}
	if len(summary.Results) == 0 && summary.OutputSubtitleTracks == 0 {
		return nil
	}
	return summary
}

func computeRoutingSummary(r *Report) *RoutingSummary {
	if r == nil || r.Envelope == nil || len(r.Envelope.Assets.Final) == 0 {
		return nil
	}
	summary := &RoutingSummary{}
	for _, asset := range r.Envelope.Assets.Final {
		if !asset.IsCompleted() || asset.Path == "" {
			continue
		}
		destination := "other"
		if pathWithinRoot(asset.Path, r.Paths.ReviewDir) {
			destination = "review"
		} else if pathWithinRoot(asset.Path, r.Paths.LibraryDir) {
			destination = "library"
		}
		expectedReview := false
		if r.Envelope.Metadata.MediaType == "tv" {
			if ep := r.Envelope.EpisodeByKey(asset.EpisodeKey); ep != nil {
				expectedReview = ep.Episode <= 0 || ep.NeedsReview
			}
		} else {
			expectedReview = r.Item.NeedsReview
		}
		matches := (expectedReview && destination == "review") || (!expectedReview && destination == "library")
		summary.Entries = append(summary.Entries, RoutingEntry{
			EpisodeKey:      asset.EpisodeKey,
			Path:            asset.Path,
			Destination:     destination,
			ExpectedReview:  expectedReview,
			MatchesExpected: matches,
		})
	}
	if len(summary.Entries) == 0 {
		return nil
	}
	return summary
}

func detectFallbackTitleSelectionAnomalies(r *Report) []Anomaly {
	if r == nil || r.Logs == nil || r.Envelope == nil {
		return nil
	}

	tvHinted := false
	noTMDBMatch := false
	broadFallbackSelection := false
	for _, d := range r.Logs.Decisions {
		switch {
		case d.DecisionType == "tmdb_search" && d.DecisionResult == "tv":
			tvHinted = true
		case d.DecisionType == "title_selection" && d.DecisionReason == "unknown media type, using duration filter":
			broadFallbackSelection = true
		}
	}
	for _, w := range r.Logs.Warnings {
		if w.EventType == "tmdb_no_match" {
			noTMDBMatch = true
			break
		}
	}
	if !tvHinted || !noTMDBMatch || !broadFallbackSelection {
		return nil
	}

	totalTitles := len(r.Envelope.Titles)
	shortLike := 0
	for _, ep := range r.Envelope.Episodes {
		if ep.RuntimeSeconds > 0 && ep.RuntimeSeconds < 600 {
			shortLike++
		}
	}
	severity := "warning"
	if totalTitles >= 8 || shortLike >= 3 {
		severity = "critical"
	}
	message := fmt.Sprintf("TV-hinted disc with no TMDB match fell back to broad title selection (%d titles", totalTitles)
	if shortLike > 0 {
		message += fmt.Sprintf(", %d short/extras-like", shortLike)
	}
	message += ")"
	return []Anomaly{{
		Severity: severity,
		Category: "title_selection",
		Message:  message,
	}}
}

func countDecisionConfidenceQualities(decisions []LogDecision) (contested, ambiguous, decisiveLowSimilarity int) {
	for _, d := range decisions {
		if d.DecisionType != "episode_match" || d.Extras == nil {
			continue
		}
		switch episodeMatchConfidenceQuality(d) {
		case contentid.ConfidenceQualityContested:
			contested++
		case contentid.ConfidenceQualityAmbiguous:
			ambiguous++
		case contentid.ConfidenceQualityDecisiveLowSimilarity:
			decisiveLowSimilarity++
		}
	}
	return contested, ambiguous, decisiveLowSimilarity
}

func episodeMatchConfidenceQuality(d LogDecision) string {
	quality, _ := d.Extras["confidence_quality"].(string)
	if quality != contentid.ConfidenceQualityAmbiguous && quality != "" {
		return quality
	}
	confidence, ok := d.Extras["match_confidence"].(float64)
	if !ok {
		return quality
	}
	ripMargin, ok := d.Extras["rip_score_margin"].(float64)
	if !ok {
		return quality
	}
	episodeMargin, ok := d.Extras["episode_score_margin"].(float64)
	if !ok {
		return quality
	}
	neighborMargin, ok := d.Extras["neighbor_score_margin"].(float64)
	if !ok {
		return quality
	}
	referenceSuspect, _ := d.Extras["reference_suspect"].(bool)
	derived := contentid.ClassifyConfidenceQuality(confidence, ripMargin, episodeMargin, neighborMargin, referenceSuspect, contentid.DefaultPolicy())
	if derived != contentid.ConfidenceQualityDecisiveLowSimilarity {
		return quality
	}
	return derived
}

func countDecisiveLowSimilarityInConfidenceBand(decisions []LogDecision, minConfidence, maxConfidence float64) int {
	count := 0
	for _, d := range decisions {
		if d.DecisionType != "episode_match" || d.Extras == nil || episodeMatchConfidenceQuality(d) != contentid.ConfidenceQualityDecisiveLowSimilarity {
			continue
		}
		confidence, ok := d.Extras["match_confidence"].(float64)
		if !ok {
			continue
		}
		if confidence >= minConfidence && confidence < maxConfidence {
			count++
		}
	}
	return count
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

	// Find majority profile by counting matches.
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

	best := 0
	for i, c := range counts {
		if c.count > counts[best].count {
			best = i
		}
	}
	majority := counts[best].summary
	majorityCount := counts[best].count

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
	if p.Probe == nil {
		return ps
	}
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
			if strings.Contains(strings.ToLower(s.Tags["title"]), "commentary") {
				ap.IsCommentary = true
			}
			ps.AudioStreams = append(ps.AudioStreams, ap)
		case "subtitle":
			ps.SubtitleStreams = append(ps.SubtitleStreams, SubtitleProfile{
				Codec:    s.CodecName,
				Language: s.Tags["language"],
				IsForced: s.Disposition["forced"] == 1,
			})
		}
	}
	return ps
}

func profilesEqual(a, b ProfileSummary) bool {
	return a.VideoCodec == b.VideoCodec &&
		a.Width == b.Width &&
		a.Height == b.Height &&
		slices.Equal(a.AudioStreams, b.AudioStreams) &&
		slices.Equal(a.SubtitleStreams, b.SubtitleStreams)
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
	if !slices.Equal(majority.SubtitleStreams, other.SubtitleStreams) {
		diffs = append(diffs, fmt.Sprintf("subtitle streams: %s (expected %s)",
			describeSubtitleStreams(other.SubtitleStreams),
			describeSubtitleStreams(majority.SubtitleStreams)))
	}
	return diffs
}

func describeSubtitleStreams(streams []SubtitleProfile) string {
	if len(streams) == 0 {
		return "none"
	}
	var parts []string
	for _, s := range streams {
		desc := s.Codec
		if s.Language != "" {
			desc += " " + s.Language
		}
		if s.IsForced {
			desc += " (forced)"
		}
		parts = append(parts, desc)
	}
	return fmt.Sprintf("%d [%s]", len(streams), strings.Join(parts, ", "))
}

// compressMediaProbes removes redundant TV episode probes that match the
// majority profile. Keeps the first match as representative, all deviations,
// all error probes, and all movie probes (no episode key).
func compressMediaProbes(probes []MediaFileProbe, consistency *EpisodeConsistency) ([]MediaFileProbe, int) {
	if consistency == nil || len(probes) == 0 {
		return probes, 0
	}

	majority := consistency.MajorityProfile

	deviationKeys := make(map[string]bool, len(consistency.Deviations))
	for _, d := range consistency.Deviations {
		deviationKeys[d.EpisodeKey] = true
	}

	var result []MediaFileProbe
	omitted := 0
	representativeFound := false

	for _, p := range probes {
		if p.Error != "" || p.EpisodeKey == "" || deviationKeys[p.EpisodeKey] {
			result = append(result, p)
			continue
		}

		profile := buildProfileSummary(p)
		if !profilesEqual(majority, profile) {
			result = append(result, p)
			continue
		}

		if !representativeFound {
			p.Representative = true
			result = append(result, p)
			representativeFound = true
		} else {
			omitted++
		}
	}

	return result, omitted
}

// analyzeCrop builds a CropAnalysis from the encoding snapshot's flat crop fields.
func analyzeCrop(snap *encodingstate.Snapshot) *CropAnalysis {
	ca := &CropAnalysis{
		Filter:   snap.CropFilter,
		Required: snap.CropRequired,
	}

	w, h, err := encodingstate.ParseCropFilter(snap.CropFilter)
	if err == nil {
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

	stats := &EpisodeStats{Count: len(episodes), PlaceholderOnly: true}
	var confidences []float64
	var episodeNumbers []int

	for _, ep := range episodes {
		if ep.Episode > 0 {
			stats.Matched++
			episodeNumbers = append(episodeNumbers, ep.Episode)
			if ep.EpisodeLast() > ep.Episode {
				episodeNumbers = append(episodeNumbers, ep.EpisodeLast())
			}
			stats.PlaceholderOnly = false
		} else {
			stats.Unresolved++
		}
		if ep.MatchConfidence > 0 || ep.NeedsReview || ep.ReviewReason != "" {
			stats.PlaceholderOnly = false
		}
		if ep.MatchConfidence > 0 {
			confidences = append(confidences, ep.MatchConfidence)
		}
	}

	if len(confidences) > 0 {
		stats.ConfidenceMin = slices.Min(confidences)
		stats.ConfidenceMax = slices.Max(confidences)
		var sum float64
		for _, c := range confidences {
			sum += c
		}
		stats.ConfidenceMean = math.Round(sum/float64(len(confidences))*1000) / 1000

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
	count := 0

	for _, p := range probes {
		if p.Error != "" {
			continue
		}
		count++
		if p.DurationSeconds > 0 {
			durations = append(durations, p.DurationSeconds)
		}
		if p.SizeBytes > 0 {
			sizes = append(sizes, p.SizeBytes)
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
			Message:  fmt.Sprintf("item failed at %s: %s", r.Item.FailedAtStage, r.Item.ErrorMessage),
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
		contested, ambiguous, decisiveLowSimilarity := countDecisionConfidenceQualities(r.Logs.Decisions)
		if contested > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode match decision(s) marked contested", contested),
			})
		}
		if ambiguous > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "info",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode match decision(s) marked ambiguous", ambiguous),
			})
		}
		if decisiveLowSimilarity > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "info",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode match decision(s) had decisive margins but lower transcript similarity", decisiveLowSimilarity),
			})
		}
	}

	if r.Envelope != nil {
		reviewCount := 0
		for _, ep := range r.Envelope.Episodes {
			if ep.NeedsReview {
				reviewCount++
			}
		}
		if reviewCount > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode(s) explicitly flagged for review", reviewCount),
			})
		}
		if r.StageGate.PhaseEpisodeID && r.Envelope.Metadata.MediaType == "tv" {
			summary := r.Envelope.Attributes.ContentID
			switch {
			case summary == nil:
				anomalies = append(anomalies, Anomaly{
					Severity: "warning",
					Category: "episodes",
					Message:  "episode identification provenance summary missing from envelope attributes",
				})
			case summary.Method == "" || summary.ReferenceSource == "":
				anomalies = append(anomalies, Anomaly{
					Severity: "warning",
					Category: "episodes",
					Message:  "episode identification provenance summary is incomplete",
				})
			case !summary.Completed && summary.EpisodesSynchronized:
				anomalies = append(anomalies, Anomaly{
					Severity: "warning",
					Category: "episodes",
					Message:  "episode identification provenance summary has inconsistent completion state",
				})
			}
		}
	}

	// Episode stats anomalies.
	if a.EpisodeStats != nil {
		if a.EpisodeStats.PlaceholderOnly && !r.StageGate.PhaseEpisodeID {
			anomalies = append(anomalies, Anomaly{
				Severity: "info",
				Category: "episodes",
				Message:  fmt.Sprintf("%d placeholder episode(s) in pre-episode-identification manifest", a.EpisodeStats.Unresolved),
			})
		} else if a.EpisodeStats.Unresolved > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "episodes",
				Message:  fmt.Sprintf("%d unresolved episode(s)", a.EpisodeStats.Unresolved),
			})
		}
		if a.EpisodeStats.Below070 > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "critical",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode(s) with confidence below 0.70", a.EpisodeStats.Below070),
			})
		}
		below080only := a.EpisodeStats.Below080 - a.EpisodeStats.Below070
		if below080only > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "episodes",
				Message:  fmt.Sprintf("%d episode(s) with confidence below 0.80", below080only),
			})
		}
		below090only := a.EpisodeStats.Below090 - a.EpisodeStats.Below080
		if below090only > 0 {
			message := fmt.Sprintf("%d episode(s) with confidence below 0.90", below090only)
			if r.Logs != nil && countDecisiveLowSimilarityInConfidenceBand(r.Logs.Decisions, 0.80, 0.90) == below090only {
				message = fmt.Sprintf("%d decisive episode match(es) below 0.90 with strong margins", below090only)
			}
			anomalies = append(anomalies, Anomaly{
				Severity: "info",
				Category: "episodes",
				Message:  message,
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
		checkStage := func(name string, ac *AssetCounts) {
			if ac != nil && ac.Failed > 0 {
				anomalies = append(anomalies, Anomaly{
					Severity: "critical",
					Category: "assets",
					Message:  fmt.Sprintf("%d failed %s asset(s)", ac.Failed, name),
				})
			}
		}
		checkStage("ripped", a.AssetHealth.Ripped)
		checkStage("encoded", a.AssetHealth.Encoded)
		checkStage("subtitled", a.AssetHealth.Subtitled)
		checkStage("final", a.AssetHealth.Final)
	}

	// Subtitle output layout anomalies.
	if r.StageGate.PhaseSubtitles {
		extraSubtitleFiles := 0
		forcedSubtitleFiles := 0
		for _, media := range a.OutputMedia {
			if len(media.Subtitles) > 1 {
				extraSubtitleFiles++
			}
			for _, sub := range media.Subtitles {
				if sub.Forced {
					forcedSubtitleFiles++
					break
				}
			}
		}
		if extraSubtitleFiles > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "subtitles",
				Message:  fmt.Sprintf("%d output file(s) have more than one subtitle stream", extraSubtitleFiles),
			})
		}
		if forcedSubtitleFiles > 0 {
			anomalies = append(anomalies, Anomaly{
				Severity: "warning",
				Category: "subtitles",
				Message:  fmt.Sprintf("%d output file(s) have a forced subtitle stream", forcedSubtitleFiles),
			})
		}
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

	if len(anomalies) == 0 {
		return nil
	}
	return anomalies
}
