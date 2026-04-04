package auditgather

import (
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
		Envelope: &EnvelopeReport{
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
		Envelope: &EnvelopeReport{
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
		Envelope: &EnvelopeReport{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{
				{Key: "s01e01", Episode: 1},
				{Key: "s01e02", Episode: 2, NeedsReview: true},
			},
			Assets: ripspec.Assets{Final: []ripspec.Asset{
				{EpisodeKey: "s01e01", Path: "/review/show/Show - S01E01.mkv", Status: "completed"},
				{EpisodeKey: "s01e02", Path: "/review/show/Show - S01E02.mkv", Status: "completed"},
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
		Envelope: &EnvelopeReport{
			Metadata: ripspec.Metadata{MediaType: "tv"},
			Episodes: []ripspec.Episode{{Key: "s01e02", Episode: 2, NeedsReview: true}},
			Assets:   ripspec.Assets{Final: []ripspec.Asset{{EpisodeKey: "s01e02", Path: "/library/tv/Show/Season 01/Show - S01E02.mkv", Status: "completed"}}},
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
