package encodingstate

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseCropFilter extracts width and height from "crop=W:H:X:Y" or "W:H:X:Y".
func ParseCropFilter(filter string) (width, height int, ok bool) {
	s := strings.TrimSpace(filter)
	if s == "" {
		return 0, 0, false
	}
	// Strip optional "crop=" prefix.
	s = strings.TrimPrefix(s, "crop=")
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return 0, 0, false
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return w, h, true
}

// MatchStandardRatio returns a human-readable name for the closest standard
// aspect ratio within 2% tolerance, or a numeric label like "1.78:1".
func MatchStandardRatio(ratio float64) string {
	type standard struct {
		name  string
		value float64
	}
	standards := []standard{
		{"4:3", 4.0 / 3.0},
		{"16:9", 16.0 / 9.0},
		{"1.85:1", 1.85},
		{"2.00:1", 2.00},
		{"2.20:1", 2.20},
		{"2.35:1", 2.35},
		{"2.39:1", 2.39},
		{"2.40:1", 2.40},
	}

	bestName := ""
	bestDist := math.MaxFloat64
	for _, s := range standards {
		dist := math.Abs(ratio - s.value)
		if dist < bestDist {
			bestDist = dist
			bestName = s.name
		}
	}

	// Accept closest match only if within 2% of that standard.
	if bestName != "" && bestDist/ratio <= 0.02 {
		return bestName
	}

	return fmt.Sprintf("%.2f:1", ratio)
}
