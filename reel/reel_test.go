package reel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/processing"
	"github.com/five82/reel/internal/reporter"
)

func TestNewOptions(t *testing.T) {
	e, err := New(WithCRF(25), WithDisableAutocrop())
	if err != nil {
		t.Fatal(err)
	}
	if e.config.QualityMode != config.QualityModeCRF || e.config.CRFSD != 25 || e.config.CRFHD != 25 || e.config.CRFUHD != 25 || e.config.CropMode != "none" {
		t.Errorf("unexpected fixed CRF config: %+v", e.config)
	}
	e, err = New(WithCRFByResolution(20, 25.5, 30))
	if err != nil {
		t.Fatal(err)
	}
	if e.config.CRFSD != 20 || e.config.CRFHD != 25.5 || e.config.CRFUHD != 30 {
		t.Errorf("resolution-specific CRFs = %v, %v, %v", e.config.CRFSD, e.config.CRFHD, e.config.CRFUHD)
	}
	display := filepath.Join(t.TempDir(), "display.json")
	if err := os.WriteFile(display, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	e, err = New(WithQualityMode(config.QualityModeTarget), WithTargetQuality("9.3-9.7"), WithCVVDPDisplay(display))
	if err != nil {
		t.Fatal(err)
	}
	if e.config.TargetQuality != "9.3-9.7" || e.config.CVVDPDisplay != display {
		t.Errorf("unexpected target config: %+v", e.config)
	}
	if _, err := New(WithCRF(99)); err == nil {
		t.Error("invalid CRF accepted")
	}
}

func TestEventReporter(t *testing.T) {
	var events []Event
	r := newEventReporter(func(e Event) error {
		events = append(events, e)
		return nil
	})
	r.EncodingProgress(reporter.ProgressSnapshot{Percent: 25, Speed: 2, RecentSpeed: 3, FPS: 24, ETA: 90 * time.Second})
	r.ValidationComplete(reporter.ValidationSummary{Passed: false, Steps: []reporter.ValidationStep{{Name: "sync", Passed: true, Details: "ok"}, {Name: "video", Details: "bad"}}})
	r.EncodingComplete(reporter.EncodingOutcome{OutputFile: "out.mkv", OriginalSize: 100, EncodedSize: 50, VideoOriginalSize: 80, VideoEncodedSize: 20})
	r.Warning("careful")
	r.Error(reporter.ReporterError{Title: "failed", Message: "reason", Context: "file", Suggestion: "retry"})
	r.BatchComplete(reporter.BatchSummary{SuccessfulCount: 1, TotalFiles: 2, TotalOriginalSize: 100, TotalEncodedSize: 50})
	wantTypes := []string{EventTypeEncodingProgress, EventTypeValidationComplete, EventTypeEncodingComplete, EventTypeWarning, EventTypeError, EventTypeBatchComplete}
	if len(events) != len(wantTypes) {
		t.Fatalf("got %d events, want %d", len(events), len(wantTypes))
	}
	for i, e := range events {
		if e.Type() != wantTypes[i] || e.Timestamp() < time.Now().Add(-time.Minute).Unix() {
			t.Errorf("event %d type/timestamp = %q/%d", i, e.Type(), e.Timestamp())
		}
	}
	if p := events[0].(EncodingProgressEvent); p.Percent != 25 || p.RecentSpeed != 3 || p.ETASeconds != 90 {
		t.Errorf("progress = %+v", p)
	}
	if v := events[1].(ValidationCompleteEvent); v.ValidationPassed || len(v.ValidationSteps) != 2 || v.ValidationSteps[1].Step != "video" || v.ValidationSteps[1].Details != "bad" {
		t.Errorf("validation = %+v", v)
	}
	if e := events[2].(EncodingCompleteEvent); e.SizeReductionPercent != 50 || e.VideoSizeReductionPercent != 75 || e.OutputFile != "out.mkv" {
		t.Errorf("encoding = %+v", e)
	}
	if w := events[3].(WarningEvent); w.Message != "careful" {
		t.Errorf("warning = %+v", w)
	}
	if e := events[4].(ErrorEvent); e.Title != "failed" || e.Context != "file" || e.Suggestion != "retry" {
		t.Errorf("error = %+v", e)
	}
	if b := events[5].(BatchCompleteEvent); b.SuccessfulCount != 1 || b.TotalFiles != 2 || b.TotalSizeReductionPercent != 50 {
		t.Errorf("batch = %+v", b)
	}
}

func TestResultFromEncodeResult(t *testing.T) {
	r := resultFromEncodeResult("out.mkv", processing.EncodeResult{InputSize: 100, OutputSize: 50, InputVideoSize: 80, OutputVideoSize: 20, ValidationPassed: true, EncodingSpeed: 2})
	if r.OutputFile != "out.mkv" || r.SizeReductionPercent != 50 || r.VideoSizeReductionPercent != 75 || !r.ValidationPassed || r.EncodingSpeed != 2 {
		t.Errorf("result = %+v", r)
	}
}
