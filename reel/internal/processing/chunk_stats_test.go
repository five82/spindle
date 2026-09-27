package processing

import (
	"strings"
	"testing"

	"github.com/five82/reel/internal/chunk"
)

func TestChunkDistributionSummary(t *testing.T) {
	chunks := []chunk.Chunk{
		{Start: 0, End: 24},
		{Start: 24, End: 72},
		{Start: 72, End: 312},
	}
	got := chunkDistributionSummary(chunks, 24)
	for _, want := range []string{"min 24", "p50 48", "max 240", "under 1s: 0", "under 2s: 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("chunkDistributionSummary() = %q, missing %q", got, want)
		}
	}
}
