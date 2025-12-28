package commentary

import (
	"testing"

	"spindle/internal/config"
)

func TestClassifyMetadata(t *testing.T) {
	positive, negative := classifyMetadata("Director Commentary")
	if !positive || negative != "" {
		t.Fatalf("expected positive commentary metadata, got positive=%v negative=%q", positive, negative)
	}
	positive, negative = classifyMetadata("Audio Description")
	if positive || negative == "" {
		t.Fatalf("expected negative audio description metadata, got positive=%v negative=%q", positive, negative)
	}
}

func TestClassifyRules(t *testing.T) {
	cfg := defaultCommentaryConfig()

	metrics := Metrics{
		SpeechRatio:              0.30,
		SpeechOverlapWithPrimary: 0.10,
		SpeechInPrimarySilence:   0.05,
		FingerprintSimilarity:    0.20,
		PrimarySpeechRatio:       0.25,
	}
	include, reason := classify(metrics, Metadata{}, cfg)
	if !include || reason != "commentary_only" {
		t.Fatalf("expected commentary_only, got include=%v reason=%q", include, reason)
	}

	metrics = Metrics{
		SpeechRatio:              0.40,
		SpeechOverlapWithPrimary: 0.80,
		SpeechInPrimarySilence:   0.10,
		FingerprintSimilarity:    0.90,
		PrimarySpeechRatio:       0.30,
	}
	include, reason = classify(metrics, Metadata{}, cfg)
	if !include || reason != "mixed_commentary" {
		t.Fatalf("expected mixed_commentary, got include=%v reason=%q", include, reason)
	}

	metrics = Metrics{
		SpeechRatio:              0.30,
		SpeechOverlapWithPrimary: 0.50,
		SpeechInPrimarySilence:   0.05,
		FingerprintSimilarity:    0.99,
		PrimarySpeechRatio:       0.31,
	}
	include, reason = classify(metrics, Metadata{}, cfg)
	if include || reason != "duplicate_downmix" {
		t.Fatalf("expected duplicate_downmix exclusion, got include=%v reason=%q", include, reason)
	}

	metrics = Metrics{
		SpeechRatio:              0.05,
		SpeechOverlapWithPrimary: 0.05,
		SpeechInPrimarySilence:   0.02,
		FingerprintSimilarity:    0.20,
		PrimarySpeechRatio:       0.25,
	}
	include, reason = classify(metrics, Metadata{}, cfg)
	if include || reason != "music_or_silent" {
		t.Fatalf("expected music_or_silent exclusion, got include=%v reason=%q", include, reason)
	}
}

func defaultCommentaryConfig() config.CommentaryDetection {
	return config.CommentaryDetection{
		FingerprintSimilarityDuplicate: 0.98,
		SpeechRatioMinCommentary:       0.25,
		SpeechRatioMaxMusic:            0.10,
		SpeechOverlapPrimaryMin:        0.60,
		SpeechInSilenceMax:             0.40,
		DurationToleranceSeconds:       120,
		DurationToleranceRatio:         0.02,
	}
}
