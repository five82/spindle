// fullvalidate scores a finished Reel encode against its source with
// full-chunk CVVDP, giving ground-truth per-chunk JOD instead of the recorded
// scores the target-quality search reports about itself.
//
// Usage: fullvalidate <source.mkv> <workdir>
//
// The workdir is a kept .reel-* directory from the encode (--keep-workdir).
// Each chunk is scored from its standalone encode/NNNN.ivf (read from frame 0,
// no seek). Those per-chunk IVFs losslessly concatenate into the final muxed
// output, so this measures the same bits as the final file but avoids seeking
// into a muxed AV1, which mis-aligns frames and reports spurious low scores.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

type tqChunkLog struct {
	ChunkIdx   int               `json:"chunk_idx"`
	Metric     string            `json:"metric"`
	FinalScore float32           `json:"final_score"`
	FinalCRF   float32           `json:"final_crf"`
	Probes     []json.RawMessage `json:"probes"`
}

type tqLog struct {
	Target    float32      `json:"target"`
	Tolerance float32      `json:"tolerance"`
	Chunks    []tqChunkLog `json:"chunks"`
}

type chunkResult struct {
	idx      int
	frames   int
	full     float32
	metric   string
	recorded float32
	probes   int
	crf      float32
	err      error
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "Usage: fullvalidate <source.mkv> <workdir>")
		os.Exit(1)
	}
	runValidation(os.Args[1], os.Args[2], newChunkScorer)
}

type chunkScorerFactory func(sourcePath, workDir, displayPath string, info *video.Info, crop *video.CropRect, width, height uint32) (func(chunk.Chunk) (float32, error), func(), error)

func newChunkScorer(sourcePath, workDir, displayPath string, info *video.Info, crop *video.CropRect, width, height uint32) (func(chunk.Chunk) (float32, error), func(), error) {
	proc, err := quality.NewVshipProcessor(width, height, info, displayPath)
	if err != nil {
		return nil, nil, err
	}
	return func(ch chunk.Chunk) (float32, error) {
		res, err := quality.ComputeChunkCVVDP(context.Background(), quality.CVVDPOptions{
			SourcePath: sourcePath, ProbePath: chunk.IVFPath(workDir, ch.Idx), Info: info,
			Chunk: ch, CropRect: crop, Width: width, Height: height, Processor: proc,
		})
		return res.Score, err
	}, func() { _ = proc.Close() }, nil
}

func runValidation(sourcePath, workDir string, factory chunkScorerFactory) {
	srcInfo, err := video.Probe(sourcePath)
	fail(err, "probe source")
	manifest, err := readManifest(filepath.Join(workDir, "resume.json"))
	fail(err, "read resume manifest")
	_, _, target, tolerance, err := quality.ParseTargetQualityRange(manifest.TargetQuality)
	fail(err, "parse manifest target quality")

	tq, err := readTQLog(filepath.Join(workDir, "target-quality.json"))
	fail(err, "read target-quality.json")
	recorded := make(map[int]tqChunkLog, len(tq.Chunks))
	for _, c := range tq.Chunks {
		recorded[c.ChunkIdx] = c
	}

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

	workers := config.DefaultMetricWorkersForWidth(width)
	if workers > len(chunks) {
		workers = len(chunks)
	}

	fmt.Printf("Source: %s (%d frames)\nWorkdir: %s\nChunks: %d, output %dx%d, crop %q\nTarget: %.2f +/- %.2f JOD, metric workers %d\n\n",
		sourcePath, frames, workDir, len(chunks), width, height, manifest.CropFilter, target, tolerance, workers)

	start := time.Now()
	chunkCh := make(chan chunk.Chunk)
	resultCh := make(chan chunkResult, len(chunks))
	// One VSHIP handler per worker, scored concurrently. CVVDP is per-handler
	// temporal state, so each worker owns a distinct handler. Requires a libvship
	// built with MITIGATE_MALLOC_ASYNC -- the default cudaMallocAsync allocator
	// races across coexisting handlers and corrupts scores. See
	// docs/VSHIP_CONCURRENCY_BUG.md.
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			score, closeScorer, err := factory(sourcePath, workDir, displayPath, srcInfo, cropRect, width, height)
			fail(err, "processor")
			defer closeScorer()
			for ch := range chunkCh {
				// Each scorer reads a standalone IVF from frame zero, avoiding
				// seek-induced misalignment in the final muxed AV1.
				full, err := score(ch)
				s := recorded[ch.Idx]
				resultCh <- chunkResult{idx: ch.Idx, frames: ch.Frames(), full: full, metric: s.Metric, recorded: s.FinalScore, probes: len(s.Probes), crf: s.FinalCRF, err: err}
			}
		}()
	}
	go func() {
		for _, ch := range chunks {
			chunkCh <- ch
		}
		close(chunkCh)
	}()
	go func() { wg.Wait(); close(resultCh) }()

	results := make([]chunkResult, 0, len(chunks))
	for r := range resultCh {
		if r.err != nil {
			fail(r.err, fmt.Sprintf("score chunk %04d", r.idx))
		}
		results = append(results, r)
		fmt.Printf("\rScored %d/%d chunks", len(results), len(chunks))
	}
	fmt.Printf("\rScored %d chunks in %s\n\n", len(results), time.Since(start).Round(time.Second))
	sort.Slice(results, func(i, j int) bool { return results[i].idx < results[j].idx })

	if dump := os.Getenv("FULLVALIDATE_JSON"); dump != "" {
		type chunkOut struct {
			Idx           int     `json:"chunk_idx"`
			Frames        int     `json:"frames"`
			Full          float32 `json:"full_jod"`
			Metric        string  `json:"search_metric"`
			RecordedScore float32 `json:"recorded_score"`
			Probes        int     `json:"probes"`
			CRF           float32 `json:"crf"`
		}
		type out struct {
			Target    float32    `json:"target"`
			Tolerance float32    `json:"tolerance"`
			Chunks    []chunkOut `json:"chunks"`
		}
		chunksOut := make([]chunkOut, len(results))
		for i, r := range results {
			chunksOut[i] = chunkOut{Idx: r.idx, Frames: r.frames, Full: r.full, Metric: r.metric, RecordedScore: r.recorded, Probes: r.probes, CRF: r.crf}
		}
		b, err := json.MarshalIndent(out{Target: target, Tolerance: tolerance, Chunks: chunksOut}, "", "  ")
		fail(err, "marshal fullvalidate json")
		fail(os.WriteFile(dump, b, 0o644), "write fullvalidate json")
	}

	report(results, target, tolerance)
}

func report(results []chunkResult, target, tolerance float32) {
	var fullErr, recordedGap float64
	var below, above, recordedCount int
	fulls := make([]float64, 0, len(results))
	for _, r := range results {
		fullErr += abs64(r.full - target)
		if r.metric == string(quality.MetricCVVDP) {
			recordedGap += abs64(r.full - r.recorded)
			recordedCount++
		}
		fulls = append(fulls, float64(r.full))
		if r.full < target-tolerance {
			below++
		}
		if r.full > target+tolerance {
			above++
		}
	}
	sort.Float64s(fulls)
	n := float64(len(results))

	fmt.Printf("=== Ground truth (full-chunk CVVDP of final output) ===\n")
	fmt.Printf("Full JOD:     min=%.4f p10=%.4f median=%.4f mean=%.4f max=%.4f\n",
		fulls[0], pct(fulls, 0.10), pct(fulls, 0.50), mean(fulls), fulls[len(fulls)-1])
	fmt.Printf("vs target:    mean_abs_error=%.4f below_range=%d above_range=%d of %d\n", fullErr/n, below, above, len(results))
	if recordedCount > 0 {
		fmt.Printf("vs recorded:  mean_abs_gap=%.4f over %d CVVDP-searched chunks (~0 with whole-chunk reuse)\n\n", recordedGap/float64(recordedCount), recordedCount)
	}

	sort.Slice(results, func(i, j int) bool { return results[i].full < results[j].full })
	fmt.Printf("Worst chunks by full JOD:\n")
	fmt.Printf("  chunk  frames  crf     full    search metric  recorded  gap      probes\n")
	for i := 0; i < len(results) && i < 10; i++ {
		r := results[i]
		if r.metric == string(quality.MetricCVVDP) {
			fmt.Printf("  %04d   %5d   %5.2f  %.4f  %-13s  %.4f   %+.4f  %d\n",
				r.idx, r.frames, r.crf, r.full, r.metric, r.recorded, float64(r.full-r.recorded), r.probes)
			continue
		}
		fmt.Printf("  %04d   %5d   %5.2f  %.4f  %-13s  %.4f        n/a  %d\n",
			r.idx, r.frames, r.crf, r.full, r.metric, r.recorded, r.probes)
	}
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

func fail(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "fullvalidate: %s: %v\n", what, err)
		os.Exit(1)
	}
}

func abs64(v float32) float64 {
	if v < 0 {
		return float64(-v)
	}
	return float64(v)
}

func mean(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}
