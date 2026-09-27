// Package chunkplan builds shot-aware chunk boundaries for video encoding.
package chunkplan

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/five82/reel/internal/util"
	"github.com/five82/reel/internal/video"
)

const (
	detectorVersion = "reel-luma-shot-scd-v5"
	metadataVersion = 11

	signatureWidth  = 64
	signatureHeight = 36
	signaturePixels = signatureWidth * signatureHeight
	histogramBins   = 64

	minimumNaturalCutDistance = 6
	baseCutThreshold          = 0.18
	minimumStrongCutScore     = 0.30
	defaultMinimumFrames      = 1
)

// Options controls shot-cut detection and content-aware chunk planning.
type Options struct {
	MaxFrames    int
	MinFrames    int
	TargetFrames int
	CropRect     *video.CropRect
	Progress     func(current, total int)

	// RetainScores keeps the per-frame shot-change scores on the Result for
	// offline analysis (e.g. scripts/chunkbench correlating per-chunk activity
	// against final CRF). The production path leaves this false so the scores
	// slice is freed after boundary detection.
	RetainScores bool

	// ShotDetectWorkers, when > 0, requests that many parallel shot-detection
	// workers instead of the auto cap. It is a benchmarking hook
	// (scripts/chunkbench A/B); the production path leaves it 0. The per-worker
	// frame floor still applies, and boundary output is worker-count invariant.
	ShotDetectWorkers int
}

// BoundaryKind explains why a planned chunk boundary exists.
type BoundaryKind string

const (
	BoundaryKindStart          BoundaryKind = "start"
	BoundaryKindNaturalShotCut BoundaryKind = "natural_shot_cut"
	BoundaryKindSyntheticSplit BoundaryKind = "synthetic_split"
)

// Result describes detected shot cuts and planned chunk boundaries.
type Result struct {
	Boundaries       []int
	BoundaryKinds    []BoundaryKind
	NaturalCuts      int
	NaturalCutFrames []int
	SyntheticSplits  int
	MergedShortShots int
	MergedWeakCuts   int
	Frames           int

	// MergedScenes is kept for compatibility with older internal callers.
	MergedScenes int

	// FrameScores holds the per-frame shot-change scores when Options.RetainScores
	// is set; nil otherwise. Index i is the score for frame i (frame 0 is 0).
	FrameScores []float64

	// ShotDetectWorkersUsed reports how many parallel workers scored the frames.
	// Benchmarking info (scripts/chunkbench); 0 when scoring was skipped or
	// served from a cached boundary file.
	ShotDetectWorkersUsed int
}

// Metadata records the inputs that produced a chunk boundary file.
type Metadata struct {
	Version              int            `json:"version"`
	Detector             string         `json:"detector"`
	InputPath            string         `json:"input_path"`
	InputSize            int64          `json:"input_size"`
	InputModTimeUnixNano int64          `json:"input_mod_time_unix_nano"`
	Width                uint32         `json:"width"`
	Height               uint32         `json:"height"`
	FPSNum               uint32         `json:"fps_num"`
	FPSDen               uint32         `json:"fps_den"`
	Frames               int            `json:"frames"`
	ActualFrames         int            `json:"actual_frames,omitempty"`
	Crop                 string         `json:"crop,omitempty"`
	MaxFrames            int            `json:"max_frames"`
	MinFrames            int            `json:"min_frames"`
	TargetFrames         int            `json:"target_frames,omitempty"`
	Boundaries           int            `json:"boundaries"`
	NaturalCuts          int            `json:"natural_cuts"`
	SyntheticSplits      int            `json:"synthetic_splits"`
	MergedShortShots     int            `json:"merged_short_shots"`
	MergedWeakCuts       int            `json:"merged_weak_cuts,omitempty"`
	MergedScenes         int            `json:"merged_scenes"`
	NaturalCutFrames     []int          `json:"natural_cut_frames,omitempty"`
	BoundaryKinds        []BoundaryKind `json:"boundary_kinds,omitempty"`
}

type frameSignature struct {
	Samples [signaturePixels]uint16
	Hist    [histogramBins]uint32
	Mean    float64
}

type inputIdentity struct {
	Path            string
	Size            int64
	ModTimeUnixNano int64
}

// PlanToFileIfNeeded detects shot cuts and writes planned chunk starts unless a matching cached file exists.
func PlanToFileIfNeeded(ctx context.Context, inputPath, boundaryFile, metadataFile string, inf *video.Info, opts Options) (Result, error) {
	if inf == nil {
		return Result{}, fmt.Errorf("nil video info")
	}
	if cached, ok := loadCachedResult(inputPath, boundaryFile, metadataFile, inf, opts); ok {
		return cached, nil
	}

	result, err := Plan(ctx, inputPath, inf, opts)
	if err != nil {
		return Result{}, err
	}
	if err := writeBoundaryFile(boundaryFile, result.Boundaries); err != nil {
		return Result{}, err
	}
	if err := writeMetadata(inputPath, metadataFile, inf, opts, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

// Plan analyzes luma frames and returns shot-aware chunk starts.
func Plan(ctx context.Context, inputPath string, inf *video.Info, opts Options) (Result, error) {
	if inf == nil {
		return Result{}, fmt.Errorf("nil video info")
	}
	if inf.Frames <= 0 {
		return Result{Boundaries: []int{0}, BoundaryKinds: []BoundaryKind{BoundaryKindStart}}, nil
	}
	maxFrames := normalizeMaxFrames(opts.MaxFrames, inf.Frames)
	minFrames := normalizeMinFrames(opts.MinFrames, maxFrames)
	targetFrames := normalizeTargetFrames(opts.TargetFrames, minFrames, maxFrames)

	workers, scores, err := scoreVideo(ctx, inputPath, inf, opts)
	if err != nil {
		return Result{}, err
	}
	frameCount := len(scores)
	naturalCuts := detectNaturalCuts(scores)
	plan := planBoundaries(naturalCuts, frameCount, maxFrames, minFrames, targetFrames, scores)
	result := Result{
		Boundaries:            plan.Boundaries,
		BoundaryKinds:         plan.BoundaryKinds,
		NaturalCuts:           max(0, len(naturalCuts)-1),
		NaturalCutFrames:      naturalCutFrames(naturalCuts),
		SyntheticSplits:       plan.SyntheticSplits,
		MergedShortShots:      plan.MergedShortShots,
		MergedWeakCuts:        plan.MergedWeakCuts,
		Frames:                frameCount,
		MergedScenes:          plan.MergedShortShots,
		ShotDetectWorkersUsed: workers,
	}
	if opts.RetainScores {
		result.FrameScores = scores
	}
	return result, nil
}

// scoreVideo computes per-frame shot-change scores. The decode is split into
// contiguous segments scored by parallel workers; each worker decodes one
// extra leading frame so segment-boundary scores match the sequential pass.
func scoreVideo(ctx context.Context, inputPath string, inf *video.Info, opts Options) (int, []float64, error) {
	workers := shotDetectWorkers(inf.Frames, opts.ShotDetectWorkers)
	threads := max(2, decoderThreads()/workers)
	if workers == 1 {
		threads = decoderThreads()
	}

	scores := make([]float64, inf.Frames)
	var progressMu sync.Mutex
	decodedTotal := 0
	report := func(n int) {
		if opts.Progress == nil {
			return
		}
		progressMu.Lock()
		decodedTotal += n
		current := decodedTotal
		progressMu.Unlock()
		opts.Progress(current, inf.Frames)
	}

	type segmentOutcome struct {
		decodedEnd int
		err        error
	}
	outcomes := make([]segmentOutcome, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		segStart := w * inf.Frames / workers
		segEnd := (w + 1) * inf.Frames / workers
		lastSegment := w == workers-1
		wg.Add(1)
		go func(w, segStart, segEnd int, lastSegment bool) {
			defer wg.Done()
			decodedEnd, err := scoreVideoSegment(ctx, inputPath, inf, opts, threads, segStart, segEnd, lastSegment, scores, report)
			outcomes[w] = segmentOutcome{decodedEnd: decodedEnd, err: err}
		}(w, segStart, segEnd, lastSegment)
	}
	wg.Wait()

	frameCount := inf.Frames
	for w, outcome := range outcomes {
		if outcome.err != nil {
			return 0, nil, outcome.err
		}
		if w == workers-1 {
			frameCount = outcome.decodedEnd
		}
	}
	if opts.Progress != nil {
		opts.Progress(frameCount, frameCount)
	}
	return workers, scores[:frameCount], nil
}

// scoreVideoSegment scores frames [segStart, segEnd). It decodes from
// segStart-1 so the boundary frame's score uses the true previous signature.
// Only the last segment may end early on EOF (short streams); earlier
// segments treat EOF as an error.
func scoreVideoSegment(
	ctx context.Context,
	inputPath string,
	inf *video.Info,
	opts Options,
	threads int,
	segStart, segEnd int,
	lastSegment bool,
	scores []float64,
	report func(int),
) (int, error) {
	src, err := video.Open(inputPath, threads)
	if err != nil {
		return 0, fmt.Errorf("shot cut detection: open video: %w", err)
	}
	defer src.Close()

	firstFrame := max(0, segStart-1)
	var previous *frameSignature
	for frameIdx := firstFrame; frameIdx < segEnd; frameIdx++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		frame, err := src.ReadLumaFrame(frameIdx, inf)
		if err != nil {
			if errors.Is(err, io.EOF) && lastSegment && acceptableTerminalEOF(frameIdx, inf.Frames) {
				return frameIdx, nil
			}
			return 0, fmt.Errorf("shot cut detection: read frame %d: %w", frameIdx, err)
		}
		sig, err := signatureFromFrame(frame, opts.CropRect)
		if err != nil {
			return 0, fmt.Errorf("shot cut detection: analyze frame %d: %w", frameIdx, err)
		}
		if previous != nil && frameIdx >= segStart {
			scores[frameIdx] = signatureChange(previous, sig)
		}
		previous = sig

		if frameIdx >= segStart && (frameIdx+1)%100 == 0 {
			report(100)
		}
	}
	return segEnd, nil
}

func shotDetectWorkers(frames, requested int) int {
	const minFramesPerWorker = 1500
	maxByFrames := frames / minFramesPerWorker
	if requested > 0 {
		// Benchmark override (Options.ShotDetectWorkers): honor the explicit
		// request, still floored by the per-worker frame count.
		return max(1, min(requested, maxByFrames))
	}
	// Half the LOGICAL cores. decoderThreads()/workers is floored at 2, so
	// logical/2 workers put total decoder threads at the logical core count --
	// detection runs in a serial phase with the rest of the machine idle
	// (measured 39% CPU during the phase at the old physical/2 default), so
	// SMT capacity is free. 2026-07-02 A/B: 16 workers cut the Sully-feature
	// detection 575s -> 415s (-28%) and sullyhv 93.5s -> 71.9s, identical
	// boundary hashes; on non-SMT boxes logical/2 == the physical/2 default
	// this replaces. Earlier history (2026-06-27): physical/2 beat physical/4
	// by 2-21%; 6 workers is a trap (16/6 floors to 2 threads/worker = 12
	// total, fewer than cap4's 16). See docs/PERFORMANCE_TESTING.md.
	workers := max(util.LogicalCores(), 1) / 2
	return max(1, min(workers, maxByFrames))
}

func decoderThreads() int {
	return min(max(util.PhysicalCores(), 1), 16)
}

func acceptableTerminalEOF(decodedFrames, expectedFrames int) bool {
	return decodedFrames > 0 && expectedFrames > 0 && decodedFrames*100 >= expectedFrames*95
}

func signatureFromFrame(frame *video.LumaFrame, crop *video.CropRect) (*frameSignature, error) {
	if frame == nil {
		return nil, fmt.Errorf("nil luma frame")
	}
	bytesPerSample := 1
	if frame.Is10Bit {
		bytesPerSample = 2
	}
	if frame.Width <= 0 || frame.Height <= 0 || frame.Stride < frame.Width*bytesPerSample || len(frame.Data) < frame.Stride*frame.Height {
		return nil, fmt.Errorf("invalid luma geometry")
	}

	x, y, width, height, err := analysisRect(frame, crop)
	if err != nil {
		return nil, err
	}

	sig := &frameSignature{}
	var sum uint64
	for gy := 0; gy < signatureHeight; gy++ {
		srcY := y + (gy*height+height/2)/signatureHeight
		if srcY >= y+height {
			srcY = y + height - 1
		}
		rowOff := srcY * frame.Stride
		for gx := 0; gx < signatureWidth; gx++ {
			srcX := x + (gx*width+width/2)/signatureWidth
			if srcX >= x+width {
				srcX = x + width - 1
			}
			value := readLuma10(frame.Data, rowOff, srcX, frame.Is10Bit, frame.LumaShift)
			idx := gy*signatureWidth + gx
			sig.Samples[idx] = value
			sig.Hist[int(value)*histogramBins/1024]++
			sum += uint64(value)
		}
	}
	sig.Mean = float64(sum) / signaturePixels
	return sig, nil
}

func analysisRect(frame *video.LumaFrame, crop *video.CropRect) (x, y, width, height int, err error) {
	x, y = 0, 0
	width, height = frame.Width, frame.Height
	if crop != nil {
		x = int(crop.X)
		y = int(crop.Y)
		width = int(crop.Width)
		height = int(crop.Height)
	}
	if x < 0 || y < 0 || width <= 0 || height <= 0 || x > frame.Width || width > frame.Width-x || y > frame.Height || height > frame.Height-y {
		return 0, 0, 0, 0, fmt.Errorf("invalid analysis rectangle %dx%d+%d+%d for frame %dx%d", width, height, x, y, frame.Width, frame.Height)
	}
	return x, y, width, height, nil
}

func readLuma10(data []byte, rowOff, col int, is10Bit bool, lumaShift int) uint16 {
	if !is10Bit {
		return uint16(data[rowOff+col]) << 2
	}
	if lumaShift == 0 {
		lumaShift = 2
	}
	shift := max(0, lumaShift-2)
	sample := binary.LittleEndian.Uint16(data[rowOff+col*2:]) >> shift
	return uint16(min(sample, 1023))
}

func signatureChange(previous, current *frameSignature) float64 {
	var pixelDelta uint64
	for i := range previous.Samples {
		pixelDelta += uint64(absInt(int(previous.Samples[i]) - int(current.Samples[i])))
	}
	pixelScore := float64(pixelDelta) / (signaturePixels * 1023)

	var histDelta uint64
	for i := range previous.Hist {
		histDelta += uint64(absInt(int(previous.Hist[i]) - int(current.Hist[i])))
	}
	histScore := float64(histDelta) / (2 * signaturePixels)
	meanScore := math.Abs(previous.Mean-current.Mean) / 1023

	return 0.65*pixelScore + 0.30*histScore + 0.05*meanScore
}

func detectNaturalCuts(scores []float64) []int {
	if len(scores) <= 1 {
		return []int{0}
	}
	threshold := shotCutThreshold(scores)
	cuts := []int{0}

	for i := 1; i < len(scores); {
		if scores[i] < threshold {
			i++
			continue
		}

		start := i
		best := i
		for i < len(scores) && scores[i] >= threshold {
			if scores[i] > scores[best] {
				best = i
			}
			i++
		}
		clusterLen := i - start
		if looksLikeFlashCluster(clusterLen) {
			continue
		}
		if best-cuts[len(cuts)-1] >= minimumNaturalCutDistance {
			cuts = append(cuts, best)
		}
	}
	return cuts
}

func shotCutThreshold(scores []float64) float64 {
	values := make([]float64, 0, len(scores)-1)
	for _, score := range scores[1:] {
		if score > 0 {
			values = append(values, score)
		}
	}
	if len(values) == 0 {
		return baseCutThreshold
	}
	sort.Float64s(values)
	median := percentileSorted(values, 0.50)
	p95 := percentileSorted(values, 0.95)
	madValues := make([]float64, len(values))
	for i, value := range values {
		madValues[i] = math.Abs(value - median)
	}
	sort.Float64s(madValues)
	mad := percentileSorted(madValues, 0.50)
	adaptive := median + 6*mad
	return maxFloat(baseCutThreshold, minFloat(maxFloat(adaptive, p95*1.10), 0.35))
}

func looksLikeFlashCluster(clusterLen int) bool {
	return clusterLen == 2
}

type boundaryPlan struct {
	Boundaries       []int
	BoundaryKinds    []BoundaryKind
	SyntheticSplits  int
	MergedShortShots int
	MergedWeakCuts   int
}

func planBoundaries(naturalCuts []int, totalFrames, maxFrames, minFrames, targetFrames int, scores []float64) boundaryPlan {
	if totalFrames <= 0 {
		return boundaryPlan{Boundaries: []int{0}, BoundaryKinds: []BoundaryKind{BoundaryKindStart}}
	}
	maxFrames = normalizeMaxFrames(maxFrames, totalFrames)
	minFrames = normalizeMinFrames(minFrames, maxFrames)
	targetFrames = normalizeTargetFrames(targetFrames, minFrames, maxFrames)

	cuts, mergedShortShots := packShortShots(normalizedNaturalCuts(naturalCuts, totalFrames), totalFrames, maxFrames, minFrames, scores)
	cuts, mergedWeakCuts := packWeakCutsToTarget(cuts, totalFrames, maxFrames, targetFrames, scores)
	plan := boundaryPlan{
		Boundaries:    make([]int, 0, len(cuts)),
		BoundaryKinds: make([]BoundaryKind, 0, len(cuts)),
	}

	appendBoundary := func(frame int, kind BoundaryKind) {
		if frame < 0 || frame >= totalFrames {
			return
		}
		if frame == 0 {
			kind = BoundaryKindStart
		}
		if len(plan.Boundaries) > 0 && plan.Boundaries[len(plan.Boundaries)-1] == frame {
			plan.BoundaryKinds[len(plan.BoundaryKinds)-1] = preferredBoundaryKind(plan.BoundaryKinds[len(plan.BoundaryKinds)-1], kind)
			return
		}
		plan.Boundaries = append(plan.Boundaries, frame)
		plan.BoundaryKinds = append(plan.BoundaryKinds, kind)
	}

	for i, start := range cuts {
		end := totalFrames
		if i+1 < len(cuts) {
			end = cuts[i+1]
		}
		frames := end - start
		if frames <= 0 {
			continue
		}
		appendBoundary(start, BoundaryKindNaturalShotCut)
		chunkCount := int(math.Ceil(float64(frames) / float64(maxFrames)))
		for n := 1; n < chunkCount; n++ {
			split := start + (frames*n)/chunkCount
			appendBoundary(split, BoundaryKindSyntheticSplit)
			plan.SyntheticSplits++
		}
	}
	if len(plan.Boundaries) == 0 || plan.Boundaries[0] != 0 {
		plan.Boundaries = append([]int{0}, plan.Boundaries...)
		plan.BoundaryKinds = append([]BoundaryKind{BoundaryKindStart}, plan.BoundaryKinds...)
	}
	plan.MergedShortShots = mergedShortShots
	plan.MergedWeakCuts = mergedWeakCuts
	return plan
}

func preferredBoundaryKind(a, b BoundaryKind) BoundaryKind {
	if a == BoundaryKindStart || b == BoundaryKindStart {
		return BoundaryKindStart
	}
	if a == BoundaryKindNaturalShotCut || b == BoundaryKindNaturalShotCut {
		return BoundaryKindNaturalShotCut
	}
	return BoundaryKindSyntheticSplit
}

func packShortShots(cuts []int, totalFrames, maxFrames, minFrames int, scores []float64) ([]int, int) {
	if minFrames <= defaultMinimumFrames || len(cuts) <= 1 {
		return cuts, 0
	}

	packed := append([]int(nil), cuts...)
	merged := 0
	for {
		changed := false
		for i := 0; i < len(packed); i++ {
			start := packed[i]
			end := totalFrames
			if i+1 < len(packed) {
				end = packed[i+1]
			}
			if end-start >= minFrames {
				continue
			}

			removeIdx := shortShotBoundaryToRemove(packed, totalFrames, maxFrames, scores, i)
			if removeIdx <= 0 || removeIdx >= len(packed) {
				continue
			}
			packed = append(packed[:removeIdx], packed[removeIdx+1:]...)
			merged++
			changed = true
			break
		}
		if !changed {
			return packed, merged
		}
	}
}

func shortShotBoundaryToRemove(cuts []int, totalFrames, maxFrames int, scores []float64, shotIdx int) int {
	start := cuts[shotIdx]
	end := totalFrames
	if shotIdx+1 < len(cuts) {
		end = cuts[shotIdx+1]
	}

	prevRemoveIdx := -1
	if shotIdx > 0 && end-cuts[shotIdx-1] <= maxFrames {
		prevRemoveIdx = shotIdx
	}

	nextRemoveIdx := -1
	if shotIdx+1 < len(cuts) {
		nextEnd := totalFrames
		if shotIdx+2 < len(cuts) {
			nextEnd = cuts[shotIdx+2]
		}
		if nextEnd-start <= maxFrames {
			nextRemoveIdx = shotIdx + 1
		}
	}

	switch {
	case prevRemoveIdx < 0:
		return nextRemoveIdx
	case nextRemoveIdx < 0:
		return prevRemoveIdx
	case cutScore(scores, cuts[prevRemoveIdx]) <= cutScore(scores, cuts[nextRemoveIdx]):
		return prevRemoveIdx
	default:
		return nextRemoveIdx
	}
}

func packWeakCutsToTarget(cuts []int, totalFrames, maxFrames, targetFrames int, scores []float64) ([]int, int) {
	if targetFrames <= 0 || len(cuts) <= 1 {
		return cuts, 0
	}
	strongThreshold := strongCutThreshold(cuts, scores)
	packed := append([]int(nil), cuts...)
	merged := 0
	for {
		removeIdx := weakTargetBoundaryToRemove(packed, totalFrames, maxFrames, targetFrames, strongThreshold, scores)
		if removeIdx <= 0 || removeIdx >= len(packed) {
			return packed, merged
		}
		packed = append(packed[:removeIdx], packed[removeIdx+1:]...)
		merged++
	}
}

func weakTargetBoundaryToRemove(cuts []int, totalFrames, maxFrames, targetFrames int, strongThreshold float64, scores []float64) int {
	bestIdx := -1
	bestScore := math.MaxFloat64
	bestPressure := -1
	for i := 1; i < len(cuts); i++ {
		leftStart := cuts[i-1]
		boundary := cuts[i]
		rightEnd := totalFrames
		if i+1 < len(cuts) {
			rightEnd = cuts[i+1]
		}
		mergedFrames := rightEnd - leftStart
		if mergedFrames > maxFrames {
			continue
		}
		leftFrames := boundary - leftStart
		rightFrames := rightEnd - boundary
		if leftFrames >= targetFrames && rightFrames >= targetFrames {
			continue
		}
		score := cutScore(scores, boundary)
		if strongThreshold > 0 && score >= strongThreshold {
			continue
		}
		pressure := max(0, targetFrames-leftFrames) + max(0, targetFrames-rightFrames)
		if score < bestScore || (score == bestScore && pressure > bestPressure) {
			bestIdx = i
			bestScore = score
			bestPressure = pressure
		}
	}
	return bestIdx
}

func strongCutThreshold(cuts []int, scores []float64) float64 {
	if len(scores) == 0 || len(cuts) <= 2 {
		return 0
	}
	values := make([]float64, 0, len(cuts)-1)
	for _, cut := range cuts[1:] {
		if score := cutScore(scores, cut); score > 0 {
			values = append(values, score)
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return maxFloat(percentileSorted(values, 0.90), minimumStrongCutScore)
}

func cutScore(scores []float64, cut int) float64 {
	if cut <= 0 || cut >= len(scores) {
		return 0
	}
	return scores[cut]
}

func naturalCutFrames(cuts []int) []int {
	frames := make([]int, 0, len(cuts))
	for _, cut := range cuts {
		if cut > 0 {
			frames = append(frames, cut)
		}
	}
	return frames
}

func inferBoundaryKinds(boundaries, naturalCutFrames []int) []BoundaryKind {
	natural := make(map[int]bool, len(naturalCutFrames))
	for _, frame := range naturalCutFrames {
		natural[frame] = true
	}
	kinds := make([]BoundaryKind, len(boundaries))
	for i, boundary := range boundaries {
		switch {
		case boundary == 0:
			kinds[i] = BoundaryKindStart
		case natural[boundary]:
			kinds[i] = BoundaryKindNaturalShotCut
		default:
			kinds[i] = BoundaryKindSyntheticSplit
		}
	}
	return kinds
}

func normalizedNaturalCuts(cuts []int, totalFrames int) []int {
	out := make([]int, 0, len(cuts)+1)
	out = append(out, 0)
	for _, cut := range cuts {
		if cut > 0 && cut < totalFrames {
			out = append(out, cut)
		}
	}
	sort.Ints(out)
	return dedupeSorted(out)
}

func normalizeMaxFrames(maxFrames, totalFrames int) int {
	if maxFrames <= 0 {
		return max(1, totalFrames)
	}
	return max(1, maxFrames)
}

func normalizeMinFrames(minFrames, maxFrames int) int {
	if minFrames <= 0 {
		return defaultMinimumFrames
	}
	return min(max(minFrames, defaultMinimumFrames), maxFrames)
}

func normalizeTargetFrames(targetFrames, minFrames, maxFrames int) int {
	if targetFrames <= minFrames {
		return 0
	}
	return min(targetFrames, maxFrames)
}

func loadCachedResult(inputPath, boundaryFile, metadataFile string, inf *video.Info, opts Options) (Result, bool) {
	data, err := os.ReadFile(metadataFile)
	if err != nil {
		return Result{}, false
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Result{}, false
	}
	if !metadataMatches(inputPath, inf, opts, meta) {
		return Result{}, false
	}
	boundaries, err := readBoundaryFile(boundaryFile)
	if err != nil || len(boundaries) == 0 {
		return Result{}, false
	}
	mergedShortShots := meta.MergedShortShots
	if mergedShortShots == 0 {
		mergedShortShots = meta.MergedScenes
	}
	boundaryKinds := meta.BoundaryKinds
	if len(boundaryKinds) != len(boundaries) {
		boundaryKinds = inferBoundaryKinds(boundaries, meta.NaturalCutFrames)
	}
	frames := meta.ActualFrames
	if frames <= 0 {
		frames = meta.Frames
	}
	return Result{
		Boundaries:       boundaries,
		BoundaryKinds:    boundaryKinds,
		NaturalCuts:      meta.NaturalCuts,
		NaturalCutFrames: meta.NaturalCutFrames,
		SyntheticSplits:  meta.SyntheticSplits,
		MergedShortShots: mergedShortShots,
		MergedWeakCuts:   meta.MergedWeakCuts,
		Frames:           frames,
		MergedScenes:     mergedShortShots,
	}, true
}

func metadataMatches(inputPath string, inf *video.Info, opts Options, meta Metadata) bool {
	id, err := identifyInput(inputPath)
	if err != nil {
		return false
	}
	return meta.Version == metadataVersion &&
		meta.Detector == detectorVersion &&
		meta.InputPath == id.Path &&
		meta.InputSize == id.Size &&
		meta.InputModTimeUnixNano == id.ModTimeUnixNano &&
		meta.Width == inf.Width &&
		meta.Height == inf.Height &&
		meta.FPSNum == inf.FPSNum &&
		meta.FPSDen == inf.FPSDen &&
		meta.Frames == inf.Frames &&
		meta.Crop == cropString(opts.CropRect) &&
		meta.MaxFrames == normalizeMaxFrames(opts.MaxFrames, inf.Frames) &&
		meta.MinFrames == normalizeMinFrames(opts.MinFrames, normalizeMaxFrames(opts.MaxFrames, inf.Frames)) &&
		meta.TargetFrames == normalizeTargetFrames(opts.TargetFrames, normalizeMinFrames(opts.MinFrames, normalizeMaxFrames(opts.MaxFrames, inf.Frames)), normalizeMaxFrames(opts.MaxFrames, inf.Frames))
}

func writeMetadata(inputPath, metadataFile string, inf *video.Info, opts Options, result Result) error {
	id, err := identifyInput(inputPath)
	if err != nil {
		return fmt.Errorf("shot cut detection: stat input: %w", err)
	}
	mergedShortShots := result.MergedShortShots
	if mergedShortShots == 0 {
		mergedShortShots = result.MergedScenes
	}
	boundaryKinds := result.BoundaryKinds
	if len(boundaryKinds) != len(result.Boundaries) {
		boundaryKinds = inferBoundaryKinds(result.Boundaries, result.NaturalCutFrames)
	}
	meta := Metadata{
		Version:              metadataVersion,
		Detector:             detectorVersion,
		InputPath:            id.Path,
		InputSize:            id.Size,
		InputModTimeUnixNano: id.ModTimeUnixNano,
		Width:                inf.Width,
		Height:               inf.Height,
		FPSNum:               inf.FPSNum,
		FPSDen:               inf.FPSDen,
		Frames:               inf.Frames,
		ActualFrames:         result.Frames,
		Crop:                 cropString(opts.CropRect),
		MaxFrames:            normalizeMaxFrames(opts.MaxFrames, inf.Frames),
		MinFrames:            normalizeMinFrames(opts.MinFrames, normalizeMaxFrames(opts.MaxFrames, inf.Frames)),
		TargetFrames:         normalizeTargetFrames(opts.TargetFrames, normalizeMinFrames(opts.MinFrames, normalizeMaxFrames(opts.MaxFrames, inf.Frames)), normalizeMaxFrames(opts.MaxFrames, inf.Frames)),
		Boundaries:           len(result.Boundaries),
		NaturalCuts:          result.NaturalCuts,
		SyntheticSplits:      result.SyntheticSplits,
		MergedShortShots:     mergedShortShots,
		MergedWeakCuts:       result.MergedWeakCuts,
		MergedScenes:         mergedShortShots,
		NaturalCutFrames:     result.NaturalCutFrames,
		BoundaryKinds:        boundaryKinds,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("shot cut detection: encode metadata: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(metadataFile, data, 0644); err != nil {
		return fmt.Errorf("shot cut detection: write metadata: %w", err)
	}
	return nil
}

func identifyInput(inputPath string) (inputIdentity, error) {
	abs, err := filepath.Abs(inputPath)
	if err != nil {
		abs = inputPath
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}
	stat, err := os.Stat(inputPath)
	if err != nil {
		return inputIdentity{}, err
	}
	return inputIdentity{Path: abs, Size: stat.Size(), ModTimeUnixNano: stat.ModTime().UnixNano()}, nil
}

func cropString(crop *video.CropRect) string {
	if crop == nil {
		return ""
	}
	return fmt.Sprintf("crop=%d:%d:%d:%d", crop.Width, crop.Height, crop.X, crop.Y)
}

func writeBoundaryFile(path string, boundaries []int) error {
	var b strings.Builder
	for _, boundary := range boundaries {
		_, _ = fmt.Fprintf(&b, "%d\n", boundary)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("shot cut detection: write chunk plan: %w", err)
	}
	return nil
}

func readBoundaryFile(path string) ([]int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var boundaries []int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		boundary, err := strconv.Atoi(line)
		if err != nil {
			return nil, err
		}
		boundaries = append(boundaries, boundary)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Ints(boundaries)
	return dedupeSorted(boundaries), nil
}

func percentileSorted(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}
	pos := p * float64(len(values)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return values[lower]
	}
	weight := pos - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func dedupeSorted(values []int) []int {
	if len(values) <= 1 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
