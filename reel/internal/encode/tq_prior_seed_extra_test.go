package encode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/five82/reel/internal/quality"
)

func TestSeedTargetQualityPriorSkipsBadLogsAndRestoresCompletedChunks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tq"), 0700); err != nil {
		t.Fatal(err)
	}
	saved := chunkTargetLog{ChunkIdx: 3, FinalCRF: 28, Metric: string(quality.MetricCVVDP), Probes: []quality.Probe{{CRF: 28, Score: 9}}}
	if err := writeChunkTargetLog(dir, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tq", "0004.json"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, metric := range []quality.MetricKind{quality.MetricCVVDP, quality.MetricSSIMU2} {
		prior := newTargetQualityPrior(30, 10, 50, 80, 0, metric)
		seedTargetQualityPrior(dir, map[int]bool{3: true, 4: true, 5: true}, prior, metric, nil)
		if prior.Count() != 1 {
			t.Fatalf("%s: restored %d chunks", metric, prior.Count())
		}
		if got, _ := prior.InitialCRF(3); got < 10 || got > 50 {
			t.Fatalf("%s: CRF=%v", metric, got)
		}
	}
}
