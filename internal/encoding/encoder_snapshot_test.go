package encoding

import (
	"testing"
	"time"

	"spindle/internal/encodingstate"
	"spindle/internal/services/drapto"
)

func TestApplyDraptoUpdateProgress(t *testing.T) {
	var snapshot encodingstate.Snapshot
	update := drapto.ProgressUpdate{
		Type:         drapto.EventTypeEncodingProgress,
		Stage:        "encoding",
		Percent:      42.5,
		ETA:          5 * time.Minute,
		Speed:        2.5,
		FPS:          96.0,
		Bitrate:      "3200kbps",
		TotalFrames:  1000,
		CurrentFrame: 425,
	}
	if !applyDraptoUpdate(&snapshot, update, "Encoding 42% (ETA 5m)") {
		t.Fatal("expected progress snapshot to change")
	}
	if snapshot.Stage != "encoding" {
		t.Fatalf("expected stage 'encoding', got %q", snapshot.Stage)
	}
	if snapshot.Percent != 42.5 {
		t.Fatalf("expected percent 42.5, got %f", snapshot.Percent)
	}
	if snapshot.ETASeconds != (5 * time.Minute).Seconds() {
		t.Fatalf("expected eta seconds %f", snapshot.ETASeconds)
	}
	if snapshot.Speed != 2.5 {
		t.Fatalf("expected speed 2.5, got %f", snapshot.Speed)
	}
	if snapshot.FPS != 96.0 {
		t.Fatalf("expected fps 96, got %f", snapshot.FPS)
	}
	if snapshot.Bitrate != "3200kbps" {
		t.Fatalf("unexpected bitrate %q", snapshot.Bitrate)
	}
	if snapshot.TotalFrames != 1000 || snapshot.CurrentFrame != 425 {
		t.Fatalf("unexpected frame counters %+v", snapshot)
	}
	if snapshot.Message == "" {
		t.Fatal("expected snapshot message populated")
	}
}

func TestApplyDraptoUpdateMetadata(t *testing.T) {
	var snapshot encodingstate.Snapshot

	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type:     drapto.EventTypeHardware,
		Hardware: &drapto.HardwareInfo{Hostname: "encoder-a"},
	}, "") {
		t.Fatal("expected hardware snapshot to change")
	}
	if snapshot.Hardware == nil || snapshot.Hardware.Hostname != "encoder-a" {
		t.Fatalf("unexpected hardware snapshot: %+v", snapshot.Hardware)
	}

	video := &drapto.VideoInfo{
		InputFile:        "/tmp/source.mkv",
		OutputFile:       "source.mkv",
		Duration:         "00:42:00",
		Resolution:       "1920x1080",
		Category:         "HD",
		DynamicRange:     "SDR",
		AudioDescription: "5.1",
	}
	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type:  drapto.EventTypeInitialization,
		Video: video,
	}, "") {
		t.Fatal("expected video snapshot to change")
	}
	if snapshot.Video == nil || snapshot.Video.InputFile != "/tmp/source.mkv" {
		t.Fatalf("unexpected video snapshot: %+v", snapshot.Video)
	}

	cfg := &drapto.EncodingConfig{
		Encoder:      "SVT-AV1",
		Preset:       "6",
		Tune:         "0",
		Quality:      "CRF 26",
		PixelFormat:  "yuv420p10le",
		AudioCodec:   "Opus",
		DraptoPreset: "Balanced",
		PresetSettings: []drapto.PresetSetting{
			{Key: "CRF", Value: "26"},
		},
		SVTParams: "hierarchical_levels=4",
	}
	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type:           drapto.EventTypeEncodingConfig,
		EncodingConfig: cfg,
	}, "") {
		t.Fatal("expected config snapshot change")
	}
	if snapshot.Config == nil || snapshot.Config.Encoder != "SVT-AV1" {
		t.Fatalf("unexpected config snapshot: %+v", snapshot.Config)
	}

	validation := &drapto.ValidationSummary{
		Passed: true,
		Steps: []drapto.ValidationStep{
			{Name: "Mux", Passed: true, Details: "ok"},
		},
	}
	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type:       drapto.EventTypeValidation,
		Validation: validation,
	}, "") {
		t.Fatal("expected validation snapshot change")
	}
	if snapshot.Validation == nil || len(snapshot.Validation.Steps) != 1 {
		t.Fatalf("unexpected validation snapshot: %+v", snapshot.Validation)
	}

	result := &drapto.EncodingResult{
		InputFile:            "/tmp/source.mkv",
		OutputFile:           "encoded.mkv",
		OutputPath:           "/tmp/encoded/encoded.mkv",
		OriginalSize:         10_000_000,
		EncodedSize:          4_000_000,
		VideoStream:          "AV1",
		AudioStream:          "Opus",
		AverageSpeed:         1.8,
		Duration:             90 * time.Second,
		SizeReductionPercent: 60,
	}
	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type:   drapto.EventTypeEncodingComplete,
		Result: result,
	}, "") {
		t.Fatal("expected result snapshot change")
	}
	if snapshot.Result == nil || snapshot.Result.OutputFile != "encoded.mkv" {
		t.Fatalf("unexpected result snapshot: %+v", snapshot.Result)
	}

	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type:    drapto.EventTypeWarning,
		Warning: "low bitrate",
	}, "") {
		t.Fatal("expected warning snapshot change")
	}
	if snapshot.Warning != "low bitrate" {
		t.Fatalf("unexpected warning snapshot: %+v", snapshot.Warning)
	}

	if !applyDraptoUpdate(&snapshot, drapto.ProgressUpdate{
		Type: drapto.EventTypeError,
		Error: &drapto.ReporterIssue{
			Title:   "Mux failure",
			Message: "segment missing",
		},
	}, "") {
		t.Fatal("expected error snapshot change")
	}
	if snapshot.Error == nil || snapshot.Error.Title != "Mux failure" {
		t.Fatalf("unexpected error snapshot: %+v", snapshot.Error)
	}
}
