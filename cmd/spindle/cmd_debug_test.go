package main

import (
	"testing"

	"github.com/five82/spindle/internal/media/ffprobe"
)

func TestAnalyzeDebugCropCountsSelectsMostCommonCrop(t *testing.T) {
	result := analyzeDebugCropCounts(map[string]int{
		"1920:800:0:140": 3,
		"1920:804:0:138": 1,
	}, 1920, 1080)

	if !result.Required {
		t.Fatal("expected crop to be required")
	}
	if result.CropFilter != "crop=1920:800:0:140" {
		t.Fatalf("CropFilter = %q", result.CropFilter)
	}
	if !result.MultipleRatios {
		t.Fatal("expected multiple ratios")
	}
	if result.TotalSamples != 4 {
		t.Fatalf("TotalSamples = %d, want 4", result.TotalSamples)
	}
}

func TestDebugStreamIsHDR(t *testing.T) {
	if !debugStreamIsHDR(ffprobe.Stream{ColorTransfer: "smpte2084"}) {
		t.Fatal("smpte2084 should be HDR")
	}
	if debugStreamIsHDR(ffprobe.Stream{ColorPrimaries: "bt709", ColorTransfer: "bt709"}) {
		t.Fatal("bt709 should not be HDR")
	}
}
