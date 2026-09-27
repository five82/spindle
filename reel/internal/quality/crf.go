// Package quality contains target-quality search and CVVDP helpers.
package quality

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	MinSVTCRF float32 = 1.0
	MaxSVTCRF float32 = 70.0

	DefaultSearchMin float32 = 4.25
	DefaultSearchMax float32 = 63.75
)

// RoundCRFToQuarter rounds a CRF to SVT-AV1's quarter-step grid.
func RoundCRFToQuarter(crf float32) float32 {
	return float32(math.Round(float64(crf*4))) / 4
}

// IsQuarterCRF reports whether crf is on SVT-AV1's quarter-step grid.
func IsQuarterCRF(crf float32) bool {
	return math.Abs(float64(crf-RoundCRFToQuarter(crf))) < 1e-5
}

// ValidateCRF checks a fixed CRF against SVT-AV1 git HEAD's supported range.
func ValidateCRF(crf float32) error {
	if crf < MinSVTCRF || crf > MaxSVTCRF {
		return fmt.Errorf("CRF must be %.0f-%.0f, got %s", MinSVTCRF, MaxSVTCRF, FormatCRF(crf))
	}
	if !IsQuarterCRF(crf) {
		return fmt.Errorf("CRF must be in 0.25 increments, got %s", FormatCRF(crf))
	}
	return nil
}

// FormatCRF formats a CRF exactly enough for SVT-AV1 quarter-step values.
func FormatCRF(crf float32) string {
	crf = RoundCRFToQuarter(crf)
	if math.Abs(float64(crf-float32(int(crf)))) < 1e-5 {
		return strconv.Itoa(int(crf))
	}
	if math.Abs(float64(crf*2-float32(int(crf*2)))) < 1e-5 {
		return fmt.Sprintf("%.1f", crf)
	}
	return fmt.Sprintf("%.2f", crf)
}

// ParseCRF parses and validates a fixed CRF value.
func ParseCRF(s string) (float32, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(s), 32)
	if err != nil {
		return 0, err
	}
	crf := float32(value)
	if err := ValidateCRF(crf); err != nil {
		return 0, err
	}
	return crf, nil
}

// ParseRange parses LOW-HIGH into two floats.
func ParseRange(s, name string) (float32, float32, error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%s must be LOW-HIGH, got %q", name, s)
	}
	low, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid %s low value %q: %w", name, parts[0], err)
	}
	high, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid %s high value %q: %w", name, parts[1], err)
	}
	return float32(low), float32(high), nil
}

// ParseTargetQualityRange parses a CVVDP JOD target range.
func ParseTargetQualityRange(s string) (low, high, target, tolerance float32, err error) {
	low, high, err = ParseRange(s, "target quality")
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if low <= 0 || high > 10 || low > high {
		return 0, 0, 0, 0, fmt.Errorf("target quality must satisfy 0 < low <= high <= 10, got %s", s)
	}
	target = (low + high) / 2
	tolerance = (high - low) / 2
	return low, high, target, tolerance, nil
}

// ParseCRFSearchRange parses and validates a target-quality CRF search range.
func ParseCRFSearchRange(s string) (float32, float32, error) {
	low, high, err := ParseRange(s, "CRF search range")
	if err != nil {
		return 0, 0, err
	}
	if low < DefaultSearchMin || high > DefaultSearchMax || low > high {
		return 0, 0, fmt.Errorf("CRF search range must satisfy %.2f <= low <= high <= %.2f, got %s", DefaultSearchMin, DefaultSearchMax, s)
	}
	if !IsQuarterCRF(low) || !IsQuarterCRF(high) {
		return 0, 0, fmt.Errorf("CRF search range bounds must be in 0.25 increments, got %s", s)
	}
	return RoundCRFToQuarter(low), RoundCRFToQuarter(high), nil
}
