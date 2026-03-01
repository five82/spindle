package api

import (
	"fmt"
	"strconv"
	"strings"

	draptolib "github.com/five82/drapto"
)

const cropAutoApplyThresholdPercent = 80.0

type CropCandidateInsight struct {
	Crop        string
	Count       int
	Percent     float64
	Dimensions  string
	IsPreferred bool
}

type CropDetectionInsight struct {
	DynamicRange           string
	Threshold              int
	OutputDimensions       string
	OutputAspectRatio      string
	MultipleRatioThreshold float64
	TopCandidatePercent    float64
	VariationSuggestion    string
	Candidates             []CropCandidateInsight
}

func BuildCropDetectionInsight(result *draptolib.CropDetectionResult) CropDetectionInsight {
	insight := CropDetectionInsight{
		DynamicRange:           "SDR",
		Threshold:              16,
		MultipleRatioThreshold: cropAutoApplyThresholdPercent,
	}
	if result == nil {
		return insight
	}
	if result.IsHDR {
		insight.DynamicRange = "HDR"
		insight.Threshold = 100
	}

	if result.Required {
		if w, h, ok := parseCropFilterDimensions(result.CropFilter); ok && h > 0 {
			removedHeight := uint64(result.VideoHeight) - h
			aspectRatio := float64(w) / float64(h)
			insight.OutputDimensions = fmt.Sprintf("%dx%d (removing %d pixels)", w, h, removedHeight)
			insight.OutputAspectRatio = fmt.Sprintf("%.3f:1", aspectRatio)
		}
	}

	insight.Candidates = make([]CropCandidateInsight, 0, len(result.Candidates))
	for i, candidate := range result.Candidates {
		candidateInsight := CropCandidateInsight{
			Crop:        candidate.Crop,
			Count:       candidate.Count,
			Percent:     candidate.Percent,
			IsPreferred: i == 0 && result.Required,
		}
		if w, h, ok := parseCropValueDimensions(candidate.Crop); ok && h > 0 {
			aspectRatio := float64(w) / float64(h)
			candidateInsight.Dimensions = fmt.Sprintf(" -> %dx%d (%.3f:1)", w, h, aspectRatio)
		}
		insight.Candidates = append(insight.Candidates, candidateInsight)
	}

	if result.MultipleRatios && len(result.Candidates) > 0 {
		topPercent := result.Candidates[0].Percent
		insight.TopCandidatePercent = topPercent
		switch {
		case topPercent >= 70:
			insight.VariationSuggestion = "This is borderline - the film may have minor aspect ratio variations"
		case topPercent >= 50:
			insight.VariationSuggestion = "Significant aspect ratio variation detected - may be intentional (e.g., IMAX sequences)"
		default:
			insight.VariationSuggestion = "High variation in detected crops - check if HDR threshold is appropriate"
		}
	}

	return insight
}

func parseCropFilterDimensions(filter string) (uint64, uint64, bool) {
	return parseCropValueDimensions(strings.TrimPrefix(strings.TrimSpace(filter), "crop="))
}

func parseCropValueDimensions(value string) (uint64, uint64, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 {
		return 0, 0, false
	}
	w, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, false
	}
	h, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, 0, false
	}
	return w, h, true
}
