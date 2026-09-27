// handlertest -- standing concurrency-safety check for VSHIP CVVDP scoring.
//
// Verifies that the linked libvship is safe to run N CVVDP handlers
// concurrently (one per metric worker, the way reel scores probes). It scores
// every chunk of a kept .reel-* workdir two ways and compares them:
//
//	truth:      ONE handler, scored serially -- the unambiguous reference.
//	concurrent: N DISTINCT handlers, scored concurrently, repeated `reps` times.
//
// PASS (exit 0) iff every concurrent rep matches truth within EPS. FAIL (exit 1)
// on any divergence: that means this libvship build races across coexisting
// handlers and corrupts scores. The fix is a libvship built with
// MITIGATE_MALLOC_ASYNC (the default cudaMallocAsync allocator shares a
// device-global pool across handlers); see docs/VSHIP_CONCURRENCY_BUG.md.
//
// Re-run after any libvship / GPU / driver change. A near-floor 4K clip such as
// sullyhv-15m at the UHD worker count is the most sensitive input.
//
// Usage: handlertest <source.mkv> <workdir> [handlers] [reps]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

// EPS separates benign float noise from a real cascade. Correct concurrent
// scoring is byte-identical to truth (delta 0.0000); a cascade diverges by
// 0.5-2.0 JOD, so 0.01 cleanly separates the two.
const EPS = 0.01

type tqChunkLog struct {
	ChunkIdx   int     `json:"chunk_idx"`
	FinalScore float32 `json:"final_score"`
	FinalCRF   float32 `json:"final_crf"`
}

type tqLog struct {
	Target    float32      `json:"target"`
	Tolerance float32      `json:"tolerance"`
	Chunks    []tqChunkLog `json:"chunks"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: handlertest <source.mkv> <workdir> [handlers] [reps]")
		os.Exit(2)
	}
	sourcePath, workDir := os.Args[1], os.Args[2]
	handlers := 0
	if len(os.Args) >= 4 {
		handlers = mustAtoi(os.Args[3])
	}
	reps := 3
	if len(os.Args) >= 5 {
		reps = mustAtoi(os.Args[4])
	}
	runHandlerTest(sourcePath, workDir, handlers, reps, newHandlerScorer)
}

type handlerScorerFactory func(sourcePath, workDir, displayPath string, info *video.Info, crop *video.CropRect, width, height uint32) (func(chunk.Chunk) (float32, error), func(), error)

func newHandlerScorer(sourcePath, workDir, displayPath string, info *video.Info, crop *video.CropRect, width, height uint32) (func(chunk.Chunk) (float32, error), func(), error) {
	proc, err := quality.NewVshipProcessor(width, height, info, displayPath)
	if err != nil {
		return nil, nil, err
	}
	return func(ch chunk.Chunk) (float32, error) {
		return scoreChunk(proc, sourcePath, ch, info, crop, width, height, workDir)
	}, func() { _ = proc.Close() }, nil
}

func runHandlerTest(sourcePath, workDir string, handlers, reps int, newScorer handlerScorerFactory) {
	srcInfo, err := video.Probe(sourcePath)
	fail(err, "probe source")
	manifest, err := readManifest(filepath.Join(workDir, "resume.json"))
	fail(err, "read resume manifest")
	tq, err := readTQLog(filepath.Join(workDir, "target-quality.json"))
	fail(err, "read target-quality.json")

	frames := manifest.Frames
	if frames == 0 {
		frames = srcInfo.Frames
	}
	srcInfo.Frames = frames
	segments, err := chunk.LoadSegments(filepath.Join(workDir, "chunk-plan.txt"), frames)
	fail(err, "load chunk plan")
	chunks := chunk.Chunkify(segments)

	var cropRect *video.CropRect
	if manifest.CropFilter != "" {
		var w, h, x, y uint32
		_, err := fmt.Sscanf(manifest.CropFilter, "crop=%d:%d:%d:%d", &w, &h, &x, &y)
		fail(err, "parse crop filter")
		cropRect = &video.CropRect{X: x, Y: y, Width: w, Height: h}
	}
	width, height := video.OutputDimensions(srcInfo, cropRect)

	displayPath, err := quality.EnsureDisplayModel(workDir, srcInfo, "")
	fail(err, "display model")

	if handlers == 0 {
		handlers = config.DefaultMetricWorkersForWidth(width)
	}
	if handlers > len(chunks) {
		handlers = len(chunks)
	}

	fmt.Printf("Source: %s (%d frames)\nWorkdir: %s\nChunks: %d, output %dx%d, crop %q\nBand: %.2f +/- %.2f JOD | handlers=%d reps=%d EPS=%.3f\n\n",
		sourcePath, frames, workDir, len(chunks), width, height, manifest.CropFilter, tq.Target, tq.Tolerance, handlers, reps, EPS)

	create := func() (func(chunk.Chunk) (float32, error), func(), error) {
		return newScorer(sourcePath, workDir, displayPath, srcInfo, cropRect, width, height)
	}

	// Phase 1: truth -- one handler, serial. Definitionally race-free.
	fmt.Println("Phase 1: single-handler serial truth...")
	score, closeTruth, err := create()
	fail(err, "truth processor")
	truth := make(map[int]float32, len(chunks))
	for i, ch := range chunks {
		s, err := score(ch)
		fail(err, fmt.Sprintf("truth chunk %04d", ch.Idx))
		truth[ch.Idx] = s
		fmt.Printf("\r  scored %d/%d", i+1, len(chunks))
	}
	closeTruth()
	mean, mn, below := stats(chunks, truth, tq)
	fmt.Printf("\r  truth: mean=%.4f min=%.4f below_band=%d\n\n", mean, mn, below)

	// Phase 2: N distinct handlers, concurrent, repeated.
	worstDelta := 0.0
	for rep := 0; rep < reps; rep++ {
		fmt.Printf("Phase 2 rep %d: %d distinct handlers, concurrent...\n", rep+1, handlers)
		conc := concurrentPass(handlers, chunks, create)
		mean, mn, below := stats(chunks, conc, tq)
		maxDelta, worstIdx := maxDeltaVsTruth(chunks, conc, truth)
		if maxDelta > worstDelta {
			worstDelta = maxDelta
		}
		flag := "ok"
		if maxDelta > EPS {
			flag = "DIVERGES"
		}
		fmt.Printf("\r  rep %d: mean=%.4f min=%.4f below_band=%d  maxDelta_vs_truth=%.4f (chunk %04d) [%s]\n",
			rep+1, mean, mn, below, maxDelta, worstIdx, flag)
	}

	fmt.Println()
	if worstDelta > EPS {
		fmt.Printf("FAIL: concurrent scoring diverges from truth by up to %.4f (> EPS %.3f).\n", worstDelta, EPS)
		fmt.Println("This libvship races across coexisting handlers. Rebuild with MITIGATE_MALLOC_ASYNC.")
		os.Exit(1)
	}
	fmt.Printf("PASS: %d concurrent handlers match single-handler truth within %.4f (EPS %.3f) across %d reps.\n",
		handlers, worstDelta, EPS, reps)
}

// concurrentPass scores all chunks with `handlers` DISTINCT handlers, one per
// worker goroutine, with no locking -- exactly how reel scores probes.
func concurrentPass(handlers int, chunks []chunk.Chunk, create func() (func(chunk.Chunk) (float32, error), func(), error)) map[int]float32 {
	scorers := make([]func(chunk.Chunk) (float32, error), handlers)
	for w := 0; w < handlers; w++ {
		score, closeScorer, err := create()
		fail(err, fmt.Sprintf("concurrent processor %d", w))
		defer closeScorer()
		scorers[w] = score
	}

	out := make(map[int]float32, len(chunks))
	var mu sync.Mutex
	var done int
	chunkCh := make(chan chunk.Chunk)
	var wg sync.WaitGroup
	for w := 0; w < handlers; w++ {
		wg.Add(1)
		go func(score func(chunk.Chunk) (float32, error)) {
			defer wg.Done()
			for ch := range chunkCh {
				s, err := score(ch)
				fail(err, fmt.Sprintf("concurrent chunk %04d", ch.Idx))
				mu.Lock()
				out[ch.Idx] = s
				done++
				fmt.Printf("\r  scored %d/%d", done, len(chunks))
				mu.Unlock()
			}
		}(scorers[w])
	}
	for _, ch := range chunks {
		chunkCh <- ch
	}
	close(chunkCh)
	wg.Wait()
	return out
}

// scoreChunk decodes the chunk's source + encoded frames and runs CVVDP on the
// given handler. No locking: each caller passes its own handler.
func scoreChunk(proc *quality.VshipProcessor, sourcePath string, ch chunk.Chunk, srcInfo *video.Info, crop *video.CropRect, width, height uint32, workDir string) (float32, error) {
	src, err := video.Open(sourcePath, 1)
	if err != nil {
		return 0, err
	}
	defer src.Close()
	probePath := chunk.IVFPath(workDir, ch.Idx)
	probeInfo, err := video.Probe(probePath)
	if err != nil {
		return 0, err
	}
	dist, err := video.Open(probePath, 1)
	if err != nil {
		return 0, err
	}
	defer dist.Close()

	frameSize := int(width) * int(height) * 3
	srcBuf := make([]byte, frameSize)
	distBuf := make([]byte, frameSize)
	srcPlanes, err := quality.PlanesFromYUV420P10(srcBuf, width, height)
	if err != nil {
		return 0, err
	}
	distPlanes, err := quality.PlanesFromYUV420P10(distBuf, width, height)
	if err != nil {
		return 0, err
	}
	if err := proc.ResetCVVDP(); err != nil {
		return 0, err
	}
	var score float32
	for i := 0; i < ch.Frames(); i++ {
		if err := src.ReadFrame(ch.Start+i, srcBuf, srcInfo, crop); err != nil {
			return 0, fmt.Errorf("read src frame %d: %w", ch.Start+i, err)
		}
		if err := dist.ReadFrame(i, distBuf, probeInfo, nil); err != nil {
			return 0, fmt.Errorf("read probe frame %d: %w", i, err)
		}
		score, err = proc.ComputeCVVDP(srcPlanes, distPlanes)
		if err != nil {
			return 0, fmt.Errorf("compute frame %d: %w", i, err)
		}
	}
	return score, nil
}

func stats(chunks []chunk.Chunk, scores map[int]float32, tq tqLog) (mean, min float64, below int) {
	var sum float64
	min = 99
	for _, ch := range chunks {
		s := scores[ch.Idx]
		sum += float64(s)
		if float64(s) < min {
			min = float64(s)
		}
		if s < tq.Target-tq.Tolerance {
			below++
		}
	}
	return sum / float64(len(chunks)), min, below
}

func maxDeltaVsTruth(chunks []chunk.Chunk, scores, truth map[int]float32) (maxDelta float64, worstIdx int) {
	for _, ch := range chunks {
		d := abs64(scores[ch.Idx] - truth[ch.Idx])
		if d > maxDelta {
			maxDelta = d
			worstIdx = ch.Idx
		}
	}
	return maxDelta, worstIdx
}

func readManifest(path string) (chunk.ResumeManifest, error) {
	var m chunk.ResumeManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

func readTQLog(path string) (tqLog, error) {
	var t tqLog
	data, err := os.ReadFile(path)
	if err != nil {
		return t, err
	}
	return t, json.Unmarshal(data, &t)
}

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	fail(err, "parse int "+s)
	return n
}

func fail(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "handlertest: %s: %v\n", what, err)
		os.Exit(2)
	}
}

func abs64(v float32) float64 {
	if v < 0 {
		return float64(-v)
	}
	return float64(v)
}
