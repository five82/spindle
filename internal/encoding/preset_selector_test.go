package encoding

import (
	"context"
	"testing"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/testsupport"
)

type presetClassifierFunc func(ctx context.Context, req presetRequest) (presetClassification, error)

func (f presetClassifierFunc) Classify(ctx context.Context, req presetRequest) (presetClassification, error) {
	return f(ctx, req)
}

func TestSelectPresetAppliesLLMProfile(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.PresetDecider.Enabled = true
	enc := &Encoder{cfg: cfg, presetClassifier: presetClassifierFunc(func(ctx context.Context, req presetRequest) (presetClassification, error) {
		return presetClassification{
			Profile:     "clean",
			Confidence:  0.92,
			Reason:      "animated series",
			Description: req.Description(),
			Source:      "test",
		}, nil
	})}
	enc.SetLogger(logging.NewNop())
	item := &queue.Item{MetadataJSON: `{"title":"South Park","media_type":"tv","season_number":5,"first_air_date":"1997-08-13","episode_air_dates":["2001-07-11","2001-06-20","2001-06-27"]}`}
	decision := enc.selectPreset(context.Background(), item, "", logging.NewNop())
	if !decision.Applied {
		t.Fatalf("expected preset to apply, decision=%+v", decision)
	}
	if decision.Profile != "clean" {
		t.Fatalf("expected clean profile, got %q", decision.Profile)
	}
	if decision.Description != "South Park Season 5 (type: tv show) (season aired: 2001) (resolution: 1080p/HD) (source: blu-ray)" {
		t.Fatalf("unexpected description: %q", decision.Description)
	}
}

func TestSelectPresetSkipsLowConfidence(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.PresetDecider.Enabled = true
	enc := &Encoder{cfg: cfg, presetClassifier: presetClassifierFunc(func(ctx context.Context, req presetRequest) (presetClassification, error) {
		return presetClassification{
			Profile:     "grain",
			Confidence:  0.42,
			Reason:      "unsure",
			Description: req.Description(),
			Source:      "test",
		}, nil
	})}
	enc.SetLogger(logging.NewNop())
	item := &queue.Item{MetadataJSON: `{"title":"Film","media_type":"movie","release_date":"1965-03-01"}`}
	decision := enc.selectPreset(context.Background(), item, "", logging.NewNop())
	if decision.Applied {
		t.Fatalf("expected preset not to apply, got %+v", decision)
	}
	if decision.Profile != "" {
		t.Fatalf("expected empty profile, got %q", decision.Profile)
	}
}

func TestParseSeasonAirYear(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     string
	}{
		{
			name:     "extracts earliest year from episode air dates",
			metadata: `{"episode_air_dates":["2001-07-11","2001-06-20","2001-06-27"]}`,
			want:     "2001",
		},
		{
			name:     "handles single air date",
			metadata: `{"episode_air_dates":["2001-07-11"]}`,
			want:     "2001",
		},
		{
			name:     "returns empty when no air dates",
			metadata: `{"episode_air_dates":[]}`,
			want:     "",
		},
		{
			name:     "returns empty when field missing",
			metadata: `{"title":"South Park"}`,
			want:     "",
		},
		{
			name:     "handles malformed dates gracefully",
			metadata: `{"episode_air_dates":["invalid","2001-06-27"]}`,
			want:     "2001",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSeasonAirYear(tt.metadata)
			if got != tt.want {
				t.Errorf("parseSeasonAirYear() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPresetRequestDescription(t *testing.T) {
	tests := []struct {
		name string
		req  presetRequest
		want string
	}{
		{
			name: "tv show with season air year different from show year",
			req: presetRequest{
				Title:         "South Park",
				Season:        5,
				Year:          "1997",
				SeasonAirYear: "2001",
				Resolution:    "1080p/HD",
				MediaType:     "tv show",
			},
			want: "South Park Season 5 (type: tv show) (season aired: 2001) (resolution: 1080p/HD) (source: blu-ray)",
		},
		{
			name: "tv show without season air year",
			req: presetRequest{
				Title:      "South Park",
				Season:     1,
				Year:       "1997",
				Resolution: "1080p/HD",
				MediaType:  "tv show",
			},
			want: "South Park Season 1 (type: tv show) (year: 1997) (resolution: 1080p/HD) (source: blu-ray)",
		},
		{
			name: "tv show with season air year same as show year",
			req: presetRequest{
				Title:         "South Park",
				Season:        1,
				Year:          "1997",
				SeasonAirYear: "1997",
				Resolution:    "1080p/HD",
				MediaType:     "tv show",
			},
			want: "South Park Season 1 (type: tv show) (year: 1997) (resolution: 1080p/HD) (source: blu-ray)",
		},
		{
			name: "movie with blu-ray source",
			req: presetRequest{
				Title:      "Toy Story",
				Year:       "1995",
				Resolution: "1080p/HD",
				MediaType:  "movie",
			},
			want: "Toy Story (type: movie) (year: 1995) (resolution: 1080p/HD) (source: blu-ray)",
		},
		{
			name: "movie with dvd source",
			req: presetRequest{
				Title:      "The Matrix",
				Year:       "1999",
				Resolution: "480p/SD",
				MediaType:  "movie",
			},
			want: "The Matrix (type: movie) (year: 1999) (resolution: 480p/SD) (source: dvd)",
		},
		{
			name: "movie with 4k source",
			req: presetRequest{
				Title:      "Blade Runner",
				Year:       "1982",
				Resolution: "3840p/4K",
				MediaType:  "movie",
			},
			want: "Blade Runner (type: movie) (year: 1982) (resolution: 3840p/4K) (source: 4k blu-ray)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.Description()
			if got != tt.want {
				t.Errorf("Description() = %q, want %q", got, tt.want)
			}
		})
	}
}
