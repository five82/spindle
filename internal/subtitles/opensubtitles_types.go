package subtitles

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	openSubtitlesMinInterval         = time.Second
	openSubtitlesMaxRateRetries      = 4
	openSubtitlesInitialBackoff      = 2 * time.Second
	openSubtitlesMaxBackoff          = 12 * time.Second
	subtitleIntroAllowanceSeconds    = 45.0
	subtitleIntroMinimumSeconds      = 5.0
	suspectOffsetSeconds             = 60.0
	suspectRuntimeMismatchRatio      = 0.07
	subtitleDurationToleranceSeconds = 8.0
)

type (
	durationMismatchError struct {
		deltaSeconds float64
		videoSeconds float64
		release      string
	}

	suspectMisIdentificationError struct {
		deltas []float64
	}
)

func (e durationMismatchError) Error() string {
	return fmt.Sprintf("subtitle duration delta %.1fs exceeds tolerance", e.deltaSeconds)
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
