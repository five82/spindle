package auditgather

import (
	"testing"

	"spindle/internal/encodingstate"
	"spindle/internal/media/ffprobe"
	"spindle/internal/ripspec"
)

func TestAggregateDecisions(t *testing.T) {
	tests := []struct {
		name       string
		decisions  []LogDecision
		wantGroups int
		check      func(t *testing.T, groups []DecisionGroup)
	}{
		{
			name:       "empty",
			decisions:  nil,
			wantGroups: 0,
		},
		{
			name: "singleton",
			decisions: []LogDecision{
				{DecisionType: "track_select", DecisionResult: "include", Message: "selected track 1"},
			},
			wantGroups: 1,
			check: func(t *testing.T, groups []DecisionGroup) {
				if groups[0].Count != 1 {
					t.Errorf("expected count 1, got %d", groups[0].Count)
				}
				if len(groups[0].Entries) != 1 {
					t.Errorf("expected 1 entry for singleton, got %d", len(groups[0].Entries))
				}
			},
		},
		{
			name: "identical multiples drop entries",
			decisions: []LogDecision{
				{DecisionType: "track_select", DecisionResult: "skip", DecisionReason: "duplicate", Message: "same msg"},
				{DecisionType: "track_select", DecisionResult: "skip", DecisionReason: "duplicate", Message: "same msg"},
				{DecisionType: "track_select", DecisionResult: "skip", DecisionReason: "duplicate", Message: "same msg"},
			},
			wantGroups: 1,
			check: func(t *testing.T, groups []DecisionGroup) {
				if groups[0].Count != 3 {
					t.Errorf("expected count 3, got %d", groups[0].Count)
				}
				if groups[0].Entries != nil {
					t.Errorf("expected nil entries for identical group, got %d", len(groups[0].Entries))
				}
			},
		},
		{
			name: "varying messages keep entries",
			decisions: []LogDecision{
				{DecisionType: "track_select", DecisionResult: "include", Message: "track 1"},
				{DecisionType: "track_select", DecisionResult: "include", Message: "track 2"},
			},
			wantGroups: 1,
			check: func(t *testing.T, groups []DecisionGroup) {
				if groups[0].Count != 2 {
					t.Errorf("expected count 2, got %d", groups[0].Count)
				}
				if len(groups[0].Entries) != 2 {
					t.Errorf("expected 2 entries for varying messages, got %d", len(groups[0].Entries))
				}
			},
		},
		{
			name: "mixed types preserve order",
			decisions: []LogDecision{
				{DecisionType: "track_select", DecisionResult: "include", Message: "t1"},
				{DecisionType: "preset_choice", DecisionResult: "hd", Message: "chose hd"},
				{DecisionType: "track_select", DecisionResult: "include", Message: "t2"},
			},
			wantGroups: 2,
			check: func(t *testing.T, groups []DecisionGroup) {
				if groups[0].DecisionType != "track_select" {
					t.Errorf("expected first group track_select, got %s", groups[0].DecisionType)
				}
				if groups[1].DecisionType != "preset_choice" {
					t.Errorf("expected second group preset_choice, got %s", groups[1].DecisionType)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := aggregateDecisions(tt.decisions)
			if len(groups) != tt.wantGroups {
				t.Fatalf("expected %d groups, got %d", tt.wantGroups, len(groups))
			}
			if tt.check != nil {
				tt.check(t, groups)
			}
		})
	}
}

func TestComputeEpisodeConsistency(t *testing.T) {
	makeProbe := func(key, codec string, w, h int, audioCodecs ...string) MediaFileProbe {
		streams := []ffprobe.Stream{
			{CodecName: codec, CodecType: "video", Width: w, Height: h},
		}
		for _, ac := range audioCodecs {
			streams = append(streams, ffprobe.Stream{
				CodecName: ac, CodecType: "audio", Channels: 6, ChannelLayout: "5.1",
			})
		}
		return MediaFileProbe{
			EpisodeKey:  key,
			DurationSec: 2400,
			SizeBytes:   1000000,
			Probe:       ffprobe.Result{Streams: streams},
		}
	}

	tests := []struct {
		name           string
		probes         []MediaFileProbe
		wantNil        bool
		wantDeviations int
	}{
		{
			name:    "fewer than 2 probes returns nil",
			probes:  []MediaFileProbe{makeProbe("s01e01", "av1", 1920, 1080, "opus")},
			wantNil: true,
		},
		{
			name: "all match no deviations",
			probes: []MediaFileProbe{
				makeProbe("s01e01", "av1", 1920, 1080, "opus"),
				makeProbe("s01e02", "av1", 1920, 1080, "opus"),
				makeProbe("s01e03", "av1", 1920, 1080, "opus"),
			},
			wantDeviations: 0,
		},
		{
			name: "one outlier",
			probes: []MediaFileProbe{
				makeProbe("s01e01", "av1", 1920, 1080, "opus"),
				makeProbe("s01e02", "av1", 1920, 1080, "opus"),
				makeProbe("s01e03", "av1", 1280, 720, "opus"),
			},
			wantDeviations: 1,
		},
		{
			name: "skip errored probes",
			probes: []MediaFileProbe{
				makeProbe("s01e01", "av1", 1920, 1080, "opus"),
				{EpisodeKey: "s01e02", Error: "file not found"},
				makeProbe("s01e03", "av1", 1920, 1080, "opus"),
			},
			wantDeviations: 0,
		},
		{
			name: "all errored returns nil",
			probes: []MediaFileProbe{
				{EpisodeKey: "s01e01", Error: "fail"},
				{EpisodeKey: "s01e02", Error: "fail"},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeEpisodeConsistency(tt.probes)
			if tt.wantNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			if len(result.Deviations) != tt.wantDeviations {
				t.Errorf("expected %d deviations, got %d", tt.wantDeviations, len(result.Deviations))
			}
		})
	}
}

func TestParseCropFilter(t *testing.T) {
	tests := []struct {
		filter string
		wantW  int
		wantH  int
		wantOK bool
	}{
		{"crop=1920:800:0:140", 1920, 800, true},
		{"1920:800:0:140", 1920, 800, true},
		{"crop=3840:1600:0:280", 3840, 1600, true},
		{"", 0, 0, false},
		{"invalid", 0, 0, false},
		{"crop=abc:def:0:0", 0, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			w, h, ok := parseCropFilter(tt.filter)
			if ok != tt.wantOK {
				t.Fatalf("parseCropFilter(%q) ok=%v, want %v", tt.filter, ok, tt.wantOK)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("parseCropFilter(%q) = (%d, %d), want (%d, %d)", tt.filter, w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestMatchStandardRatio(t *testing.T) {
	tests := []struct {
		ratio float64
		want  string
	}{
		{1.33, "4:3"},
		{1.78, "16:9"},
		{1.85, "1.85:1"},
		{2.00, "2.00:1"},
		{2.20, "2.20:1"},
		{2.35, "2.35:1"},
		{2.39, "2.39:1"},
		{2.40, "2.40:1"},
		{3.00, "3.00:1"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := matchStandardRatio(tt.ratio)
			if got != tt.want {
				t.Errorf("matchStandardRatio(%.2f) = %q, want %q", tt.ratio, got, tt.want)
			}
		})
	}
}

func TestComputeEpisodeStats(t *testing.T) {
	tests := []struct {
		name     string
		episodes []ripspec.Episode
		wantNil  bool
		check    func(t *testing.T, s *EpisodeStats)
	}{
		{
			name:    "empty",
			wantNil: true,
		},
		{
			name: "contiguous sequence",
			episodes: []ripspec.Episode{
				{Key: "s01e01", Episode: 1, MatchConfidence: 0.95},
				{Key: "s01e02", Episode: 2, MatchConfidence: 0.90},
				{Key: "s01e03", Episode: 3, MatchConfidence: 0.85},
			},
			check: func(t *testing.T, s *EpisodeStats) {
				if s.Count != 3 {
					t.Errorf("count: got %d, want 3", s.Count)
				}
				if s.Matched != 3 {
					t.Errorf("matched: got %d, want 3", s.Matched)
				}
				if !s.SequenceContiguous {
					t.Error("expected contiguous sequence")
				}
				if s.EpisodeRange != "1-3" {
					t.Errorf("range: got %q, want %q", s.EpisodeRange, "1-3")
				}
				if s.ConfidenceMin != 0.85 {
					t.Errorf("confidence min: got %.2f, want 0.85", s.ConfidenceMin)
				}
				if s.Below090 != 1 {
					t.Errorf("below090: got %d, want 1", s.Below090)
				}
			},
		},
		{
			name: "non-contiguous with gaps",
			episodes: []ripspec.Episode{
				{Key: "s01e01", Episode: 1, MatchConfidence: 0.95},
				{Key: "s01e03", Episode: 3, MatchConfidence: 0.95},
			},
			check: func(t *testing.T, s *EpisodeStats) {
				if s.SequenceContiguous {
					t.Error("expected non-contiguous")
				}
			},
		},
		{
			name: "mixed resolved and unresolved",
			episodes: []ripspec.Episode{
				{Key: "s01e01", Episode: 1, MatchConfidence: 0.95},
				{Key: "s01_002", Episode: 0, MatchConfidence: 0},
			},
			check: func(t *testing.T, s *EpisodeStats) {
				if s.Matched != 1 {
					t.Errorf("matched: got %d, want 1", s.Matched)
				}
				if s.Unresolved != 1 {
					t.Errorf("unresolved: got %d, want 1", s.Unresolved)
				}
				if s.ConfidenceMin != 0.95 {
					t.Errorf("confidence min: got %.2f, want 0.95 (zero excluded)", s.ConfidenceMin)
				}
			},
		},
		{
			name: "confidence thresholds cumulative",
			episodes: []ripspec.Episode{
				{Key: "s01e01", Episode: 1, MatchConfidence: 0.65},
				{Key: "s01e02", Episode: 2, MatchConfidence: 0.75},
				{Key: "s01e03", Episode: 3, MatchConfidence: 0.85},
				{Key: "s01e04", Episode: 4, MatchConfidence: 0.95},
			},
			check: func(t *testing.T, s *EpisodeStats) {
				if s.Below070 != 1 {
					t.Errorf("below070: got %d, want 1", s.Below070)
				}
				if s.Below080 != 2 {
					t.Errorf("below080: got %d, want 2 (cumulative)", s.Below080)
				}
				if s.Below090 != 3 {
					t.Errorf("below090: got %d, want 3 (cumulative)", s.Below090)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeEpisodeStats(tt.episodes)
			if tt.wantNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil")
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestComputeMediaStats(t *testing.T) {
	tests := []struct {
		name    string
		probes  []MediaFileProbe
		wantNil bool
		check   func(t *testing.T, s *MediaStats)
	}{
		{
			name: "multiple probes",
			probes: []MediaFileProbe{
				{DurationSec: 2400, SizeBytes: 1000000},
				{DurationSec: 2800, SizeBytes: 1500000},
				{DurationSec: 2600, SizeBytes: 1200000},
			},
			check: func(t *testing.T, s *MediaStats) {
				if s.FileCount != 3 {
					t.Errorf("file count: got %d, want 3", s.FileCount)
				}
				if s.DurationMinSec != 2400 {
					t.Errorf("duration min: got %.0f, want 2400", s.DurationMinSec)
				}
				if s.DurationMaxSec != 2800 {
					t.Errorf("duration max: got %.0f, want 2800", s.DurationMaxSec)
				}
				if s.SizeMinBytes != 1000000 {
					t.Errorf("size min: got %d, want 1000000", s.SizeMinBytes)
				}
				if s.SizeMaxBytes != 1500000 {
					t.Errorf("size max: got %d, want 1500000", s.SizeMaxBytes)
				}
			},
		},
		{
			name: "errored probes excluded",
			probes: []MediaFileProbe{
				{DurationSec: 2400, SizeBytes: 1000000},
				{Error: "file not found"},
			},
			check: func(t *testing.T, s *MediaStats) {
				if s.FileCount != 1 {
					t.Errorf("file count: got %d, want 1", s.FileCount)
				}
			},
		},
		{
			name: "single probe",
			probes: []MediaFileProbe{
				{DurationSec: 2400, SizeBytes: 1000000},
			},
			check: func(t *testing.T, s *MediaStats) {
				if s.DurationMinSec != s.DurationMaxSec {
					t.Error("single probe: min and max should be equal")
				}
			},
		},
		{
			name: "all errored returns nil",
			probes: []MediaFileProbe{
				{Error: "fail"},
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeMediaStats(tt.probes)
			if tt.wantNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil")
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestComputeAssetHealth(t *testing.T) {
	tests := []struct {
		name    string
		assets  *ripspec.Assets
		wantNil bool
		check   func(t *testing.T, h *AssetHealth)
	}{
		{
			name:    "nil assets",
			assets:  nil,
			wantNil: true,
		},
		{
			name:    "empty assets",
			assets:  &ripspec.Assets{},
			wantNil: true,
		},
		{
			name: "mixed ok and failed",
			assets: &ripspec.Assets{
				Ripped: []ripspec.Asset{
					{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed"},
					{EpisodeKey: "s01e02", Path: "/b.mkv", Status: "failed"},
				},
				Encoded: []ripspec.Asset{
					{EpisodeKey: "s01e01", Path: "/c.mkv", Status: "completed"},
				},
			},
			check: func(t *testing.T, h *AssetHealth) {
				if h.Ripped == nil {
					t.Fatal("expected ripped counts")
				}
				if h.Ripped.Total != 2 || h.Ripped.OK != 1 || h.Ripped.Failed != 1 {
					t.Errorf("ripped: total=%d ok=%d failed=%d", h.Ripped.Total, h.Ripped.OK, h.Ripped.Failed)
				}
				if h.Encoded == nil || h.Encoded.Total != 1 {
					t.Error("expected encoded counts")
				}
			},
		},
		{
			name: "muxed tracking for subtitled",
			assets: &ripspec.Assets{
				Subtitled: []ripspec.Asset{
					{EpisodeKey: "s01e01", Path: "/a.mkv", Status: "completed", SubtitlesMuxed: true},
					{EpisodeKey: "s01e02", Path: "/b.mkv", Status: "completed", SubtitlesMuxed: false},
				},
			},
			check: func(t *testing.T, h *AssetHealth) {
				if h.Subtitled == nil {
					t.Fatal("expected subtitled counts")
				}
				if h.Subtitled.Muxed != 1 {
					t.Errorf("muxed: got %d, want 1", h.Subtitled.Muxed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computeAssetHealth(tt.assets)
			if tt.wantNil {
				if result != nil {
					t.Fatalf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected non-nil")
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestDetectAnomalies(t *testing.T) {
	tests := []struct {
		name     string
		report   *Report
		analysis *Analysis
		wantNil  bool
		check    func(t *testing.T, anomalies []Anomaly)
	}{
		{
			name:     "clean item produces nil",
			report:   &Report{Item: ItemSummary{Status: "completed"}},
			analysis: &Analysis{},
			wantNil:  true,
		},
		{
			name: "failed item",
			report: &Report{
				Item: ItemSummary{
					Status:         "failed",
					FailedAtStatus: "encoding",
					ErrorMessage:   "encoder crashed",
				},
			},
			analysis: &Analysis{},
			check: func(t *testing.T, anomalies []Anomaly) {
				found := false
				for _, a := range anomalies {
					if a.Category == "item_state" && a.Severity == "critical" {
						found = true
					}
				}
				if !found {
					t.Error("expected critical item_state anomaly")
				}
			},
		},
		{
			name:   "low confidence episodes",
			report: &Report{},
			analysis: &Analysis{
				EpisodeStats: &EpisodeStats{
					Count:              3,
					Matched:            3,
					Below070:           1,
					Below080:           2,
					Below090:           3,
					SequenceContiguous: true,
					EpisodeRange:       "1-3",
				},
			},
			check: func(t *testing.T, anomalies []Anomaly) {
				critCount := 0
				warnCount := 0
				infoCount := 0
				for _, a := range anomalies {
					if a.Category == "episodes" {
						switch a.Severity {
						case "critical":
							critCount++
						case "warning":
							warnCount++
						case "info":
							infoCount++
						}
					}
				}
				if critCount != 1 {
					t.Errorf("expected 1 critical episode anomaly (below 0.70), got %d", critCount)
				}
				if warnCount != 1 {
					t.Errorf("expected 1 warning episode anomaly (below 0.80 tier), got %d", warnCount)
				}
				if infoCount != 1 {
					t.Errorf("expected 1 info episode anomaly (below 0.90 tier), got %d", infoCount)
				}
			},
		},
		{
			name: "validation failure",
			report: &Report{
				Encoding: &EncodingReport{
					Snapshot: encodingstate.Snapshot{
						Validation: &encodingstate.Validation{Passed: false},
					},
				},
			},
			analysis: &Analysis{},
			check: func(t *testing.T, anomalies []Anomaly) {
				found := false
				for _, a := range anomalies {
					if a.Category == "encoding" && a.Severity == "critical" {
						found = true
					}
				}
				if !found {
					t.Error("expected critical encoding anomaly for validation failure")
				}
			},
		},
		{
			name:   "non-contiguous sequence",
			report: &Report{},
			analysis: &Analysis{
				EpisodeStats: &EpisodeStats{
					Count:              2,
					Matched:            2,
					SequenceContiguous: false,
					EpisodeRange:       "1-3",
				},
			},
			check: func(t *testing.T, anomalies []Anomaly) {
				found := false
				for _, a := range anomalies {
					if a.Category == "episodes" && a.Severity == "warning" &&
						a.Message == "non-contiguous episode sequence: 1-3" {
						found = true
					}
				}
				if !found {
					t.Error("expected non-contiguous sequence anomaly")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			anomalies := detectAnomalies(tt.report, tt.analysis)
			if tt.wantNil {
				if anomalies != nil {
					t.Fatalf("expected nil, got %+v", anomalies)
				}
				return
			}
			if anomalies == nil {
				t.Fatal("expected non-nil anomalies")
			}
			if tt.check != nil {
				tt.check(t, anomalies)
			}
		})
	}
}
