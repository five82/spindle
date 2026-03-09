package encodingstate

import (
	"fmt"
	"math"
	"strings"
)

// standardRatio pairs a numeric aspect ratio with its display label.
type standardRatio struct {
	ratio float64
	label string
}

// standardRatios lists known cinematic and broadcast aspect ratios.
var standardRatios = []standardRatio{
	{1.333, "1.33:1"},
	{1.778, "1.78:1"},
	{1.850, "1.85:1"},
	{2.000, "2.00:1"},
	{2.200, "2.20:1"},
	{2.350, "2.35:1"},
	{2.390, "2.39:1"},
	{2.400, "2.40:1"},
}

// tolerance is the maximum relative deviation (2%) for ratio matching.
const tolerance = 0.02

// ParseCropFilter extracts width and height from a crop filter string.
// Accepted formats: "crop=W:H:X:Y" or "W:H:X:Y".
func ParseCropFilter(filter string) (width, height int, err error) {
	s := strings.TrimSpace(filter)
	if s == "" {
		return 0, 0, fmt.Errorf("empty crop filter")
	}

	// Strip the "crop=" prefix if present.
	s = strings.TrimPrefix(s, "crop=")

	var w, h, x, y int
	n, scanErr := fmt.Sscanf(s, "%d:%d:%d:%d", &w, &h, &x, &y)
	if scanErr != nil || n != 4 {
		return 0, 0, fmt.Errorf("invalid crop filter format: %q", filter)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("crop dimensions must be positive: %dx%d", w, h)
	}
	return w, h, nil
}

// MatchStandardRatio returns the label of a standard aspect ratio if the given
// ratio is within 2% tolerance of a known value. When multiple standards fall
// within tolerance, the closest match wins. If no standard ratio matches,
// it returns the ratio formatted to two decimal places with a ":1" suffix.
func MatchStandardRatio(ratio float64) string {
	bestLabel := ""
	bestDev := math.MaxFloat64
	for _, sr := range standardRatios {
		dev := math.Abs(ratio-sr.ratio) / sr.ratio
		if dev <= tolerance && dev < bestDev {
			bestDev = dev
			bestLabel = sr.label
		}
	}
	if bestLabel != "" {
		return bestLabel
	}
	return fmt.Sprintf("%.2f:1", ratio)
}
