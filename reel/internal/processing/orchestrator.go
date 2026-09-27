package processing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/five82/reel/internal/config"
	encodepipe "github.com/five82/reel/internal/encode"
	"github.com/five82/reel/internal/encoder"
	"github.com/five82/reel/internal/media"
	"github.com/five82/reel/internal/perf"
	"github.com/five82/reel/internal/quality"
	"github.com/five82/reel/internal/reporter"
	"github.com/five82/reel/internal/util"
	"github.com/five82/reel/internal/validation"
	"github.com/five82/reel/internal/video"
)

// EncodeResult contains the result of a single file encode.
type EncodeResult struct {
	Filename          string
	Duration          time.Duration
	InputSize         uint64
	OutputSize        uint64
	InputVideoSize    uint64
	OutputVideoSize   uint64
	VideoDurationSecs float64
	EncodingSpeed     float32
	ValidationPassed  bool
	ValidationSteps   []validation.ValidationStep
	Stats             *perf.Report
}

// ProcessVideos orchestrates encoding for a list of video files.
func ProcessVideos(
	ctx context.Context,
	cfg *config.Config,
	filesToProcess []string,
	targetFilenameOverride string,
	rep reporter.Reporter,
) ([]EncodeResult, error) {
	if rep == nil {
		rep = reporter.NullReporter{}
	}

	var results []EncodeResult

	// Emit hardware information
	sysInfo := util.GetSystemInfo()
	rep.Hardware(reporter.HardwareSummary{
		Hostname: sysInfo.Hostname,
	})

	// Show batch initialization for multiple files
	if len(filesToProcess) > 1 {
		var fileNames []string
		for _, f := range filesToProcess {
			fileNames = append(fileNames, util.GetFilename(f))
		}
		rep.BatchStarted(reporter.BatchStartInfo{
			TotalFiles: len(filesToProcess),
			FileList:   fileNames,
			OutputDir:  cfg.OutputDir,
		})
	}

	for fileIdx, inputPath := range filesToProcess {
		// Check for cancellation before starting each file
		if ctx.Err() != nil {
			rep.Warning(fmt.Sprintf("Encoding cancelled: %v", ctx.Err()))
			break
		}

		fileStartTime := time.Now()
		perfc := perf.New()

		// Show file progress for multiple files
		if len(filesToProcess) > 1 {
			rep.FileProgress(reporter.FileProgressContext{
				CurrentFile: fileIdx + 1,
				TotalFiles:  len(filesToProcess),
			})
		}

		inputFilename := util.GetFilename(inputPath)

		// Determine output path
		override := ""
		if len(filesToProcess) == 1 && targetFilenameOverride != "" {
			override = targetFilenameOverride
		}
		outputPath := util.ResolveOutputPath(inputPath, cfg.OutputDir, override)

		// Skip if output exists
		if util.FileExists(outputPath) {
			rep.Warning(fmt.Sprintf("Output file already exists: %s. Skipping encode.", outputPath))
			continue
		}

		// Analyze video properties (container/codec parameters).
		finishStep := startPhase(perfc, rep, "Video property analysis")
		videoProps, err := media.GetVideoProperties(inputPath)
		finishStep()
		if err != nil {
			rep.Error(reporter.ReporterError{
				Title:      "Analysis Error",
				Message:    fmt.Sprintf("Could not analyze %s: %v", inputFilename, err),
				Context:    fmt.Sprintf("File: %s", inputPath),
				Suggestion: "Check if the file is a valid video format",
			})
			continue
		}

		// Probe the decoded first frame once and share the result with both HDR
		// analysis and the chunked pipeline. The first-frame decode is the
		// dominant media-probe cost (~0.7s on 4K); probing it twice (once here
		// for HDR, again inside ProcessChunked) was pure waste. vidInf is passed
		// into ProcessChunked instead of letting it re-probe.
		finishStep = startPhase(perfc, rep, "Video probe")
		vidInf, err := video.Probe(inputPath)
		finishStep()
		if err != nil {
			rep.Error(reporter.ReporterError{
				Title:      "Analysis Error",
				Message:    fmt.Sprintf("Could not probe video for %s: %v", inputFilename, err),
				Context:    fmt.Sprintf("File: %s", inputPath),
				Suggestion: "Check if the file is a valid video format",
			})
			continue
		}

		finishStep = startPhase(perfc, rep, "HDR analysis")
		refinedHDR := media.RefineHDR(videoProps.HDRInfo, vidInf)
		finishStep()
		hdrInfo := &refinedHDR

		// Determine per-file settings that depend on video resolution.
		fileCfg := *cfg
		fileCfg.MetricWorkers = cfg.MetricWorkersForWidth(videoProps.Width)
		quality, _ := determineQualitySettings(videoProps, &fileCfg)
		isHDR := hdrInfo.IsHDR

		// Get audio info. Channels are derived from the stream info rather than
		// re-probed, so the file is opened once for audio analysis.
		finishStep = startPhase(perfc, rep, "Audio analysis")
		audioStreams := GetAudioStreamInfo(inputPath)
		audioChannels := audioChannelsFromStreams(audioStreams)
		audioDescription := FormatAudioDescription(audioChannels)
		finishStep()

		// Emit initialization event
		rep.Initialization(reporter.InitializationSummary{
			InputFile:        inputFilename,
			OutputFile:       util.GetFilename(outputPath),
			Duration:         util.FormatDuration(videoProps.DurationSecs),
			Resolution:       fmt.Sprintf("%dx%d", videoProps.Width, videoProps.Height),
			DynamicRange:     formatDynamicRange(isHDR),
			AudioDescription: audioDescription,
		})

		// Verbose video analysis details
		rep.Verbose(fmt.Sprintf("Video duration: %.2f seconds", videoProps.DurationSecs))
		if isHDR {
			rep.Verbose(fmt.Sprintf("Color primaries: %s, transfer: %s", hdrInfo.ColourPrimaries, hdrInfo.TransferCharacteristics))
		}

		// Setup encode parameters (for display only)
		encodeParams := setupEncodeParams(&fileCfg, quality, hdrInfo)

		// Format audio description for config display
		audioDescConfig := FormatAudioDescriptionConfig(audioChannels, audioStreams)

		// Emit encoding config
		rep.EncodingConfig(reporter.EncodingConfigSummary{
			Encoder:            "SVT-AV1",
			EncoderVersion:     encoder.SVTVersion(),
			Preset:             fmt.Sprintf("%d", encodeParams.Preset),
			Tune:               fmt.Sprintf("%d", encodeParams.Tune),
			Quality:            formatQualityDescription(vidInf, encodeParams.Quality, &fileCfg),
			PixelFormat:        encodeParams.PixelFormat,
			MatrixCoefficients: encodeParams.MatrixCoefficients,
			AudioCodec:         "Opus",
			AudioDescription:   audioDescConfig,
			SVTAV1Params:       encoder.SvtParamsDisplay(fileCfg.SVTAV1ACBias, fileCfg.SVTAV1EnableVarianceBoost, fileCfg.SVTAV1Tune),
		})

		// Record the file-level facts now that resolution, HDR, and quality are
		// known. Frame count, chunk count, and worker counts are filled in by the
		// chunked pipeline once probing settles them.
		perfc.UpdateMeta(func(m *perf.Meta) {
			m.InputFile = inputFilename
			m.OutputFile = util.GetFilename(outputPath)
			m.Width = videoProps.Width
			m.Height = videoProps.Height
			m.HDR = isHDR
			m.DurationSeconds = videoProps.DurationSecs
			m.QualityMode = fileCfg.QualityMode
			m.Preset = fileCfg.SVTAV1Preset
			m.Denoise = fileCfg.Denoise
			m.MetricWorkers = fileCfg.MetricWorkers
			m.MaxAdaptiveWorkers = encodepipe.MaxAdaptiveWorkers()
			m.Hostname = sysInfo.Hostname
			m.SVTAV1Version = encoder.SVTVersion()
			if fileCfg.QualityMode == config.QualityModeTarget {
				m.TargetQuality = fileCfg.TargetQuality
			} else {
				m.CRF = quality
			}
		})

		// Run chunked encoding with FFmpeg/libav + SVT-AV1 library
		finishStep = startVerboseStep(rep, "Chunked encode pipeline")
		cropResult, encodeError := ProcessChunked(ctx, &fileCfg, inputPath, outputPath, videoProps, vidInf, audioStreams, quality, rep, perfc)
		finishStep()
		encodeSuccess := encodeError == nil

		if !encodeSuccess {
			// A failed encode keeps its work directory for resume whenever the
			// final output was not produced (and always with --keep-workdir), so
			// emit the timing artifact for the partial run too -- phase timing up
			// to the failure is exactly what attribution wants here.
			if cfg.KeepWorkDir || !util.FileExists(outputPath) {
				if err := perfc.Write(); err != nil {
					rep.Verbose(fmt.Sprintf("Could not write perf.json: %v", err))
				}
			}
			// Check if the user canceled the operation (Ctrl+C / SIGTERM)
			if ctx.Err() == context.Canceled {
				rep.OperationComplete(fmt.Sprintf("Encoding canceled: %s", inputFilename))
				rep.OperationComplete("Run the same command to resume from the last completed chunk")
				break
			}
			if errors.Is(encodeError, encodepipe.ErrMemoryPressure) {
				// encodeError wraps ErrMemoryPressure with the trigger and the
				// measured availableFraction/swap-growth numbers from the
				// limiter (see adaptiveLimiter.criticalPressure); surface them
				// here rather than just the sentinel's static text.
				rep.Error(reporter.ReporterError{
					Title:      "Memory Pressure",
					Message:    fmt.Sprintf("Stopped encoding %s because Reel could not keep memory usage safely below RAM", inputFilename),
					Context:    fmt.Sprintf("File: %s; %v", inputPath, encodeError),
					Suggestion: "Run the same command to resume from completed chunks; Reel will restart conservatively and adapt again",
				})
				break
			}
			rep.Error(reporter.ReporterError{
				Title:      "Encoding Error",
				Message:    fmt.Sprintf("Failed to encode %s: %v", inputFilename, encodeError),
				Context:    fmt.Sprintf("File: %s", inputPath),
				Suggestion: logSuggestion(cfg),
			})
			continue
		}

		fileElapsedTime := time.Since(fileStartTime)

		inputSize, _ := util.GetFileSize(inputPath)
		outputSize, _ := util.GetFileSize(outputPath)
		finishStep = startPhase(perfc, rep, "Stream size scan (input)")
		inputVideoSize := videoStreamBytes(inputPath, "input", rep)
		finishStep()
		finishStep = startPhase(perfc, rep, "Stream size scan (output)")
		outputVideoSize := videoStreamBytes(outputPath, "output", rep)
		finishStep()
		encodingSpeed := float32(videoProps.DurationSecs) / float32(fileElapsedTime.Seconds())

		// Calculate expected dimensions after crop
		expectedWidth, expectedHeight := GetOutputDimensions(videoProps.Width, videoProps.Height, cropResult.CropFilter)

		// Validate output
		expectedDims := &[2]uint32{expectedWidth, expectedHeight}
		expectedDuration := videoProps.DurationSecs
		expectedAudioTracks := len(audioChannels)
		expectedDisplayAspect := expectedDisplayAspectAfterCrop(videoProps, expectedWidth, expectedHeight)

		finishStep = startPhase(perfc, rep, "Output validation")
		validationResult, err := validation.ValidateOutputVideo(inputPath, outputPath, validation.Options{
			ExpectedDimensions:    expectedDims,
			ExpectedDuration:      &expectedDuration,
			ExpectedHDR:           &isHDR,
			ExpectedAudioTracks:   &expectedAudioTracks,
			ExpectedDisplayAspect: expectedDisplayAspect,
		})
		finishStep()

		var validationPassed bool
		var validationSteps []validation.ValidationStep
		if err != nil {
			validationPassed = false
			validationSteps = []validation.ValidationStep{
				{Name: "Validation", Passed: false, Details: err.Error()},
			}
		} else {
			validationPassed = validationResult.IsValid()
			for _, step := range validationResult.GetValidationSteps() {
				validationSteps = append(validationSteps, validation.ValidationStep{
					Name:    step.Name,
					Passed:  step.Passed,
					Details: step.Details,
				})
			}
		}

		results = append(results, EncodeResult{
			Filename:          inputFilename,
			Duration:          fileElapsedTime,
			InputSize:         inputSize,
			OutputSize:        outputSize,
			InputVideoSize:    inputVideoSize,
			OutputVideoSize:   outputVideoSize,
			VideoDurationSecs: videoProps.DurationSecs,
			EncodingSpeed:     encodingSpeed,
			ValidationPassed:  validationPassed,
			ValidationSteps:   validationSteps,
			Stats:             perfc.Report(),
		})

		// Emit validation complete
		var repSteps []reporter.ValidationStep
		for _, s := range validationSteps {
			repSteps = append(repSteps, reporter.ValidationStep{
				Name:    s.Name,
				Passed:  s.Passed,
				Details: s.Details,
			})
		}
		rep.ValidationComplete(reporter.ValidationSummary{
			Passed: validationPassed,
			Steps:  repSteps,
		})

		// Emit encoding complete
		rep.EncodingComplete(reporter.EncodingOutcome{
			InputFile:         inputFilename,
			OutputFile:        util.GetFilename(outputPath),
			OriginalSize:      inputSize,
			EncodedSize:       outputSize,
			VideoOriginalSize: inputVideoSize,
			VideoEncodedSize:  outputVideoSize,
			VideoStream:       fmt.Sprintf("AV1 (libsvtav1), %dx%d", expectedWidth, expectedHeight),
			AudioStream:       GenerateAudioResultsDescription(audioChannels, audioStreams),
			TotalTime:         fileElapsedTime,
			AverageSpeed:      encodingSpeed,
			OutputPath:        outputPath,
		})

		// Write the perf.json timing artifact. It lives in the work directory,
		// which only survives when the user keeps it, so only write it then.
		if cfg.KeepWorkDir {
			if err := perfc.Write(); err != nil {
				rep.Verbose(fmt.Sprintf("Could not write perf.json: %v", err))
			} else {
				rep.Verbose("Wrote perf.json timing artifact to work directory")
			}
		}

		// Cooldown between encodes
		if len(filesToProcess) > 1 && fileIdx < len(filesToProcess)-1 && cfg.EncodeCooldownSecs > 0 {
			time.Sleep(time.Duration(cfg.EncodeCooldownSecs) * time.Second)
		}
	}

	// Generate summary
	switch len(results) {
	case 0:
		rep.Warning("No files were successfully encoded")
	case 1:
		rep.OperationComplete(fmt.Sprintf("Successfully encoded %s", results[0].Filename))
	default:
		// Calculate totals
		var totalDuration time.Duration
		var totalOriginalSize, totalEncodedSize uint64
		var totalVideoDuration float64
		var fileResults []reporter.FileResult
		validationPassedCount := 0

		for _, r := range results {
			totalDuration += r.Duration
			totalOriginalSize += r.InputSize
			totalEncodedSize += r.OutputSize
			totalVideoDuration += r.VideoDurationSecs
			reduction := util.CalculateSizeReduction(r.InputSize, r.OutputSize)
			fileResults = append(fileResults, reporter.FileResult{
				Filename:  r.Filename,
				Reduction: reduction,
			})
			if r.ValidationPassed {
				validationPassedCount++
			}
		}

		avgSpeed := float32(0)
		if totalDuration.Seconds() > 0 {
			avgSpeed = float32(totalVideoDuration / totalDuration.Seconds())
		}

		rep.BatchComplete(reporter.BatchSummary{
			SuccessfulCount:       len(results),
			TotalFiles:            len(filesToProcess),
			TotalOriginalSize:     totalOriginalSize,
			TotalEncodedSize:      totalEncodedSize,
			TotalDuration:         totalDuration,
			AverageSpeed:          avgSpeed,
			FileResults:           fileResults,
			ValidationPassedCount: validationPassedCount,
			ValidationFailedCount: len(results) - validationPassedCount,
		})
	}

	return results, nil
}

func logSuggestion(cfg *config.Config) string {
	if cfg == nil || cfg.LogFile == "" {
		return ""
	}
	return fmt.Sprintf("Check the log for more details: %s", cfg.LogFile)
}

func videoStreamBytes(path, label string, rep reporter.Reporter) uint64 {
	bytes, err := media.GetVideoStreamBytes(path)
	if err != nil {
		rep.Verbose(fmt.Sprintf("Could not calculate %s video stream size: %v", label, err))
		return 0
	}
	return bytes
}

// determineQualitySettings returns the CRF quality setting based on video resolution.
func determineQualitySettings(props *media.VideoProperties, cfg *config.Config) (float32, string) {
	return cfg.CRFForWidth(props.Width), ""
}

func formatDynamicRange(isHDR bool) string {
	if isHDR {
		return "HDR"
	}
	return "SDR"
}

func formatQualityDescription(inf *video.Info, crf float32, cfg *config.Config) string {
	width := inf.Width
	if cfg.QualityMode == config.QualityModeTarget {
		if probeMetricFor(cfg, inf) == quality.MetricSSIMU2 {
			return fmt.Sprintf("SSIMULACRA2 target %.1f-%.1f (auto for SDR <=1080p; initial CRF %s with adaptive priors, whole-chunk probes, CRF search %s, metric workers %d)", quality.SSIMU2Target-quality.SSIMU2Tolerance, quality.SSIMU2Target+quality.SSIMU2Tolerance, quality.FormatCRF(crf), cfg.CRFSearchRange, cfg.MetricWorkers)
		}
		return fmt.Sprintf("CVVDP target %.2f-%.2f JOD (initial CRF %s with adaptive priors, whole-chunk probes, CRF search %s, metric workers %d)", cfg.TargetQualityMin, cfg.TargetQualityMax, quality.FormatCRF(crf), cfg.CRFSearchRange, cfg.MetricWorkers)
	}
	var tier string
	if width >= config.UHDWidthThreshold {
		tier = "UHD"
	} else if width >= config.HDWidthThreshold {
		tier = "HD"
	} else {
		tier = "SD"
	}
	return fmt.Sprintf("CRF %s (%s)", quality.FormatCRF(crf), tier)
}

type encodeParams struct {
	Quality            float32
	Preset             uint8
	Tune               uint8
	PixelFormat        string
	MatrixCoefficients string
}

func setupEncodeParams(
	cfg *config.Config,
	quality float32,
	hdrInfo *media.HDRInfo,
) *encodeParams {
	params := &encodeParams{
		Quality:     quality,
		Preset:      cfg.SVTAV1Preset,
		Tune:        cfg.SVTAV1Tune,
		PixelFormat: "yuv420p10le",
	}

	// Set matrix coefficients based on HDR
	if hdrInfo.IsHDR {
		params.MatrixCoefficients = hdrInfo.MatrixCoefficients
		if params.MatrixCoefficients == "" {
			params.MatrixCoefficients = "bt2020nc"
		}
	} else {
		params.MatrixCoefficients = "bt709"
	}

	return params
}
