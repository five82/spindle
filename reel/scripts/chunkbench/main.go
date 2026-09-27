// chunkbench runs shot detection and chunk planning without encoding.
//
// Usage: go run scripts/chunkbench/main.go <video.mkv>
//
// Outputs chunk statistics in ~30-60 seconds for SDR, ~2-4 minutes for 4K HDR.
// Much faster than a full encode since it skips all encoding, CVVDP, and muxing.
package main

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/five82/reel/internal/chunkplan"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/video"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: chunkbench <video.mkv> [frame-scores-out.txt]")
		os.Exit(1)
	}
	inputPath := os.Args[1]
	scoresOut := ""
	if len(os.Args) >= 3 {
		scoresOut = os.Args[2]
	}

	inf, err := video.Probe(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe failed: %v\n", err)
		os.Exit(1)
	}

	cfg := config.NewConfig("", "", "")
	chunkDuration := cfg.TargetQualityChunkDurationForWidth(inf.Width)
	fps := float64(inf.FPSNum) / float64(inf.FPSDen)
	if fps <= 0 {
		fps = 24
	}
	durationSec := 0.0
	if fps > 0 && inf.Frames > 0 {
		durationSec = float64(inf.Frames) / fps
	}

	maxFrames := int(math.Ceil(fps * chunkDuration))
	minFrames := int(math.Ceil(fps * 2))
	// Target-quality mode uses 6s target; CRF mode uses 0 (no target)
	targetFrames := int(math.Ceil(fps * 6))

	opts := chunkplan.Options{
		MaxFrames:    maxFrames,
		MinFrames:    minFrames,
		TargetFrames: targetFrames,
		RetainScores: scoresOut != "",
		Progress: func(current, total int) {
			if total > 0 {
				pct := current * 100 / total
				fmt.Printf("\r  shot detection: %d%% (%d/%d frames)", pct, current, total)
			}
		},
	}

	// A/B hook: REEL_SHOT_DETECT_WORKERS=N overrides the shot-detection worker
	// cap (auto = cores/2, e.g. 8 on a 16-core box) to test specific worker
	// counts. Boundary output is worker-count invariant, so the boundary hash
	// must match across values.
	var workersLabel string
	if v := os.Getenv("REEL_SHOT_DETECT_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.ShotDetectWorkers = n
			workersLabel = strconv.Itoa(n)
		}
	}

	fmt.Printf("File:      %s\n", inputPath)
	fmt.Printf("Duration:  %s\n", formatDuration(durationSec))
	fmt.Printf("Resolution: %dx%d\n", inf.Width, inf.Height)
	fmt.Printf("FPS:       %.2f\n", fps)
	fmt.Printf("Frames:    %d\n", inf.Frames)
	fmt.Printf("Chunking:  max=%ds (%d frames), min=%ds (%d frames), target=%ds (%d frames)\n",
		int(chunkDuration), maxFrames, int(2), minFrames, int(6), targetFrames)
	if workersLabel != "" {
		fmt.Printf("Workers:   %s (shot-detection override)\n", workersLabel)
	}
	fmt.Println()

	start := time.Now()
	result, err := chunkplan.Plan(context.Background(), inputPath, inf, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nplanning failed: %v\n", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	fmt.Printf("\rShot detection complete in %s (workers=%d)\n\n", elapsed.Round(time.Millisecond), result.ShotDetectWorkersUsed)

	if scoresOut != "" {
		if err := writeScores(scoresOut, result.FrameScores); err != nil {
			fmt.Fprintf(os.Stderr, "writing frame scores: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Wrote %d per-frame scores to %s\n\n", len(result.FrameScores), scoresOut)
	}

	printChunkStats(result, fps)
}

// writeScores dumps one per-frame shot-change score per line, for offline
// per-chunk activity correlation against final CRFs.
func writeScores(path string, scores []float64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	for _, s := range scores {
		if _, err := fmt.Fprintf(w, "%.6f\n", s); err != nil {
			return err
		}
	}
	return w.Flush()
}

func printChunkStats(result chunkplan.Result, fps float64) {
	fmt.Printf("=== Chunking Results ===\n")
	fmt.Printf("Total frames:         %d\n", result.Frames)
	fmt.Printf("Boundary hash:        %s\n", boundaryHash(result.Boundaries))
	fmt.Printf("Natural shot cuts:    %d\n", result.NaturalCuts)
	fmt.Printf("Merged short shots:   %d\n", result.MergedShortShots)
	fmt.Printf("Merged weak cuts:     %d\n", result.MergedWeakCuts)
	fmt.Printf("Synthetic splits:     %d\n", result.SyntheticSplits)
	fmt.Printf("Final chunks:         %d\n", len(result.Boundaries))
	fmt.Println()

	if len(result.Boundaries) < 2 {
		return
	}

	var durations []float64
	var frameCounts []int
	for i := 1; i < len(result.Boundaries); i++ {
		frames := result.Boundaries[i] - result.Boundaries[i-1]
		frameCounts = append(frameCounts, frames)
		duration := float64(frames) / fps
		durations = append(durations, duration)
	}
	// Last chunk
	lastFrames := result.Frames - result.Boundaries[len(result.Boundaries)-1]
	frameCounts = append(frameCounts, lastFrames)
	durations = append(durations, float64(lastFrames)/fps)

	fmt.Printf("=== Chunk Durations ===\n")
	fmt.Printf("Count:    %d\n", len(durations))
	fmt.Printf("Total:    %.1fs\n", sumFloat64(durations))
	fmt.Printf("Mean:     %.1fs (%.0f frames)\n", meanFloat64(durations), meanInt(frameCounts))
	fmt.Printf("Min:      %.1fs (%d frames)\n", minFloat64(durations), minInt(frameCounts))
	fmt.Printf("Max:      %.1fs (%d frames)\n", maxFloat64(durations), maxInt(frameCounts))
	fmt.Printf("Under 1s: %d\n", countUnder(durations, 1.0))
	fmt.Printf("Under 2s: %d\n", countUnder(durations, 2.0))
	fmt.Println()

	sorted := make([]float64, len(durations))
	copy(sorted, durations)
	float64Sort(sorted)
	fmt.Printf("Percentiles (seconds):\n")
	fmt.Printf("  p10: %.1f  p25: %.1f  p50: %.1f  p75: %.1f  p90: %.1f\n",
		percentileFloat64(sorted, 0.10),
		percentileFloat64(sorted, 0.25),
		percentileFloat64(sorted, 0.50),
		percentileFloat64(sorted, 0.75),
		percentileFloat64(sorted, 0.90))

	fmt.Println()
	fmt.Printf("=== Boundary Composition ===\n")
	var natural, synthetic int
	for _, kind := range result.BoundaryKinds {
		switch kind {
		case chunkplan.BoundaryKindNaturalShotCut:
			natural++
		case chunkplan.BoundaryKindSyntheticSplit:
			synthetic++
		}
	}
	fmt.Printf("Natural shot cuts:    %d\n", natural)
	fmt.Printf("Synthetic splits:     %d\n", synthetic)
}

func formatDuration(seconds float64) string {
	m := int(seconds) / 60
	s := int(seconds) % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

func sumFloat64(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s
}

func meanFloat64(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return sumFloat64(v) / float64(len(v))
}

func meanInt(v []int) float64 {
	if len(v) == 0 {
		return 0
	}
	var s int
	for _, x := range v {
		s += x
	}
	return float64(s) / float64(len(v))
}

func minFloat64(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxFloat64(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func minInt(v []int) int {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxInt(v []int) int {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

func countUnder(v []float64, threshold float64) int {
	var c int
	for _, x := range v {
		if x < threshold {
			c++
		}
	}
	return c
}

func float64Sort(v []float64) {
	for i := 0; i < len(v); i++ {
		for j := i + 1; j < len(v); j++ {
			if v[j] < v[i] {
				v[i], v[j] = v[j], v[i]
			}
		}
	}
}

func percentileFloat64(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	idx := int(p * float64(len(v)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(v) {
		idx = len(v) - 1
	}
	return v[idx]
}

func boundaryHash(boundaries []int) string {
	h := fnv.New64a()
	for _, b := range boundaries {
		_, _ = fmt.Fprintf(h, "%d,", b)
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
