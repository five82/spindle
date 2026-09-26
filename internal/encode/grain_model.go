package encode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/five82/reel/internal/chunk"
	"github.com/five82/reel/internal/config"
	"github.com/five82/reel/internal/grain"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/video"
)

// estimateGrainAndCeiling samples the already-aligned original/fftdnoiz pairs.
// Unlike the former observability-only ceiling, this must succeed: a treated
// title cannot encode before its source-matched synthesis model is available.
func estimateGrainAndCeiling(ctx context.Context, in GrainGateInput, stats *perf.GrainTreatmentStats) (grain.Estimate, error) {
	if err := ctx.Err(); err != nil {
		return grain.Estimate{}, err
	}
	if in.DisplayPath == "" || len(stats.SampleChunks) == 0 {
		return grain.Estimate{}, fmt.Errorf("paired-frame analysis requires a display model and sample chunks")
	}
	width, height := video.OutputDimensions(in.Info, in.CropRect)
	proc, err := quality.NewVshipProcessor(width, height, in.Info, in.DisplayPath)
	if err != nil {
		return grain.Estimate{}, fmt.Errorf("create paired-frame scorer: %w", err)
	}
	defer func() { _ = proc.Close() }()
	return estimateGrainFromPairs(ctx, in, stats, func(ctx context.Context, ch chunk.Chunk, observe func(int, []byte, []byte) error) (float32, error) {
		res, err := quality.ComputeChunkDenoiseCeiling(ctx, quality.DenoiseCeilingOptions{
			SourcePath: in.InputPath,
			Info:       in.Info,
			Chunk:      ch,
			CropRect:   in.CropRect,
			Width:      width,
			Height:     height,
			Denoise:    stats.Denoise,
			Processor:  proc,
			Observe:    observe,
		})
		return res.Score, err
	})
}

// estimateGrainFromPairs owns the title-wide estimator; only pair production
// and metric scoring need native decoders and a GPU handler.
func estimateGrainFromPairs(ctx context.Context, in GrainGateInput, stats *perf.GrainTreatmentStats, measure func(context.Context, chunk.Chunk, func(int, []byte, []byte) error) (float32, error)) (grain.Estimate, error) {
	width, height := video.OutputDimensions(in.Info, in.CropRect)
	estimator, err := grain.New(int(width), int(height))
	if err != nil {
		return grain.Estimate{}, err
	}
	defer estimator.Close()

	byIdx := make(map[int]chunk.Chunk, len(in.Chunks))
	for _, ch := range in.Chunks {
		byIdx[ch.Idx] = ch
	}
	start := time.Now()
	var scores []float64
	for _, idx := range stats.SampleChunks {
		ch, ok := byIdx[idx]
		if !ok {
			return grain.Estimate{}, fmt.Errorf("grain sample chunk %d is missing from the plan", idx)
		}
		score, err := measure(ctx, ch, func(i int, original, denoised []byte) error {
			if !grain.SampleFrame(i, ch.Frames()) {
				return nil
			}
			return estimator.Observe(ch.Start+i, original, denoised)
		})
		if err != nil {
			return grain.Estimate{}, fmt.Errorf("paired-frame analysis of chunk %04d: %w", idx, err)
		}
		scores = append(scores, float64(score))
		if in.Verbose != nil {
			in.Verbose(fmt.Sprintf("Grain gate ceiling chunk=%04d denoise_ceiling_jod=%.4f", ch.Idx, score))
		}
	}
	if err := ctx.Err(); err != nil {
		return grain.Estimate{}, err
	}
	estimate, err := estimator.Finish()
	if err != nil {
		return grain.Estimate{}, err
	}
	stats.CeilingSeconds = time.Since(start).Seconds()
	mean, minScore := 0.0, scores[0]
	for _, s := range scores {
		mean += s
		minScore = min(minScore, s)
	}
	mean /= float64(len(scores))
	stats.DenoiseCeilingJODMean = &mean
	stats.DenoiseCeilingJODMin = &minScore
	stats.CeilingMeasured = true
	return estimate, nil
}

// The model and decision share one atomic record. The .tbl used by SVT is
// derived from this record, not an independent checkpoint that can go stale.
type grainVerdict struct {
	perf.GrainTreatmentStats
	Table string `json:"film_grain_table,omitempty"`
}

func (v *grainVerdict) setEstimate(e grain.Estimate) {
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(e.Table)))
	v.Table = e.Table
	v.GrainTable = "estimated:" + sum
	v.Tier = "estimated"
	v.Estimation = &perf.GrainEstimationStats{
		Version: grain.Version, SHA256: sum, Frames: e.Frames,
		AcceptedFrames: e.AcceptedFrames, Patches: e.Patches, Seconds: e.Seconds,
	}
}

func (v *grainVerdict) validate() error {
	if v.Mode != config.GrainTreatmentAuto {
		return fmt.Errorf("invalid recorded grain gate mode %q", v.Mode)
	}
	if !v.Treated {
		if v.Table != "" || v.Estimation != nil || v.GrainTable != "" || v.Denoise != "" {
			return fmt.Errorf("untreated grain verdict contains treatment parameters")
		}
		return nil
	}
	if v.Table == "" || v.Estimation == nil || v.Estimation.Version == "" || v.Denoise == "" {
		return fmt.Errorf("recorded grain treatment has no estimated model; use a new work directory")
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(v.Table)))
	if v.Estimation.SHA256 != sum || v.GrainTable != "estimated:"+sum {
		return fmt.Errorf("recorded grain model checksum mismatch; use a new work directory")
	}
	return nil
}

func grainVerdictPath(workDir string) string {
	return filepath.Join(workDir, "grain-gate.json")
}

func loadGrainVerdict(workDir string) (*grainVerdict, error) {
	data, err := os.ReadFile(grainVerdictPath(workDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read grain verdict: %w", err)
	}
	var v grainVerdict
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("invalid grain verdict; use a new work directory: %w", err)
	}
	if err := v.validate(); err != nil {
		return nil, err
	}
	return &v, nil
}

func saveGrainVerdict(workDir string, v *grainVerdict) error {
	if err := v.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode grain verdict: %w", err)
	}
	return writeGrainFile(grainVerdictPath(workDir), append(data, '\n'))
}

func writeGrainFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".grain-*")
	if err != nil {
		return fmt.Errorf("create grain record: %w", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write grain record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync grain record: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("publish grain record: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
