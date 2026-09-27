package processing

import (
	"fmt"
	"sort"

	"github.com/five82/reel/internal/chunk"
)

func chunkDistributionSummary(chunks []chunk.Chunk, fps float64) string {
	if len(chunks) == 0 || fps <= 0 {
		return "Chunk frames: none"
	}

	frames := make([]int, len(chunks))
	underOneSecond := 0
	underTwoSeconds := 0
	for i, ch := range chunks {
		count := ch.Frames()
		frames[i] = count
		duration := float64(count) / fps
		if duration < 1 {
			underOneSecond++
		}
		if duration < 2 {
			underTwoSeconds++
		}
	}
	sort.Ints(frames)

	return fmt.Sprintf(
		"Chunk frames: min %d, p10 %d, p50 %d, p90 %d, max %d; under 1s: %d, under 2s: %d",
		frames[0],
		percentileInt(frames, 0.10),
		percentileInt(frames, 0.50),
		percentileInt(frames, 0.90),
		frames[len(frames)-1],
		underOneSecond,
		underTwoSeconds,
	)
}

func percentileInt(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
