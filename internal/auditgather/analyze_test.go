package auditgather

import (
	"encoding/json"
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
)

func TestAggregateDecisions_IdenticalCollapse(t *testing.T) {
	decisions := []LogDecision{
		{DecisionType: "track_select", DecisionResult: "accepted", DecisionReason: "best match", Message: "selected track 1"},
		{DecisionType: "track_select", DecisionResult: "accepted", DecisionReason: "best match", Message: "selected track 1"},
		{DecisionType: "track_select", DecisionResult: "accepted", DecisionReason: "best match", Message: "selected track 1"},
	}

	groups := aggregateDecisions(decisions)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Count != 3 {
		t.Errorf("expected count 3, got %d", groups[0].Count)
	}
	if groups[0].Entries != nil {
		t.Error("expected nil entries for identical messages")
	}
}

func TestAggregateDecisions_VaryingMessages(t *testing.T) {
	decisions := []LogDecision{
		{DecisionType: "commentary", DecisionResult: "detected", Message: "track 2 is commentary"},
		{DecisionType: "commentary", DecisionResult: "detected", Message: "track 4 is commentary"},
	}

	groups := aggregateDecisions(decisions)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Count != 2 {
		t.Errorf("expected count 2, got %d", groups[0].Count)
	}
	if groups[0].Entries == nil {
		t.Error("expected entries preserved for varying messages")
	}
	if len(groups[0].Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(groups[0].Entries))
	}
}

func TestAggregateDecisions_MultipleGroups(t *testing.T) {
	decisions := []LogDecision{
		{DecisionType: "disc_enqueue", DecisionResult: "created"},
		{DecisionType: "rip_cache", DecisionResult: "hit"},
		{DecisionType: "disc_enqueue", DecisionResult: "created"},
	}

	groups := aggregateDecisions(decisions)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	// Verify insertion order preserved.
	if groups[0].DecisionType != "disc_enqueue" {
		t.Errorf("expected first group disc_enqueue, got %s", groups[0].DecisionType)
	}
	if groups[1].DecisionType != "rip_cache" {
		t.Errorf("expected second group rip_cache, got %s", groups[1].DecisionType)
	}
}

func TestDecisionGroupJSONUsesDecisionFieldNames(t *testing.T) {
	groups := aggregateDecisions([]LogDecision{{
		DecisionType:   "tmdb_search",
		DecisionResult: "fallback_multi",
		DecisionReason: "no tv match above threshold",
		Message:        "TV-hinted search found no match, falling back to multi",
	}})
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	blob, err := json.Marshal(groups[0])
	if err != nil {
		t.Fatalf("marshal decision group: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal decision group: %v", err)
	}

	if got["decision_type"] != "tmdb_search" {
		t.Fatalf("decision_type = %v, want tmdb_search", got["decision_type"])
	}
	if got["decision_result"] != "fallback_multi" {
		t.Fatalf("decision_result = %v, want fallback_multi", got["decision_result"])
	}
	if got["decision_reason"] != "no tv match above threshold" {
		t.Fatalf("decision_reason = %v, want no tv match above threshold", got["decision_reason"])
	}
	if _, ok := got["type"]; ok {
		t.Fatalf("unexpected shorthand field 'type' present in json: %s", string(blob))
	}
}

func TestComputeStageTimingsCollapsesDuplicateCompletes(t *testing.T) {
	timings := computeStageTimings([]StageEvent{
		{TS: "t1", EventType: "stage_start", Stage: "encoding"},
		{TS: "t2", EventType: "stage_complete", Stage: "encoding"},
		{TS: "t3", EventType: "stage_complete", Stage: "encoding", DurationSeconds: 12.5},
	})
	if len(timings) != 1 {
		t.Fatalf("timing count = %d, want 1", len(timings))
	}
	if timings[0].Starts != 1 || timings[0].Completions != 2 {
		t.Fatalf("counts = starts:%d completions:%d, want 1/2", timings[0].Starts, timings[0].Completions)
	}
	if timings[0].DurationSeconds != 12.5 {
		t.Fatalf("duration = %f, want 12.5", timings[0].DurationSeconds)
	}
	if timings[0].StartedAt != "t1" || timings[0].CompletedAt != "t3" {
		t.Fatalf("times = %q/%q, want t1/t3", timings[0].StartedAt, timings[0].CompletedAt)
	}
}

func TestComputeTitleSelectionSummarizesFeatureCandidates(t *testing.T) {
	r := &Report{
		Logs: &LogAnalysis{Decisions: []LogDecision{{DecisionType: "title_selection", DecisionResult: "title 0 (5750s)", DecisionReason: "primary_title_selector"}}},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "movie"},
			Titles: []ripspec.Title{
				{ID: 0, Duration: 5750, Chapters: 10, Playlist: "00800.mpls"},
				{ID: 1, Duration: 5750, Chapters: 10, Playlist: "00801.mpls"},
				{ID: 2, Duration: 340, Chapters: 34},
			},
		},
	}

	summary := computeTitleSelection(r)
	if summary == nil {
		t.Fatal("expected title selection summary")
	}
	if summary.SelectedID != 0 || summary.SelectedDurationSeconds != 5750 {
		t.Fatalf("selected = %d/%d, want 0/5750", summary.SelectedID, summary.SelectedDurationSeconds)
	}
	if summary.FeatureCandidateCount != 2 || summary.SimilarRuntimeCount != 2 {
		t.Fatalf("candidate counts = %d/%d, want 2/2", summary.FeatureCandidateCount, summary.SimilarRuntimeCount)
	}
	if len(summary.Candidates) != 2 || !summary.Candidates[0].Selected || summary.Candidates[1].Selected {
		t.Fatalf("unexpected candidates: %+v", summary.Candidates)
	}
}

func TestComputeOutputMediaSummarizesStreamsAndLabels(t *testing.T) {
	media := []MediaFileProbe{{
		Path:            "/movie.mkv",
		Role:            "final",
		DurationSeconds: 10,
		SizeBytes:       100,
		Probe: &ffprobe.Result{Streams: []ffprobe.Stream{
			{CodecType: "video", CodecName: "av1", Width: 3840, Height: 1600, ColorTransfer: "smpte2084"},
			{Index: 1, CodecType: "audio", CodecName: "opus", Channels: 2, Tags: map[string]string{"language": "eng", "title": "Director"}, Disposition: map[string]int{"comment": 1}},
			{Index: 2, CodecType: "subtitle", CodecName: "subrip", Tags: map[string]string{"language": "eng", "title": "English"}},
		}},
	}}

	summary := computeOutputMedia(media)
	if len(summary) != 1 {
		t.Fatalf("media summaries = %d, want 1", len(summary))
	}
	if summary[0].Video == nil || !summary[0].Video.HDR {
		t.Fatalf("expected HDR video summary: %+v", summary[0].Video)
	}
	if len(summary[0].Audio) != 1 || summary[0].Audio[0].LabelCorrect {
		t.Fatalf("expected unlabeled commentary audio: %+v", summary[0].Audio)
	}
	if len(summary[0].Subtitles) != 1 || !summary[0].Subtitles[0].LabelCorrect {
		t.Fatalf("expected correctly labeled subtitle: %+v", summary[0].Subtitles)
	}
}

func TestComputeSubtitleSummarySeparatesValidationResults(t *testing.T) {
	r := &Report{
		Envelope: &ripspec.Envelope{Attributes: ripspec.EnvelopeAttributes{SubtitleGenerationResults: []ripspec.SubtitleGenRecord{
			{EpisodeKey: "main", Source: "whisperx", Language: "en", Segments: 10, ValidationResult: "passed", QCObservations: []string{"high_reading_speed"}},
			{EpisodeKey: "s01e02", Source: "whisperx", Language: "en", Segments: 8, ValidationResult: "needs_review", ReviewIssues: []string{"bad timing"}},
		}}},
	}

	summary := computeSubtitleSummary(r, computeOutputMedia(r.Media))
	if summary == nil {
		t.Fatal("expected subtitle summary")
	}
	if summary.ValidationPassed != 1 || summary.ValidationNeedsReview != 1 || summary.ValidationFailed != 0 {
		t.Fatalf("validation counts = passed:%d review:%d failed:%d", summary.ValidationPassed, summary.ValidationNeedsReview, summary.ValidationFailed)
	}
	if len(summary.Results) != 2 || len(summary.Results[1].ReviewIssues) != 1 {
		t.Fatalf("unexpected results: %+v", summary.Results)
	}
}

func TestComputeRoutingSummaryClassifiesLibraryAndReview(t *testing.T) {
	r := &Report{
		Paths: AuditPaths{ReviewDir: "/review", LibraryDir: "/library"},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{{Key: "s01e01", Episode: 1}, {Key: "s01e02", Episode: 2, NeedsReview: true}},
			Assets: ripspec.Assets{Final: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/library/show/s01e01.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "/review/show/s01e02.mkv", Status: ripspec.AssetStatusCompleted},
			}},
		},
	}

	summary := computeRoutingSummary(r)
	if summary == nil || len(summary.Entries) != 2 {
		t.Fatalf("routing summary = %+v, want 2 entries", summary)
	}
	if summary.Entries[0].Destination != "library" || !summary.Entries[0].MatchesExpected {
		t.Fatalf("first route = %+v, want library/match", summary.Entries[0])
	}
	if summary.Entries[1].Destination != "review" || !summary.Entries[1].ExpectedReview || !summary.Entries[1].MatchesExpected {
		t.Fatalf("second route = %+v, want review expected/match", summary.Entries[1])
	}
}

func TestComputeEpisodeStats_Thresholds(t *testing.T) {
	episodes := []ripspec.Episode{
		{Key: "s01e01", Episode: 1, MatchConfidence: 0.95},
		{Key: "s01e02", Episode: 2, MatchConfidence: 0.75},
		{Key: "s01e03", Episode: 3, MatchConfidence: 0.65},
		{Key: "s01e04", Episode: 4, MatchConfidence: 0.85},
	}

	stats := computeEpisodeStats(episodes)
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.Count != 4 {
		t.Errorf("count: got %d, want 4", stats.Count)
	}
	if stats.Matched != 4 {
		t.Errorf("matched: got %d, want 4", stats.Matched)
	}
	if stats.Below070 != 1 {
		t.Errorf("below070: got %d, want 1 (0.65)", stats.Below070)
	}
	if stats.Below080 != 2 {
		t.Errorf("below080: got %d, want 2 (0.65, 0.75)", stats.Below080)
	}
	if stats.Below090 != 3 {
		t.Errorf("below090: got %d, want 3 (0.65, 0.75, 0.85)", stats.Below090)
	}
	if stats.ConfidenceMin != 0.65 {
		t.Errorf("min: got %f, want 0.65", stats.ConfidenceMin)
	}
	if stats.ConfidenceMax != 0.95 {
		t.Errorf("max: got %f, want 0.95", stats.ConfidenceMax)
	}
}

func TestComputeEpisodeStats_Contiguity(t *testing.T) {
	contiguous := []ripspec.Episode{
		{Key: "s01e01", Episode: 1, MatchConfidence: 0.9},
		{Key: "s01e02", Episode: 2, MatchConfidence: 0.9},
		{Key: "s01e03", Episode: 3, MatchConfidence: 0.9},
	}
	stats := computeEpisodeStats(contiguous)
	if !stats.SequenceContiguous {
		t.Error("expected contiguous sequence")
	}
	if stats.EpisodeRange != "1-3" {
		t.Errorf("range: got %s, want 1-3", stats.EpisodeRange)
	}

	gapped := []ripspec.Episode{
		{Key: "s01e01", Episode: 1, MatchConfidence: 0.9},
		{Key: "s01e03", Episode: 3, MatchConfidence: 0.9},
		{Key: "s01e05", Episode: 5, MatchConfidence: 0.9},
	}
	stats = computeEpisodeStats(gapped)
	if stats.SequenceContiguous {
		t.Error("expected non-contiguous sequence")
	}
}

func TestComputeEpisodeStats_Unresolved(t *testing.T) {
	episodes := []ripspec.Episode{
		{Key: "s01_001", Episode: 0},
		{Key: "s01_002", Episode: 0},
	}
	stats := computeEpisodeStats(episodes)
	if stats.Matched != 0 {
		t.Errorf("matched: got %d, want 0", stats.Matched)
	}
	if stats.Unresolved != 2 {
		t.Errorf("unresolved: got %d, want 2", stats.Unresolved)
	}
	if !stats.PlaceholderOnly {
		t.Error("expected placeholder_only for unresolved placeholder manifest")
	}
}

func TestDetectAnomalies_PreEpisodeIDPlaceholdersAreInformational(t *testing.T) {
	r := &Report{
		StageGate: StageGate{PhaseEpisodeID: false, MediaHint: "tv"},
	}
	a := &Analysis{EpisodeStats: &EpisodeStats{Count: 2, Unresolved: 2, PlaceholderOnly: true}}

	anomalies := detectAnomalies(r, a)
	found := false
	for _, an := range anomalies {
		if an.Message == "2 placeholder episode(s) in pre-episode-identification manifest" {
			found = true
			if an.Severity != "info" {
				t.Fatalf("severity = %s, want info", an.Severity)
			}
		}
		if an.Message == "2 unresolved episode(s)" {
			t.Fatal("unexpected unresolved anomaly for pre-episode-identification placeholders")
		}
	}
	if !found {
		t.Fatal("expected placeholder manifest anomaly")
	}
}

func TestDetectAnomalies_FailedItem(t *testing.T) {
	r := &Report{
		Item: ItemSummary{
			ErrorMessage:  "encoding failed",
			FailedAtStage: "encoding",
		},
	}
	a := &Analysis{}

	anomalies := detectAnomalies(r, a)
	found := false
	for _, an := range anomalies {
		if an.Severity == "critical" && an.Category == "item_state" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected critical item_state anomaly for failed item")
	}
}

func TestDetectAnomalies_CleanItem(t *testing.T) {
	r := &Report{
		Item: ItemSummary{Stage: "completed"},
	}
	a := &Analysis{}

	anomalies := detectAnomalies(r, a)
	if len(anomalies) != 0 {
		t.Errorf("expected 0 anomalies for clean item, got %d", len(anomalies))
	}
}

func TestDetectAnomalies_MissingContentIDSummary(t *testing.T) {
	r := &Report{
		StageGate: StageGate{PhaseEpisodeID: true},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{{Key: "s01e01", Episode: 1}},
		},
	}
	anomalies := detectAnomalies(r, &Analysis{})
	found := false
	for _, an := range anomalies {
		if an.Message == "episode identification provenance summary missing from envelope attributes" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected missing content ID summary anomaly")
	}
}

func TestDetectAnomalies_ContentIDSummaryPresent(t *testing.T) {
	r := &Report{
		StageGate: StageGate{PhaseEpisodeID: true},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{{Key: "s01e01", Episode: 1}},
			Attributes: ripspec.EnvelopeAttributes{ContentID: &ripspec.ContentIDSummary{
				Method:               "whisperx_tfidf_hungarian",
				ReferenceSource:      "opensubtitles",
				EpisodesSynchronized: true,
				Completed:            true,
			}},
		},
	}
	anomalies := detectAnomalies(r, &Analysis{})
	for _, an := range anomalies {
		if an.Message == "episode identification provenance summary missing from envelope attributes" ||
			an.Message == "episode identification provenance summary is incomplete" {
			t.Fatalf("unexpected provenance anomaly: %+v", an)
		}
	}
}

func TestDetectTVRoutingAnomalies_FlagsCleanEpisodesInReview(t *testing.T) {
	r := &Report{
		Paths: AuditPaths{ReviewDir: "/review"},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{
				{Key: "s01e01", Episode: 1},
				{Key: "s01e02", Episode: 2, NeedsReview: true},
			},
			Assets: ripspec.Assets{Final: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/review/show/Show - S01E01.mkv", Status: ripspec.AssetStatusCompleted},
				{EpisodeKey: "s01e02", Path: "/review/show/Show - S01E02.mkv", Status: ripspec.AssetStatusCompleted},
			}},
		},
	}
	anomalies := detectTVRoutingAnomalies(r)
	found := false
	for _, an := range anomalies {
		if an.Message == "1 clean resolved episode(s) routed to review" {
			found = true
			if an.Severity != "critical" {
				t.Fatalf("severity = %s, want critical", an.Severity)
			}
		}
	}
	if !found {
		t.Fatal("expected routing anomaly for clean episode in review")
	}
}

func TestDetectTVRoutingAnomalies_FlagsReviewEpisodesInLibrary(t *testing.T) {
	r := &Report{
		Paths: AuditPaths{ReviewDir: "/review"},
		Envelope: &ripspec.Envelope{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{{Key: "s01e02", Episode: 2, NeedsReview: true}},
			Assets:   ripspec.Assets{Final: []ripspec.Asset{{EpisodeKey: "s01e02", Path: "/library/tv/Show/Season 01/Show - S01E02.mkv", Status: ripspec.AssetStatusCompleted}}},
		},
	}
	anomalies := detectTVRoutingAnomalies(r)
	found := false
	for _, an := range anomalies {
		if an.Message == "1 review-required episode(s) routed to library" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected routing anomaly for review episode in library")
	}
}

func TestCompressMediaProbes(t *testing.T) {
	majority := ProfileSummary{VideoCodec: "av1", Width: 1920, Height: 1080}

	probes := []MediaFileProbe{
		{EpisodeKey: "s01e01", Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: "av1", Width: 1920, Height: 1080}}}},
		{EpisodeKey: "s01e02", Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: "av1", Width: 1920, Height: 1080}}}},
		{EpisodeKey: "s01e03", Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: "av1", Width: 1920, Height: 1080}}}},
		{EpisodeKey: "s01e04", Probe: &ffprobe.Result{Streams: []ffprobe.Stream{{CodecType: "video", CodecName: "av1", Width: 1280, Height: 720}}}},
	}

	consistency := &EpisodeConsistency{
		MajorityProfile: majority,
		MajorityCount:   3,
		TotalEpisodes:   4,
		Deviations: []ProfileDeviation{
			{EpisodeKey: "s01e04", Differences: []string{"resolution: 1280x720 (expected 1920x1080)"}},
		},
	}

	result, omitted := compressMediaProbes(probes, consistency)

	if omitted != 2 {
		t.Errorf("omitted: got %d, want 2", omitted)
	}
	if len(result) != 2 {
		t.Fatalf("result count: got %d, want 2 (1 representative + 1 deviation)", len(result))
	}

	// First should be representative.
	if !result[0].Representative {
		t.Error("expected first result to be representative")
	}
	if result[0].EpisodeKey != "s01e01" {
		t.Errorf("representative key: got %s, want s01e01", result[0].EpisodeKey)
	}

	// Second should be the deviation.
	if result[1].EpisodeKey != "s01e04" {
		t.Errorf("deviation key: got %s, want s01e04", result[1].EpisodeKey)
	}
}

func TestCompressMediaProbes_NilConsistency(t *testing.T) {
	probes := []MediaFileProbe{{Path: "/test.mkv"}}
	result, omitted := compressMediaProbes(probes, nil)
	if omitted != 0 {
		t.Errorf("omitted: got %d, want 0", omitted)
	}
	if len(result) != 1 {
		t.Errorf("result count: got %d, want 1", len(result))
	}
}

func TestDetectDefaultAudioLanguageAnomalies_FlagsNonEnglishDefault(t *testing.T) {
	r := &Report{Media: []MediaFileProbe{{
		EpisodeKey: "s03_009",
		Path:       "/review/Batman - s03_009.mkv",
		Probe: &ffprobe.Result{Streams: []ffprobe.Stream{
			{CodecType: "audio", Tags: map[string]string{"language": "ita"}, Disposition: map[string]int{"default": 1}},
			{CodecType: "audio", Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 0}},
			{CodecType: "audio", Tags: map[string]string{"language": "ger"}, Disposition: map[string]int{"default": 0}},
		}},
	}}}

	anomalies := detectDefaultAudioLanguageAnomalies(r)
	if len(anomalies) != 1 {
		t.Fatalf("expected 1 anomaly, got %d", len(anomalies))
	}
	if anomalies[0].Severity != "warning" {
		t.Fatalf("severity = %s, want warning", anomalies[0].Severity)
	}
	if anomalies[0].Category != "media" {
		t.Fatalf("category = %s, want media", anomalies[0].Category)
	}
	want := "non-English default audio language \"it\" despite English audio present: s03_009"
	if anomalies[0].Message != want {
		t.Fatalf("message = %q, want %q", anomalies[0].Message, want)
	}
}

func TestDetectDefaultAudioLanguageAnomalies_IgnoresEnglishDefault(t *testing.T) {
	r := &Report{Media: []MediaFileProbe{{
		EpisodeKey: "s03e21",
		Probe: &ffprobe.Result{Streams: []ffprobe.Stream{
			{CodecType: "audio", Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}},
			{CodecType: "audio", Tags: map[string]string{"language": "ita"}, Disposition: map[string]int{"default": 0}},
		}},
	}}}

	anomalies := detectDefaultAudioLanguageAnomalies(r)
	if len(anomalies) != 0 {
		t.Fatalf("expected 0 anomalies, got %d: %+v", len(anomalies), anomalies)
	}
}

func TestCountDecisionConfidenceQualitiesIncludesDecisiveLowSimilarity(t *testing.T) {
	decisions := []LogDecision{
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "clear"}},
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "decisive_low_similarity", "match_confidence": 0.821}},
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "ambiguous"}},
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "contested"}},
		{DecisionType: "tmdb_match", Extras: map[string]any{"confidence_quality": "clear"}},
	}

	contested, ambiguous, decisive := countDecisionConfidenceQualities(decisions)
	if contested != 1 || ambiguous != 1 || decisive != 1 {
		t.Fatalf("counts = contested:%d ambiguous:%d decisive:%d", contested, ambiguous, decisive)
	}
}

func TestCountDecisiveLowSimilarityInConfidenceBand(t *testing.T) {
	decisions := []LogDecision{
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "decisive_low_similarity", "match_confidence": 0.821}},
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "decisive_low_similarity", "match_confidence": 0.945}},
		{DecisionType: "episode_match", Extras: map[string]any{"confidence_quality": "ambiguous", "match_confidence": 0.830}},
	}

	got := countDecisiveLowSimilarityInConfidenceBand(decisions, 0.80, 0.90)
	if got != 1 {
		t.Fatalf("decisive low-similarity count = %d, want 1", got)
	}
}

