package api

import (
	"testing"

	draptolib "github.com/five82/drapto"
)

func TestBuildCropDetectionInsight(t *testing.T) {
	result := &draptolib.CropDetectionResult{
		VideoWidth:     3840,
		VideoHeight:    2160,
		IsHDR:          true,
		Required:       true,
		CropFilter:     "crop=3840:1600:0:280",
		MultipleRatios: true,
		TotalSamples:   141,
		Message:        "Multiple aspect ratios detected",
		Candidates: []draptolib.CropCandidate{
			{Crop: "3840:1600:0:280", Count: 92, Percent: 65.2},
			{Crop: "3840:1634:0:263", Count: 49, Percent: 34.8},
		},
	}

	insight := BuildCropDetectionInsight(result)
	if insight.DynamicRange != "HDR" {
		t.Fatalf("DynamicRange = %q, want HDR", insight.DynamicRange)
	}
	if insight.Threshold != 100 {
		t.Fatalf("Threshold = %d, want 100", insight.Threshold)
	}
	if insight.OutputDimensions == "" {
		t.Fatal("OutputDimensions should not be empty")
	}
	if insight.OutputAspectRatio == "" {
		t.Fatal("OutputAspectRatio should not be empty")
	}
	if insight.TopCandidatePercent != 65.2 {
		t.Fatalf("TopCandidatePercent = %.1f, want 65.2", insight.TopCandidatePercent)
	}
	if insight.VariationSuggestion == "" {
		t.Fatal("VariationSuggestion should not be empty")
	}
	if len(insight.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(insight.Candidates))
	}
	if !insight.Candidates[0].IsPreferred {
		t.Fatal("first candidate should be preferred")
	}
}
