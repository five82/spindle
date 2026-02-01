package audioanalysis

import (
	"context"
	"strings"
	"testing"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := config.Default()
	cfg.TMDB.APIKey = "test-key"
	cfg.Paths.StagingDir = base + "/staging"
	cfg.Paths.LibraryDir = base + "/library"
	cfg.Paths.LogDir = base + "/logs"
	cfg.Paths.ReviewDir = base + "/review"
	return &cfg
}

func TestNewStage(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	s := NewStage(cfg, store, logging.NewNop())
	if s == nil {
		t.Fatal("expected stage to be created")
	}
	if s.cfg != cfg {
		t.Error("stage cfg mismatch")
	}
	if s.store != store {
		t.Error("stage store mismatch")
	}
}

func TestStage_HealthCheck(t *testing.T) {
	tests := []struct {
		name      string
		stage     *Stage
		wantReady bool
	}{
		{
			name:      "nil stage",
			stage:     nil,
			wantReady: false,
		},
		{
			name:      "nil config",
			stage:     &Stage{cfg: nil},
			wantReady: false,
		},
		{
			name:      "valid stage",
			stage:     &Stage{cfg: &config.Config{}},
			wantReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.stage.HealthCheck(context.Background())
			if health.Ready != tt.wantReady {
				t.Errorf("HealthCheck().Ready = %v, want %v", health.Ready, tt.wantReady)
			}
		})
	}
}

func TestStage_SetLogger(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	s := NewStage(cfg, store, nil)
	// Note: NewStage may initialize the logger even with nil input

	newLogger := logging.NewNop()
	s.SetLogger(newLogger)
	if s.logger == nil {
		t.Error("expected logger to be set after SetLogger")
	}
}

func TestStage_SetLoggerNilStage(t *testing.T) {
	var s *Stage
	// Should not panic
	s.SetLogger(logging.NewNop())
}

func TestStage_PrepareErrors(t *testing.T) {
	tests := []struct {
		name  string
		stage *Stage
	}{
		{"nil stage", nil},
		{"nil config", &Stage{cfg: nil}},
		{"nil store", &Stage{cfg: &config.Config{}, store: nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.stage.Prepare(context.Background(), &queue.Item{})
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestStage_ExecuteNilStage(t *testing.T) {
	var s *Stage
	err := s.Execute(context.Background(), &queue.Item{})
	if err == nil {
		t.Error("expected error for nil stage")
	}
}

func TestStage_ExecuteNilItem(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	s := NewStage(cfg, store, logging.NewNop())
	err = s.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil item")
	}
}

func TestStage_ExecuteMissingRipSpec(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	s := NewStage(cfg, store, logging.NewNop())
	item := &queue.Item{RipSpecData: ""}
	err = s.Execute(context.Background(), item)
	if err == nil {
		t.Error("expected error for missing rip spec")
	}
}

func TestBuildAnalysisTargets(t *testing.T) {
	tests := []struct {
		name    string
		env     *ripspec.Envelope
		item    *queue.Item
		wantLen int
	}{
		{
			name:    "nil envelope",
			env:     nil,
			item:    &queue.Item{EncodedFile: "/path/to/encoded.mkv"},
			wantLen: 0,
		},
		{
			name: "encoded assets in envelope",
			env: &ripspec.Envelope{
				Assets: ripspec.Assets{
					Encoded: []ripspec.Asset{
						{Path: "/path/to/ep1.mkv"},
						{Path: "/path/to/ep2.mkv"},
					},
				},
			},
			item:    &queue.Item{},
			wantLen: 2,
		},
		{
			name:    "fallback to item encoded file",
			env:     &ripspec.Envelope{},
			item:    &queue.Item{EncodedFile: "/path/to/encoded.mkv"},
			wantLen: 1,
		},
		{
			name:    "no targets available",
			env:     &ripspec.Envelope{},
			item:    &queue.Item{},
			wantLen: 0,
		},
		{
			name: "skips empty paths",
			env: &ripspec.Envelope{
				Assets: ripspec.Assets{
					Encoded: []ripspec.Asset{
						{Path: "/path/to/valid.mkv"},
						{Path: ""},
						{Path: "  "},
					},
				},
			},
			item:    &queue.Item{},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := buildAnalysisTargets(tt.env, tt.item)
			if len(targets) != tt.wantLen {
				t.Errorf("buildAnalysisTargets() returned %d targets, want %d", len(targets), tt.wantLen)
			}
		})
	}
}

func TestBuildCompletionMessage(t *testing.T) {
	tests := []struct {
		name       string
		refine     AudioRefinementResult
		commentary *CommentaryResult
		wantParts  []string
	}{
		{
			name:       "basic completion",
			refine:     AudioRefinementResult{},
			commentary: nil,
			wantParts:  []string{"Audio analysis complete"},
		},
		{
			name:       "with primary audio",
			refine:     AudioRefinementResult{PrimaryAudioDescription: "TrueHD 7.1 Atmos"},
			commentary: nil,
			wantParts:  []string{"Audio analysis complete", "Primary: TrueHD 7.1 Atmos"},
		},
		{
			name:   "with commentary tracks",
			refine: AudioRefinementResult{PrimaryAudioDescription: "DTS-HD MA 5.1"},
			commentary: &CommentaryResult{
				CommentaryTracks: []CommentaryTrack{
					{Index: 1, Confidence: 0.95},
					{Index: 2, Confidence: 0.90},
				},
			},
			wantParts: []string{"Primary: DTS-HD MA 5.1", "Commentary: 2 track(s)"},
		},
		{
			name:       "empty commentary result",
			refine:     AudioRefinementResult{},
			commentary: &CommentaryResult{},
			wantParts:  []string{"Audio analysis complete"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCompletionMessage(tt.refine, tt.commentary)
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("buildCompletionMessage() = %q, missing %q", got, part)
				}
			}
		})
	}
}

func TestStoreCommentaryResult(t *testing.T) {
	tests := []struct {
		name   string
		env    *ripspec.Envelope
		result *CommentaryResult
		check  func(t *testing.T, env *ripspec.Envelope)
	}{
		{
			name:   "nil envelope",
			env:    nil,
			result: &CommentaryResult{PrimaryTrack: TrackInfo{Index: 1}},
			check:  func(t *testing.T, env *ripspec.Envelope) {},
		},
		{
			name:   "nil result",
			env:    &ripspec.Envelope{},
			result: nil,
			check: func(t *testing.T, env *ripspec.Envelope) {
				if env.Attributes != nil && env.Attributes["audio_analysis"] != nil {
					t.Error("expected no audio_analysis attribute for nil result")
				}
			},
		},
		{
			name: "stores primary track",
			env:  &ripspec.Envelope{},
			result: &CommentaryResult{
				PrimaryTrack: TrackInfo{Index: 1},
			},
			check: func(t *testing.T, env *ripspec.Envelope) {
				if env.Attributes == nil {
					t.Fatal("expected Attributes to be initialized")
				}
				analysis, ok := env.Attributes["audio_analysis"].(map[string]any)
				if !ok {
					t.Fatal("expected audio_analysis map")
				}
				primary, ok := analysis["primary_track"].(map[string]any)
				if !ok {
					t.Fatal("expected primary_track map")
				}
				if primary["index"] != 1 {
					t.Errorf("primary_track.index = %v, want 1", primary["index"])
				}
			},
		},
		{
			name: "stores commentary tracks",
			env:  &ripspec.Envelope{},
			result: &CommentaryResult{
				PrimaryTrack: TrackInfo{Index: 1},
				CommentaryTracks: []CommentaryTrack{
					{Index: 2, Confidence: 0.95, Reason: "director commentary"},
				},
			},
			check: func(t *testing.T, env *ripspec.Envelope) {
				analysis := env.Attributes["audio_analysis"].(map[string]any)
				tracks, ok := analysis["commentary_tracks"].([]map[string]any)
				if !ok {
					t.Fatal("expected commentary_tracks slice")
				}
				if len(tracks) != 1 {
					t.Fatalf("expected 1 commentary track, got %d", len(tracks))
				}
				if tracks[0]["index"] != 2 {
					t.Errorf("commentary track index = %v, want 2", tracks[0]["index"])
				}
				if tracks[0]["confidence"] != 0.95 {
					t.Errorf("commentary track confidence = %v, want 0.95", tracks[0]["confidence"])
				}
			},
		},
		{
			name: "stores excluded tracks",
			env:  &ripspec.Envelope{},
			result: &CommentaryResult{
				PrimaryTrack: TrackInfo{Index: 1},
				ExcludedTracks: []ExcludedTrack{
					{Index: 3, Reason: "stereo_downmix", Similarity: 0.92},
				},
			},
			check: func(t *testing.T, env *ripspec.Envelope) {
				analysis := env.Attributes["audio_analysis"].(map[string]any)
				excluded, ok := analysis["excluded_tracks"].([]map[string]any)
				if !ok {
					t.Fatal("expected excluded_tracks slice")
				}
				if len(excluded) != 1 {
					t.Fatalf("expected 1 excluded track, got %d", len(excluded))
				}
				if excluded[0]["reason"] != "stereo_downmix" {
					t.Errorf("excluded track reason = %v, want stereo_downmix", excluded[0]["reason"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeCommentaryResult(tt.env, tt.result)
			tt.check(t, tt.env)
		})
	}
}
