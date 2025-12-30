package commentary

import (
	"errors"
	"strings"
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
		SpeechRatio:              0.50,
		SpeechOverlapWithPrimary: 0.10,
		SpeechInPrimarySilence:   0.55,
		FingerprintSimilarity:    0.05,
		PrimarySpeechRatio:       0.35,
	}
	include, reason = classify(metrics, Metadata{}, cfg)
	if include || reason != "audio_description" {
		t.Fatalf("expected audio_description exclusion, got include=%v reason=%q", include, reason)
	}

	metrics = Metrics{
		SpeechRatio:              0.50,
		SpeechOverlapWithPrimary: 0.65,
		SpeechInPrimarySilence:   0.55,
		FingerprintSimilarity:    0.05,
		PrimarySpeechRatio:       0.35,
	}
	include, reason = classify(metrics, Metadata{}, cfg)
	if !include || reason != "commentary_only" {
		t.Fatalf("expected commentary_only inclusion, got include=%v reason=%q", include, reason)
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

func TestBuildFilterUsesGlobalStreamIndex(t *testing.T) {
	filter, label := buildFilter(13, []window{{start: 1.5, duration: 90}})
	if label != "[aout]" {
		t.Fatalf("expected label [aout], got %q", label)
	}
	if want := "[0:13]atrim=start=1.500:duration=90.000,asetpts=PTS-STARTPTS[aout]"; filter != want {
		t.Fatalf("expected filter %q, got %q", want, filter)
	}

	filter, label = buildFilter(7, []window{{start: 10, duration: 5}, {start: 20, duration: 5}})
	if label != "[aout]" {
		t.Fatalf("expected label [aout], got %q", label)
	}
	if want := "[0:7]atrim=start=10.000:duration=5.000,asetpts=PTS-STARTPTS[a0];[0:7]atrim=start=20.000:duration=5.000,asetpts=PTS-STARTPTS[a1];[a0][a1]concat=n=2:v=0:a=1[aout]"; filter != want {
		t.Fatalf("expected filter %q, got %q", want, filter)
	}
}

func TestClassifyFingerprintFailure(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		reason string
		cause  string
	}{
		{
			name:   "stream specifier",
			err:    errors.New("ffmpeg fingerprint extract: exit status 234: stream specifier ':a:13' in filtergraph"),
			reason: "fingerprint_failed_stream_missing",
			cause:  "ffmpeg_stream_missing",
		},
		{
			name:   "decode error",
			err:    errors.New("ffmpeg fingerprint extract: invalid data found when processing input"),
			reason: "fingerprint_failed_decode",
			cause:  "ffmpeg_decode_error",
		},
		{
			name:   "fpcalc error",
			err:    errors.New("fpcalc: exit status 1"),
			reason: "fingerprint_failed_fpcalc",
			cause:  "fpcalc_error",
		},
		{
			name:   "unknown",
			err:    errors.New("ffmpeg fingerprint extract: exit status 1"),
			reason: "fingerprint_failed",
			cause:  "unknown_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := classifyFingerprintFailure(tc.err)
			if failure.Reason != tc.reason {
				t.Fatalf("expected reason %q, got %q", tc.reason, failure.Reason)
			}
			if failure.Cause != tc.cause {
				t.Fatalf("expected cause %q, got %q", tc.cause, failure.Cause)
			}
			if failure.Hint == "" {
				t.Fatalf("expected hint to be set")
			}
			if failure.Attention == "" {
				t.Fatalf("expected attention to be set")
			}
		})
	}
}

func TestFormatDecisionSummaryFingerprintFailure(t *testing.T) {
	decision := Decision{
		Index:   13,
		Include: false,
		Reason:  "fingerprint_failed_stream_missing",
		Metadata: Metadata{
			Language: "eng",
		},
	}
	summary := formatDecisionSummary(decision, decision.Reason)
	if !strings.Contains(summary, "fingerprint_failed_stream_missing") {
		t.Fatalf("expected reason in summary, got %q", summary)
	}
	if !strings.Contains(summary, "Stream not found by ffmpeg; candidate skipped") {
		t.Fatalf("expected stream missing hint, got %q", summary)
	}
}

func defaultCommentaryConfig() config.CommentaryDetection {
	return config.CommentaryDetection{
		FingerprintSimilarityDuplicate: 0.98,
		SpeechRatioMinCommentary:       0.25,
		SpeechRatioMaxMusic:            0.10,
		SpeechOverlapPrimaryMin:        0.60,
		SpeechOverlapPrimaryMaxAD:      0.30,
		SpeechInSilenceMax:             0.40,
		DurationToleranceSeconds:       120,
		DurationToleranceRatio:         0.02,
	}
}
