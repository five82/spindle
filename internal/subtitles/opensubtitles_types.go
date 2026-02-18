package subtitles

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// OpenSubtitles rate limiting configuration.
const (
	openSubtitlesMinInterval    = time.Second
	openSubtitlesMaxRateRetries = 4
	openSubtitlesInitialBackoff = 2 * time.Second
	openSubtitlesMaxBackoff     = 12 * time.Second
)

type (
	durationMismatchError struct {
		deltaSeconds float64
		videoSeconds float64
		release      string
	}

	// earlyDurationRejectError indicates a candidate was rejected during
	// early duration pre-check (before expensive alignment).
	earlyDurationRejectError struct {
		deltaSeconds float64
		release      string
	}

	suspectMisIdentificationError struct {
		deltas []float64
	}

	// alignmentQualityError indicates alignment produced incoherent timing.
	// Returned when shift analysis or output integrity checks detect that an
	// alignment tool produced garbage rather than improving timing.
	alignmentQualityError struct {
		reason  string
		release string
		metrics alignmentQualityMetrics
	}
)

func (e durationMismatchError) Error() string {
	return fmt.Sprintf("subtitle duration delta %.1fs exceeds tolerance", e.deltaSeconds)
}

func (e earlyDurationRejectError) Error() string {
	return fmt.Sprintf("subtitle rejected early: duration delta %.1fs exceeds pre-check tolerance", e.deltaSeconds)
}

func (e alignmentQualityError) Error() string {
	return fmt.Sprintf("alignment quality failed: %s", e.reason)
}

func (e suspectMisIdentificationError) Error() string {
	return "opensubtitles candidates suggest mis-identification (large consistent offset)"
}

func (e suspectMisIdentificationError) medianAbsDelta() float64 {
	if len(e.deltas) == 0 {
		return 0
	}
	values := append([]float64(nil), e.deltas...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	mid := len(values) / 2
	median := math.Abs(values[mid])
	if len(values)%2 == 0 {
		median = (math.Abs(values[mid-1]) + math.Abs(values[mid])) / 2
	}
	return median
}
