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

func TestNewAnalyzer(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	a := NewAnalyzer(cfg, store, logging.NewNop())
	if a == nil {
		t.Fatal("expected analyzer to be created")
	}
	if a.cfg != cfg {
		t.Error("analyzer cfg mismatch")
	}
	if a.store != store {
		t.Error("analyzer store mismatch")
	}
}

func TestAnalyzer_HealthCheck(t *testing.T) {
	tests := []struct {
		name      string
		analyzer  *Analyzer
		wantReady bool
	}{
		{
			name:      "nil analyzer",
			analyzer:  nil,
			wantReady: false,
		},
		{
			name:      "nil config",
			analyzer:  &Analyzer{cfg: nil},
			wantReady: false,
		},
		{
			name:      "nil store",
			analyzer:  &Analyzer{cfg: &config.Config{}},
			wantReady: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := tt.analyzer.HealthCheck(context.Background())
			if health.Ready != tt.wantReady {
				t.Errorf("HealthCheck().Ready = %v, want %v", health.Ready, tt.wantReady)
			}
		})
	}
	t.Run("valid analyzer", func(t *testing.T) {
		cfg := testConfig(t)
		store, err := queue.Open(cfg)
		if err != nil {
			t.Fatalf("queue.Open: %v", err)
		}
		defer store.Close()
		a := NewAnalyzer(cfg, store, logging.NewNop())
		health := a.HealthCheck(context.Background())
		if !health.Ready {
			t.Errorf("HealthCheck().Ready = false, want true; detail: %s", health.Detail)
		}
	})
}

func TestAnalyzer_SetLogger(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	a := NewAnalyzer(cfg, store, nil)
	// Note: NewAnalyzer may initialize the logger even with nil input

	newLogger := logging.NewNop()
	a.SetLogger(newLogger)
	if a.logger == nil {
		t.Error("expected logger to be set after SetLogger")
	}
}

func TestAnalyzer_SetLoggerNilAnalyzer(t *testing.T) {
	var a *Analyzer
	// Should not panic
	a.SetLogger(logging.NewNop())
}

func TestAnalyzer_PrepareErrors(t *testing.T) {
	tests := []struct {
		name     string
		analyzer *Analyzer
	}{
		{"nil analyzer", nil},
		{"nil config", &Analyzer{cfg: nil}},
		{"nil store", &Analyzer{cfg: &config.Config{}, store: nil}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.analyzer.Prepare(context.Background(), &queue.Item{})
			if err == nil {
				t.Errorf("expected error for %s", tt.name)
			}
		})
	}
}

func TestAnalyzer_ExecuteNilAnalyzer(t *testing.T) {
	var a *Analyzer
	err := a.Execute(context.Background(), &queue.Item{})
	if err == nil {
		t.Error("expected error for nil analyzer")
	}
}

func TestAnalyzer_ExecuteNilItem(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	a := NewAnalyzer(cfg, store, logging.NewNop())
	err = a.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil item")
	}
}

func TestAnalyzer_ExecuteMissingRipSpec(t *testing.T) {
	cfg := testConfig(t)
	store, err := queue.Open(cfg)
	if err != nil {
		t.Fatalf("queue.Open: %v", err)
	}
	defer store.Close()

	a := NewAnalyzer(cfg, store, logging.NewNop())
	item := &queue.Item{RipSpecData: ""}
	err = a.Execute(context.Background(), item)
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
				if env.Attributes.AudioAnalysis != nil {
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
				analysis := env.Attributes.AudioAnalysis
				if analysis == nil {
					t.Fatal("expected AudioAnalysis to be set")
				}
				if analysis.PrimaryTrack.Index != 1 {
					t.Errorf("primary_track.index = %v, want 1", analysis.PrimaryTrack.Index)
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
				analysis := env.Attributes.AudioAnalysis
				if analysis == nil {
					t.Fatal("expected AudioAnalysis to be set")
				}
				if len(analysis.CommentaryTracks) != 1 {
					t.Fatalf("expected 1 commentary track, got %d", len(analysis.CommentaryTracks))
				}
				if analysis.CommentaryTracks[0].Index != 2 {
					t.Errorf("commentary track index = %v, want 2", analysis.CommentaryTracks[0].Index)
				}
				if analysis.CommentaryTracks[0].Confidence != 0.95 {
					t.Errorf("commentary track confidence = %v, want 0.95", analysis.CommentaryTracks[0].Confidence)
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
				analysis := env.Attributes.AudioAnalysis
				if analysis == nil {
					t.Fatal("expected AudioAnalysis to be set")
				}
				if len(analysis.ExcludedTracks) != 1 {
					t.Fatalf("expected 1 excluded track, got %d", len(analysis.ExcludedTracks))
				}
				if analysis.ExcludedTracks[0].Reason != "stereo_downmix" {
					t.Errorf("excluded track reason = %v, want stereo_downmix", analysis.ExcludedTracks[0].Reason)
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
