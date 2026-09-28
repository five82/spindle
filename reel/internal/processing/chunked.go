// Package processing provides video processing orchestration.
package processing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	nativeaudio "github.com/five82/spindle/reel/internal/audio"
	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/chunkplan"
	"github.com/five82/spindle/reel/internal/config"
	"github.com/five82/spindle/reel/internal/encode"
	"github.com/five82/spindle/reel/internal/media"
	"github.com/five82/spindle/reel/internal/perf"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/reporter"
	"github.com/five82/spindle/reel/internal/video"
	"github.com/five82/spindle/reel/internal/worker"
)

// ProcessChunked runs the chunked encoding pipeline for a single file.
// Returns the crop result so the caller can use it for validation.
type targetQualityEncoder func(context.Context, []chunk.Chunk, string, *video.Info, *encode.EncodeConfig, string, *video.CropRect, encode.ProgressCallback, encode.TargetQualityConfig) (int, *perf.TargetQualityStats, error)

func ProcessChunked(
	ctx context.Context,
	cfg *config.Config,
	inputPath, outputPath string,
	videoProps *media.VideoProperties,
	vidInf *video.Info,
	audioStreams []media.AudioStreamInfo,
	qualitySetting float32,
	rep reporter.Reporter,
	perfc *perf.Collector,
) (CropResult, error) {
	return processChunkedWithEncoder(ctx, cfg, inputPath, outputPath, videoProps, vidInf, audioStreams, qualitySetting, rep, perfc, encode.EncodeTargetQuality)
}

func processChunkedWithEncoder(
	ctx context.Context,
	cfg *config.Config,
	inputPath, outputPath string,
	videoProps *media.VideoProperties,
	vidInf *video.Info,
	audioStreams []media.AudioStreamInfo,
	qualitySetting float32,
	rep reporter.Reporter,
	perfc *perf.Collector,
	encodeTarget targetQualityEncoder,
) (CropResult, error) {
	if cfg.QualityMode == config.QualityModeTarget && !quality.VshipBuildEnabled() {
		return CropResult{}, fmt.Errorf("target-quality mode is not available in this build; rebuild without -tags no_vship and with libvship installed, or use --quality-mode crf")
	}

	// Create work directory
	workDir := chunk.GetWorkDirPath(inputPath, cfg.GetTempDir())
	if err := chunk.CreateWorkDir(workDir); err != nil {
		return CropResult{}, fmt.Errorf("failed to create work directory: %w", err)
	}
	perfc.SetWorkDir(workDir)

	if cfg.KeepWorkDir {
		rep.Verbose(fmt.Sprintf("Work directory: %s", workDir))
	}

	// Cleanup on completion (unless resuming a failed encode or explicitly keeping the work directory).
	defer func() {
		// Only cleanup if output was successfully created.
		if cfg.KeepWorkDir {
			return
		}
		if _, err := os.Stat(outputPath); err == nil {
			_ = chunk.CleanupWorkDir(workDir)
		}
	}()

	// One phase is open at a time; the deferred end closes whichever phase an
	// early return leaves open.
	phases := newPhaseTracker(perfc, rep)
	defer phases.end()

	// ========================================================================
	// PHASE 1: Run luma-based crop detection
	//
	// vidInf (the decoded first-frame probe) is supplied by the caller so the
	// orchestrator can share one probe between HDR analysis and the chunked
	// pipeline instead of decoding the first frame twice.
	// ========================================================================
	rep.StageProgress(reporter.StageProgress{Stage: "Preparing", Message: "Analyzing video and detecting crop"})

	if cfg.MetricWorkers == 0 {
		fileCfg := *cfg
		fileCfg.MetricWorkers = cfg.MetricWorkersForWidth(vidInf.Width)
		cfg = &fileCfg
	}

	phases.start("Crop detection")
	cropResult := DetectCrop(inputPath, vidInf, cfg.CropMode == "none")
	phases.end()

	// Report crop detection result
	rep.CropResult(reporter.CropSummary{
		Message:  cropResult.Message,
		Crop:     cropResult.CropFilter,
		Required: cropResult.Required,
		Disabled: cfg.CropMode == "none",
	})

	// Convert crop filter to an exact source rectangle for shot detection and encoding.
	var cropRect *video.CropRect
	var err error
	if cropResult.Required && cropResult.CropFilter != "" {
		cropRect, err = parseCropFilter(cropResult.CropFilter, videoProps.Width, videoProps.Height)
		if err != nil {
			return CropResult{}, fmt.Errorf("invalid crop filter %q: %w", cropResult.CropFilter, err)
		}
		rep.Verbose(fmt.Sprintf("Crop rectangle: %dx%d at +%d+%d", cropRect.Width, cropRect.Height, cropRect.X, cropRect.Y))
	}

	// Generate shot-aware chunks based on resolution (using config values).
	chunkDuration := cfg.ChunkDurationForWidth(vidInf.Width)
	if cfg.QualityMode == config.QualityModeTarget {
		chunkDuration = cfg.TargetQualityChunkDurationForWidth(vidInf.Width)
	}
	fps := float64(vidInf.FPSNum) / float64(vidInf.FPSDen)
	maxChunkFrames := int(math.Ceil(fps * chunkDuration))
	if maxChunkFrames < 1 {
		maxChunkFrames = 1
	}
	minChunkFrames := int(math.Ceil(fps * 2))
	if minChunkFrames < 1 {
		minChunkFrames = 1
	}
	targetChunkFrames := 0
	if cfg.QualityMode == config.QualityModeTarget {
		targetChunkFrames = int(math.Ceil(fps * 6))
	}
	rep.StageProgress(reporter.StageProgress{Stage: "Chunking", Message: "Detecting shot cuts"})
	chunkPlanFile := filepath.Join(workDir, "chunk-plan.txt")
	chunkPlanMetadataFile := filepath.Join(workDir, "chunk-plan.json")
	lastPlanProgress := time.Now().Add(-10 * time.Second)
	lastPlanPercent := -1
	phases.start("Shot cut detection")
	planResult, err := chunkplan.PlanToFileIfNeeded(ctx, inputPath, chunkPlanFile, chunkPlanMetadataFile, vidInf, chunkplan.Options{
		MaxFrames:    maxChunkFrames,
		MinFrames:    minChunkFrames,
		TargetFrames: targetChunkFrames,
		CropRect:     cropRect,
		Progress: func(current, total int) {
			if total <= 0 {
				return
			}
			percent := int(float64(current) * 100 / float64(total))
			now := time.Now()
			if current == total || percent >= lastPlanPercent+5 || now.Sub(lastPlanProgress) >= 10*time.Second {
				rep.StageProgress(reporter.StageProgress{Lane: "work", State: "running", Stage: "Shot cut detection", Message: "Scanning source frames", Completed: int64(current), Total: int64(total), Unit: "frames"})
				rep.Verbose(fmt.Sprintf("Shot cut detection progress: %d%% (%d/%d frames)", percent, current, total))
				lastPlanPercent = percent
				lastPlanProgress = now
			}
		},
	})
	phases.end()
	if err != nil {
		return CropResult{}, fmt.Errorf("shot cut detection failed: %w", err)
	}
	if planResult.Frames > 0 && planResult.Frames != vidInf.Frames {
		rep.Verbose(fmt.Sprintf("Video frame count adjusted after decode: probed %d, decoded %d", vidInf.Frames, planResult.Frames))
		vidInf.Frames = planResult.Frames
	}
	if planResult.MergedWeakCuts > 0 {
		rep.Verbose(fmt.Sprintf("Detected %d natural shot cuts, merged %d short shots, merged %d weak cuts, and added %d duration splits", planResult.NaturalCuts, planResult.MergedShortShots, planResult.MergedWeakCuts, planResult.SyntheticSplits))
	} else {
		rep.Verbose(fmt.Sprintf("Detected %d natural shot cuts, merged %d short shots, and added %d duration splits", planResult.NaturalCuts, planResult.MergedShortShots, planResult.SyntheticSplits))
	}
	retainedNaturalCuts := 0
	for _, kind := range planResult.BoundaryKinds {
		if kind == chunkplan.BoundaryKindNaturalShotCut {
			retainedNaturalCuts++
		}
	}
	rep.Verbose(fmt.Sprintf("Chunk boundaries: %d natural shot cuts, %d duration splits", retainedNaturalCuts, planResult.SyntheticSplits))

	// Load planned chunk boundaries
	phases.start("Chunk planning")
	segments, err := chunk.LoadSegments(chunkPlanFile, vidInf.Frames)
	if err != nil {
		return CropResult{}, fmt.Errorf("failed to load chunk boundaries: %w", err)
	}
	rep.Verbose(fmt.Sprintf("Created %d content-aware chunks", len(segments)))

	// Convert planned segments to chunks
	chunks := chunk.Chunkify(segments)
	rep.StageProgress(reporter.StageProgress{Stage: "Chunking", Message: fmt.Sprintf("Split video into %d chunks", len(chunks))})

	// Calculate average chunk duration for verbose output
	totalFrames := 0
	for _, c := range chunks {
		totalFrames += int(c.End - c.Start)
	}
	avgChunkFrames := float64(totalFrames) / float64(len(chunks))
	avgChunkDuration := avgChunkFrames / fps
	rep.Verbose(fmt.Sprintf("Average chunk duration: %.1fs (%d frames)", avgChunkDuration, int(avgChunkFrames)))
	rep.Verbose(chunkDistributionSummary(chunks, fps))
	phases.end()

	// Setup encode config
	var grainTable *string
	if cfg.GrainTable != "" {
		gt := cfg.GrainTable
		grainTable = &gt
	}
	encCfg := &encode.EncodeConfig{
		GrainTable:            grainTable,
		CRF:                   qualitySetting,
		Preset:                cfg.SVTAV1Preset,
		Tune:                  cfg.SVTAV1Tune,
		ACBias:                cfg.SVTAV1ACBias,
		EnableVarianceBoost:   cfg.SVTAV1EnableVarianceBoost,
		VarianceBoostStrength: cfg.SVTAV1VarianceBoostStrength,
		VarianceOctile:        cfg.SVTAV1VarianceOctile,
		LevelOfParallelism:    cfg.SVTAV1LevelOfParallelism,
		Denoise:               cfg.Denoise,
	}
	var grainStats *perf.GrainTreatmentStats
	if cfg.Verbose {
		encCfg.StatusCallback = func(message string) {
			rep.Verbose(message)
		}
	}
	// Worker reductions and the critical cancel are degraded-behavior events
	// (they change what the encode does), so they reach the reporter
	// unconditionally rather than only in verbose mode.
	encCfg.WarningCallback = func(message string) {
		rep.Warning(message)
	}

	// Grain treatment. The gate needs the chunk plan and the crop rectangle,
	// and its verdict must be settled before the first chunk is encoded, so it
	// runs between chunk planning and encoding. Fixed-CRF mode is never gated:
	// without quality feedback there is nothing to spend the freed bits on.
	gated := cfg.QualityMode == config.QualityModeTarget
	gateInput := encode.GrainGateInput{
		InputPath:     inputPath,
		WorkDir:       workDir,
		Info:          vidInf,
		Chunks:        chunks,
		CropRect:      cropRect,
		BandTopJOD:    float64(cfg.TargetQualityTarget + cfg.TargetQualityTolerance),
		BandCenterJOD: float64(cfg.TargetQualityTarget),
		Verbose:       rep.Verbose,
	}

	phases.start("Resume setup")
	// The manifest records the treatment the chunks in this work directory
	// were encoded under, so it is built from the verdict already recorded
	// there (if any) and rewritten below once the gate decides. A run under a
	// different treatment then sees a mismatched manifest and starts over
	// instead of mixing treated and untreated chunks in one output.
	recorded := encode.GrainTreatment{}
	if gated {
		recorded, err = encode.RecordedGrainTreatment(cfg.GrainTreatment, encCfg, gateInput)
		if err != nil {
			return CropResult{}, err
		}
	}
	manifest, err := buildResumeManifest(inputPath, vidInf, cfg, chunks, cropResult.CropFilter, chunkDuration, qualitySetting, recorded)
	if err != nil {
		return CropResult{}, err
	}
	resumeReset, err := chunk.EnsureResumeManifest(workDir, manifest)
	if err != nil {
		return CropResult{}, err
	}
	if resumeReset {
		rep.Warning("Input or encode settings changed since the interrupted encode; discarded stale resume state and starting over")
	}
	phases.end()

	var displayPath string
	if gated {
		// SSIMU2 runs still need the display model: each title warms up with
		// CVVDP probes to calibrate its SSIMU2 offset (see encode/tq_calibration.go).
		displayPath, err = quality.EnsureDisplayModel(workDir, vidInf, cfg.CVVDPDisplay)
		if err != nil {
			return CropResult{}, err
		}
		gateInput.DisplayPath = displayPath

		phases.start("Grain gate")
		treatment, err := encode.ResolveGrainTreatment(ctx, cfg.GrainTreatment, encCfg, gateInput)
		phases.end()
		if err != nil {
			return CropResult{}, fmt.Errorf("grain treatment failed: %w", err)
		}
		newDenoise, newTable := treatmentIdentity(treatment)
		oldDenoise, oldTable := treatmentIdentity(recorded)
		if newDenoise != oldDenoise || newTable != oldTable {
			// The gate decided just now (or the recorded verdict went away
			// with stale state), so pin the decision in the manifest. Nothing
			// is encoded yet, so overwriting is safe.
			manifest.Denoise, manifest.GrainTable = newDenoise, newTable
			if err := chunk.WriteResumeManifest(workDir, manifest); err != nil {
				return CropResult{}, err
			}
		}
		encCfg.Denoise = treatment.Denoise
		encCfg.GrainTable = nil
		if treatment.TablePath != "" {
			tablePath := treatment.TablePath
			encCfg.GrainTable = &tablePath
		}
		grainStats = treatment.Stats
		perfc.SetGrainTreatment(grainStats)
		perfc.UpdateMeta(func(m *perf.Meta) { m.Denoise = encCfg.Denoise })
		for _, line := range encode.GrainTreatmentSummary(grainStats) {
			rep.StageProgress(reporter.StageProgress{Stage: "Encoding", State: "done", Message: line})
		}
	}

	resumeInfo, err := chunk.GetResume(workDir)
	if err != nil {
		return CropResult{}, fmt.Errorf("failed to load resume info: %w", err)
	}
	resumedFrames := resumeInfo.Validate(workDir, chunks).TotalEncodedFrames()

	// Adaptive encoding starts conservatively, tests higher worker counts by
	// throughput, and backs off on real RAM/swap pressure.
	maxWorkers := encode.MaxAdaptiveWorkers()
	perfc.UpdateMeta(func(m *perf.Meta) {
		m.Frames = vidInf.Frames
		m.Chunks = len(chunks)
	})
	rep.StageProgress(reporter.StageProgress{
		Stage: "encoding", Lane: "video",
		Message: fmt.Sprintf("Starting adaptive chunked encoding with up to %d workers", maxWorkers),
	})
	rep.EncodingStarted(uint64(vidInf.Frames))

	startTime := time.Now()
	type speedSample struct {
		at     time.Time
		frames int
	}
	var speedSamples []speedSample
	const recentSpeedWindow = 60 * time.Second

	var audio *audioJob
	progressCallback := func(progress worker.Progress) {
		if audio != nil {
			audio.report(rep)
		}
		// Calculate average speed, recent rolling speed, and ETA.
		elapsed := time.Since(startTime)
		var speed float32
		var recentSpeed float32
		var eta time.Duration

		runFramesComplete := progress.FramesComplete - resumedFrames
		if runFramesComplete < 0 {
			runFramesComplete = 0
		}
		if elapsed.Seconds() > 0 && runFramesComplete > 0 {
			// Speed is based only on frames encoded in this run. Resume frames count
			// toward percent complete, but not toward current encode speed/ETA.
			videoSeconds := float64(runFramesComplete) / fps
			speed = float32(videoSeconds / elapsed.Seconds())

			now := time.Now()
			speedSamples = append(speedSamples, speedSample{at: now, frames: runFramesComplete})
			cutoff := now.Add(-recentSpeedWindow)
			first := 0
			for first < len(speedSamples)-1 && speedSamples[first].at.Before(cutoff) {
				first++
			}
			if first > 0 {
				speedSamples = speedSamples[first:]
			}
			if len(speedSamples) >= 2 {
				oldest := speedSamples[0]
				newest := speedSamples[len(speedSamples)-1]
				framesDelta := newest.frames - oldest.frames
				secondsDelta := newest.at.Sub(oldest.at).Seconds()
				if framesDelta > 0 && secondsDelta > 0 {
					recentSpeed = float32((float64(framesDelta) / fps) / secondsDelta)
				}
			}

			// A local estimate needs advancing recent samples after calibration.
			if recentSpeed > 0 && elapsed >= 30*time.Second && (cfg.QualityMode != config.QualityModeTarget || progress.CalibrationReady) && progress.ChunksComplete >= 3 && progress.Percent() >= 10 {
				remainingFrames := progress.FramesTotal - progress.FramesComplete
				remainingVideoSeconds := float64(remainingFrames) / fps
				eta = time.Duration(remainingVideoSeconds/float64(recentSpeed)) * time.Second
			}
		}

		unit := "encoded frames"
		if cfg.QualityMode == config.QualityModeTarget {
			unit = "accepted frames"
		}
		rep.EncodingProgress(reporter.ProgressSnapshot{
			FrameUnit:    unit,
			Calibrating:  cfg.QualityMode == config.QualityModeTarget && !progress.CalibrationReady,
			CurrentFrame: uint64(progress.FramesComplete),
			TotalFrames:  uint64(progress.FramesTotal),
			Percent:      float32(progress.Percent()),
			Speed:        speed,
			RecentSpeed:  recentSpeed,
			FPS:          speed * float32(fps),
			InFlight:     progress.InFlight, EncodeSlotWaitSeconds: progress.EncodeSlotWaitSeconds,
			ETA:            eta,
			ChunksComplete: progress.ChunksComplete,
			ChunksTotal:    progress.ChunksTotal,
			ActiveWorkers:  progress.ActiveWorkers,
			TargetWorkers:  progress.TargetWorkers,
			MaxWorkers:     progress.MaxWorkers,
			Probing:        progress.Probing, Scoring: progress.Scoring, Finishing: progress.Finishing,
		})

		perfc.RecordWorkerSample(perf.WorkerSample{
			Active:                progress.ActiveWorkers,
			Target:                progress.TargetWorkers,
			Max:                   progress.MaxWorkers,
			InFlight:              progress.InFlight,
			ChunksComplete:        progress.ChunksComplete,
			FramesComplete:        progress.FramesComplete,
			EncodeSlotWaitSeconds: progress.EncodeSlotWaitSeconds,
		})
	}

	// ========================================================================
	// PHASE 2: Run video encoding and audio encoding in parallel
	// ========================================================================
	encodeCtx, cancelEncode := context.WithCancel(ctx)
	defer cancelEncode()

	audio = startAudioJob(encodeCtx, cancelEncode, inputPath, workDir, audioStreams, videoProps.DurationSecs, rep, perfc)
	// Every return path must stop audio and join its goroutine; canceling
	// first keeps the join prompt on error returns.
	defer func() {
		cancelEncode()
		_, _ = audio.join()
	}()

	// Run parallel video encode
	phases.start("Video encoding")
	var encodeErr error
	if cfg.QualityMode == config.QualityModeTarget {
		metric := probeMetricFor(cfg, vidInf)
		tqTarget, tqTolerance := cfg.TargetQualityTarget, cfg.TargetQualityTolerance
		if metric == quality.MetricCVVDP {
			rep.Verbose(fmt.Sprintf("Target-quality CVVDP: target %.2f +/- %.2f JOD, CRF range %s, initial CRF %s with adaptive priors, whole-chunk probes (every probe scores the full chunk), metric workers %d, display %s", tqTarget, tqTolerance, cfg.CRFSearchRange, quality.FormatCRF(qualitySetting), cfg.MetricWorkers, displayPath))
			rep.Verbose(cvvdpDisplaySummary(cfg, vidInf))
		} else {
			tqTarget, tqTolerance = quality.SSIMU2Target, quality.SSIMU2Tolerance
			rep.Verbose(fmt.Sprintf("Target-quality SSIMULACRA2 (SDR <=1080p): target %.1f +/- %.1f after per-title CVVDP warmup calibration, CRF range %s, initial CRF %s with adaptive priors, whole-chunk probes (every probe scores the full chunk), metric workers %d", tqTarget, tqTolerance, cfg.CRFSearchRange, quality.FormatCRF(qualitySetting), cfg.MetricWorkers))
		}
		var tqStats *perf.TargetQualityStats
		_, tqStats, encodeErr = encodeTarget(
			encodeCtx,
			chunks,
			inputPath,
			vidInf,
			encCfg,
			workDir,
			cropRect,
			progressCallback,
			encode.TargetQualityConfig{
				Metric:         metric,
				Target:         tqTarget,
				Tolerance:      tqTolerance,
				CRFMin:         cfg.CRFSearchMin,
				CRFMax:         cfg.CRFSearchMax,
				MaxProbes:      cfg.TargetQualityMaxProbes,
				MetricWorkers:  cfg.MetricWorkers,
				DisplayPath:    displayPath,
				InitialCRF:     qualitySetting,
				GrainTreatment: grainStats,
				Verbose: func(message string) {
					rep.Verbose(message)
				},
			},
		)
		// Recorded even on error so a failed run's perf.json carries the
		// partial search summary alongside its partial phase timings.
		perfc.SetTargetQuality(tqStats)
	} else {
		_, encodeErr = encode.EncodeAll(
			encodeCtx,
			chunks,
			inputPath,
			vidInf,
			encCfg,
			workDir,
			cropRect,
			progressCallback,
		)
	}

	phases.end()

	if encodeErr != nil {
		// Stop audio when video fails so cancellation/memory pressure returns promptly.
		cancelEncode()
		_, audioErr := audio.join()
		return CropResult{}, encodePipelineError(ctx.Err(), encodeErr, audioErr)
	}

	// Merge IVF files
	rep.StageProgress(reporter.StageProgress{Stage: "Merging", Message: "Merging encoded chunks"})
	phases.start("Video merge")
	if err := chunk.MergeOutput(workDir, vidInf, len(chunks)); err != nil {
		return CropResult{}, fmt.Errorf("video merge failed: %w", err)
	}
	phases.end()

	displayAspect := displayAspectAfterCrop(videoProps, vidInf, cropRect)

	// Wait for audio encoding to complete
	encodedAudio, audioErr := audio.join()
	if audioErr != nil {
		// If the context was canceled, report cancellation instead of audio error
		if ctx.Err() != nil {
			return CropResult{}, ctx.Err()
		}
		return CropResult{}, fmt.Errorf("audio encoding failed: %w", audioErr)
	}

	// Final mux
	rep.StageProgress(reporter.StageProgress{Stage: "Muxing", Message: "Creating final output"})
	phases.start("Final mux")
	if err := chunk.MuxFinal(inputPath, workDir, outputPath, encodedAudio, displayAspect, videoProps.DurationSecs); err != nil {
		return CropResult{}, fmt.Errorf("final mux failed: %w", err)
	}
	phases.end()

	return cropResult, nil
}

// audioJob tracks the background audio extraction that runs concurrently with
// the video encode. join is idempotent, so the happy path joins where the
// results are needed while one deferred cancel+join covers every error return.
type audioJob struct {
	done       chan struct{}
	finishStep func()
	streams    []nativeaudio.EncodedStream
	err        error
	joined     bool
	reported   bool
	reporter   reporter.Reporter
}

// startAudioJob begins audio extraction in the background (it only reads the
// source file). The verbose start/stop lines stay on the orchestration
// goroutine and bracket the join, but the perf phase is recorded from inside
// the goroutine when extraction actually finishes -- otherwise its duration
// would span the whole concurrent video encode/merge window (the orchestration
// goroutine only joins after merge), massively over-reporting audio cost in
// perf.json. An extraction error cancels the shared encode context so the
// video encode stops promptly.
func startAudioJob(ctx context.Context, cancel context.CancelFunc, inputPath, workDir string, streams []media.AudioStreamInfo, videoDurationSecs float64, rep reporter.Reporter, perfc *perf.Collector) *audioJob {
	job := &audioJob{done: make(chan struct{}), finishStep: func() {}, reporter: rep}
	if len(streams) == 0 {
		job.reported = true
		close(job.done)
		return job
	}
	rep.StageProgress(reporter.StageProgress{Lane: "audio", Stage: "audio", Message: fmt.Sprintf("Extracting %d audio tracks", len(streams))})
	job.finishStep = startVerboseStep(rep, "Audio extraction")
	phaseStart := time.Now()
	go func() {
		defer close(job.done)
		job.streams, job.err = chunk.ExtractAudio(ctx, inputPath, workDir, streams, videoDurationSecs)
		perfc.RecordPhase("Audio extraction", phaseStart, time.Now())
		if job.err != nil {
			cancel()
		}
	}()
	return job
}

// join waits for extraction to finish and closes its verbose step. Safe to
// call repeatedly, but only from the orchestration goroutine.
func (a *audioJob) join() ([]nativeaudio.EncodedStream, error) {
	if !a.joined {
		<-a.done
		a.finishStep()
		a.report(a.reporter)
		a.joined = true
	}
	return a.streams, a.err
}

// report is called only on the serialized video-reporting path or after it
// joins. The audio goroutine never writes to a reporter or wire concurrently.
func (a *audioJob) report(rep reporter.Reporter) {
	if a.reported || rep == nil {
		return
	}
	select {
	case <-a.done:
		a.reported = true
		state, message := "done", "Audio extraction finished"
		if a.err != nil {
			state, message = "failed", a.err.Error()
		}
		rep.StageProgress(reporter.StageProgress{Lane: "audio", Stage: "audio", State: state, Message: message})
	default:
	}
}

func cvvdpDisplaySummary(cfg *config.Config, inf *video.Info) string {
	if cfg.CVVDPDisplay != "" {
		return fmt.Sprintf("CVVDP display model: model=%s, override=%s", quality.DisplayModelKey, cfg.CVVDPDisplay)
	}
	if inf != nil && inf.TransferCharacteristics != nil && (*inf.TransferCharacteristics == 16 || *inf.TransferCharacteristics == 18) {
		return fmt.Sprintf("CVVDP display model: model=%s, generated HDR normal-viewing model, ppd~75, resolution=3840x2160, peak=1000 nits, contrast=1000000, ambient=10 lux", quality.DisplayModelKey)
	}
	return fmt.Sprintf("CVVDP display model: model=%s, generated SDR normal-viewing model, ppd~75, resolution=3840x2160, peak=200 nits, contrast=1000, ambient=100 lux", quality.DisplayModelKey)
}

func encodePipelineError(parentErr, encodeErr, audioErr error) error {
	// If the parent context was canceled (e.g. user pressed Ctrl+C), return that
	// directly so the caller can handle it gracefully.
	if parentErr != nil {
		return parentErr
	}
	if errors.Is(encodeErr, encode.ErrMemoryPressure) {
		return fmt.Errorf("chunked encoding failed: %w", encodeErr)
	}

	// Audio runs under the same child context as video. When video fails first,
	// Reel cancels that child context and audio often reports only "context canceled".
	// Do not let that cleanup error hide the real video failure.
	if audioErr != nil && errors.Is(encodeErr, context.Canceled) && !errors.Is(audioErr, context.Canceled) {
		return fmt.Errorf("audio encoding failed: %w", audioErr)
	}
	return fmt.Errorf("chunked encoding failed: %w", encodeErr)
}

// displayAspectAfterCrop returns the display aspect ratio to signal after cropping anamorphic sources.
func displayAspectAfterCrop(props *media.VideoProperties, inf *video.Info, cropRect *video.CropRect) string {
	width, height, sarNum, sarDen := aspectInputs(props, inf)
	if sarNum == 0 || sarDen == 0 || sarNum == sarDen {
		return ""
	}

	if cropRect != nil {
		width = cropRect.Width
		height = cropRect.Height
	}
	if width == 0 || height == 0 {
		return ""
	}

	aspect := displayAspectRatio(width, height, sarNum, sarDen)
	if aspect == nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", aspect[0], aspect[1])
}

func expectedDisplayAspectAfterCrop(props *media.VideoProperties, width, height uint32) *[2]uint32 {
	if props == nil || props.SampleAspectRatioNum == 0 || props.SampleAspectRatioDen == 0 || props.SampleAspectRatioNum == props.SampleAspectRatioDen {
		return nil
	}
	return displayAspectRatio(width, height, props.SampleAspectRatioNum, props.SampleAspectRatioDen)
}

func displayAspectRatio(width, height, sarNum, sarDen uint32) *[2]uint32 {
	if width == 0 || height == 0 || sarNum == 0 || sarDen == 0 {
		return nil
	}
	num := uint64(width) * uint64(sarNum)
	den := uint64(height) * uint64(sarDen)
	g := gcd(num, den)
	return &[2]uint32{uint32(num / g), uint32(den / g)}
}

func aspectInputs(props *media.VideoProperties, inf *video.Info) (width, height, sarNum, sarDen uint32) {
	if props != nil {
		width = props.Width
		height = props.Height
		sarNum = props.SampleAspectRatioNum
		sarDen = props.SampleAspectRatioDen
	}
	if inf != nil {
		if width == 0 {
			width = inf.Width
		}
		if height == 0 {
			height = inf.Height
		}
		if sarNum == 0 || sarDen == 0 {
			sarNum = inf.SampleAspectRatioNum
			sarDen = inf.SampleAspectRatioDen
		}
	}
	return width, height, sarNum, sarDen
}

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// treatmentIdentity is what the resume manifest records about a treatment:
// the two values that change the encoded bits.
func treatmentIdentity(t encode.GrainTreatment) (denoise, table string) {
	if t.Stats == nil {
		return "", ""
	}
	return t.Stats.Denoise, t.Stats.GrainTable
}

func buildResumeManifest(
	inputPath string,
	vidInf *video.Info,
	cfg *config.Config,
	chunks []chunk.Chunk,
	cropFilter string,
	chunkDuration float64,
	qualitySetting float32,
	treatment encode.GrainTreatment,
) (chunk.ResumeManifest, error) {
	stat, err := os.Stat(inputPath)
	if err != nil {
		return chunk.ResumeManifest{}, fmt.Errorf("failed to stat input for resume manifest: %w", err)
	}
	denoise, grainTableRef := treatmentIdentity(treatment)
	if cfg.QualityMode != config.QualityModeTarget {
		// Fixed-CRF mode never gates, so only the experimental flags apply.
		denoise, grainTableRef = cfg.Denoise, cfg.GrainTable
	}
	return chunk.ResumeManifest{
		InputPath:             chunk.CanonicalInputPath(inputPath),
		InputSize:             stat.Size(),
		InputModTimeUnixNano:  stat.ModTime().UnixNano(),
		Width:                 vidInf.Width,
		Height:                vidInf.Height,
		FPSNum:                vidInf.FPSNum,
		FPSDen:                vidInf.FPSDen,
		Frames:                vidInf.Frames,
		CropFilter:            cropFilter,
		QualityMode:           cfg.QualityMode,
		Quality:               qualitySetting,
		TargetQuality:         cfg.TargetQuality,
		CRFSearchRange:        cfg.CRFSearchRange,
		Preset:                cfg.SVTAV1Preset,
		Tune:                  cfg.SVTAV1Tune,
		ACBias:                cfg.SVTAV1ACBias,
		EnableVarianceBoost:   cfg.SVTAV1EnableVarianceBoost,
		VarianceBoostStrength: cfg.SVTAV1VarianceBoostStrength,
		VarianceOctile:        cfg.SVTAV1VarianceOctile,
		GrainTreatment:        cfg.GrainTreatment,
		Denoise:               denoise,
		GrainTable:            grainTableRef,
		ChunkDurationSecs:     chunkDuration,
		ChunkFingerprint:      chunk.ChunkFingerprint(chunks),
	}, nil
}

func parseCropFilter(filter string, srcWidth, srcHeight uint32) (*video.CropRect, error) {
	var w, h, x, y uint32
	_, err := fmt.Sscanf(filter, "crop=%d:%d:%d:%d", &w, &h, &x, &y)
	if err != nil {
		return nil, err
	}
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("width and height must be non-zero")
	}
	if x > srcWidth || w > srcWidth-x || y > srcHeight || h > srcHeight-y {
		return nil, fmt.Errorf("rectangle %dx%d+%d+%d exceeds source %dx%d", w, h, x, y, srcWidth, srcHeight)
	}
	if x%2 != 0 || y%2 != 0 || w%2 != 0 || h%2 != 0 {
		return nil, fmt.Errorf("YUV420 crop offsets and dimensions must be even")
	}

	return &video.CropRect{X: x, Y: y, Width: w, Height: h}, nil
}

// CheckChunkedDependencies verifies that required tools are available.
func CheckChunkedDependencies() error {
	return nil
}
