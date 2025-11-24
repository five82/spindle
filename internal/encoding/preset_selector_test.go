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
	cfg.DeepSeekPresetDeciderEnabled = true
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
	item := &queue.Item{MetadataJSON: `{"title":"South Park","media_type":"tv","season_number":5,"release_date":"1997-07-01"}`}
	decision := enc.selectPreset(context.Background(), item, "", logging.NewNop())
	if !decision.Applied {
		t.Fatalf("expected preset to apply, decision=%+v", decision)
	}
	if decision.Profile != "clean" {
		t.Fatalf("expected clean profile, got %q", decision.Profile)
	}
	if decision.Description != "South Park Season 5 1997 hd tv show" {
		t.Fatalf("unexpected description: %q", decision.Description)
	}
}

func TestSelectPresetSkipsLowConfidence(t *testing.T) {
	cfg := testsupport.NewConfig(t, testsupport.WithStubbedBinaries())
	cfg.DeepSeekPresetDeciderEnabled = true
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
