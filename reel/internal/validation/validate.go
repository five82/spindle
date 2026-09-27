package validation

import (
	"fmt"
	"math"
	"strings"

	"github.com/five82/reel/internal/media"
)

const (
	// durationToleranceSecs is the maximum allowed difference in duration between input and output.
	durationToleranceSecs = 1.0
	// maxSyncDriftMs is the maximum allowed audio/video sync drift in milliseconds.
	maxSyncDriftMs = 100.0
)

// Options contains optional parameters for validation.
type Options struct {
	ExpectedDimensions    *[2]uint32
	ExpectedDuration      *float64
	ExpectedHDR           *bool
	ExpectedAudioTracks   *int
	ExpectedAudioChannels []uint32
	ExpectedDisplayAspect *[2]uint32
}

// ValidateOutputVideo performs comprehensive validation of an encoded video.
func ValidateOutputVideo(inputPath, outputPath string, opts Options) (*Result, error) {
	result := &Result{
		IsCropCorrect:            true,
		IsDurationCorrect:        true,
		IsHDRCorrect:             true,
		IsAudioOpus:              true,
		IsAudioTrackCountCorrect: true,
		IsSyncPreserved:          true,
		IsAspectCorrect:          true,
	}

	// Get output video properties
	outputProps, err := media.GetVideoProperties(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get output video properties: %w", err)
	}

	// Validate video codec (should be AV1)
	result.IsAV1, result.CodecName = validateVideoCodec(outputPath)
	result.Is10Bit, result.BitDepth, result.PixelFormat = validateBitDepth(outputPath)

	// Validate dimensions if expected
	if opts.ExpectedDimensions != nil {
		result.ActualDimensions = &[2]uint32{outputProps.Width, outputProps.Height}
		result.ExpectedDimensions = opts.ExpectedDimensions
		result.IsCropCorrect, result.CropMessage = validateDimensions(
			outputProps.Width, outputProps.Height,
			opts.ExpectedDimensions[0], opts.ExpectedDimensions[1],
		)
	} else {
		result.CropMessage = "No crop validation required"
	}

	// Validate display aspect ratio for anamorphic sources if expected.
	if opts.ExpectedDisplayAspect != nil {
		result.ExpectedDisplayAspect = opts.ExpectedDisplayAspect
		result.ActualDisplayAspect = displayAspect(outputProps.Width, outputProps.Height, outputProps.SampleAspectRatioNum, outputProps.SampleAspectRatioDen)
		result.IsAspectCorrect, result.AspectMessage = validateDisplayAspect(result.ActualDisplayAspect, *opts.ExpectedDisplayAspect)
	} else {
		result.AspectMessage = "Aspect validation skipped"
	}

	// Validate duration if expected
	if opts.ExpectedDuration != nil {
		actualDur := outputProps.DurationSecs
		result.ActualDuration = &actualDur
		result.ExpectedDuration = opts.ExpectedDuration
		result.IsDurationCorrect, result.DurationMessage = validateDuration(actualDur, *opts.ExpectedDuration)
	} else {
		result.DurationMessage = "Duration validation skipped"
	}

	// Validate HDR status if expected using native media probing.
	if opts.ExpectedHDR != nil {
		hdrResult := ValidateHDRStatusWithPath(outputPath, opts.ExpectedHDR)
		result.IsHDRCorrect = hdrResult.IsValid
		result.ActualHDR = hdrResult.ActualHDR
		result.ExpectedHDR = opts.ExpectedHDR
		result.HDRMessage = hdrResult.Message
	} else {
		// No expected HDR, but still detect actual status for reporting
		hdrResult := ValidateHDRStatusWithPath(outputPath, nil)
		result.IsHDRCorrect = true // No expectation means always valid
		result.ActualHDR = hdrResult.ActualHDR
		result.HDRMessage = hdrResult.Message
	}

	// Validate audio
	outputAudioStreams, outputAudioErr := media.GetAudioStreamInfo(outputPath)
	if outputAudioErr != nil {
		result.AudioMessage = "Failed to get audio info"
	} else {
		result.IsAudioOpus, result.IsAudioTrackCountCorrect, result.AudioCodecs, result.AudioMessage = validateAudio(
			outputAudioStreams, opts.ExpectedAudioTracks,
		)
	}

	// Validate A/V sync using each audio track's start relative to video.
	inputAudioStreams, inputAudioErr := media.GetAudioStreamInfo(inputPath)
	switch {
	case inputAudioErr != nil:
		result.IsSyncPreserved = false
		result.SyncMessage = "Failed to get input audio timing"
	case outputAudioErr != nil:
		result.IsSyncPreserved = false
		result.SyncMessage = "Failed to get output audio timing"
	default:
		result.IsSyncPreserved, result.SyncDriftMs, result.SyncMessage = validateSync(inputAudioStreams, outputAudioStreams, outputProps.DurationSecs)
	}

	sourceVideoDuration := outputProps.DurationSecs
	if opts.ExpectedDuration != nil {
		sourceVideoDuration = *opts.ExpectedDuration
	}
	if count, maxOverrun := audioOverruns(inputAudioStreams, sourceVideoDuration); count > 0 {
		remaining, _ := audioOverruns(outputAudioStreams, outputProps.DurationSecs)
		result.SourceTimelineNormalized = outputAudioErr == nil && remaining == 0
		outcome := "output bounded to the video endpoint"
		if !result.SourceTimelineNormalized {
			outcome = "output was not bounded to the video endpoint"
		}
		result.SourceTimelineMessage = fmt.Sprintf(
			"%d source audio track(s) ended up to %.3fs past video; %s",
			count, maxOverrun, outcome,
		)
	}

	return result, nil
}

// validateVideoCodec checks that the output is AV1.
func validateVideoCodec(outputPath string) (bool, string) {
	codecName, err := media.GetVideoCodecName(outputPath)
	if err != nil {
		return false, ""
	}

	isAV1 := strings.Contains(strings.ToLower(codecName), "av1") ||
		strings.Contains(strings.ToLower(codecName), "av01")

	return isAV1, codecName
}

// validateBitDepth checks that the output is 10-bit.
func validateBitDepth(outputPath string) (bool, *uint8, string) {
	props, err := media.GetVideoProperties(outputPath)
	if err != nil {
		return false, nil, ""
	}

	if props.HDRInfo.BitDepth != nil {
		is10Bit := *props.HDRInfo.BitDepth >= 10
		return is10Bit, props.HDRInfo.BitDepth, ""
	}

	// Default to true for AV1 (typically 10-bit)
	defaultDepth := uint8(10)
	return true, &defaultDepth, "yuv420p10le"
}

// validateDimensions checks that dimensions match expected values.
func validateDimensions(actualW, actualH, expectedW, expectedH uint32) (bool, string) {
	if actualW == expectedW && actualH == expectedH {
		return true, fmt.Sprintf("Dimensions match: %dx%d", actualW, actualH)
	}
	return false, fmt.Sprintf("Dimension mismatch: got %dx%d, expected %dx%d",
		actualW, actualH, expectedW, expectedH)
}

// displayAspect calculates the reduced display aspect ratio from dimensions and sample aspect ratio.
func displayAspect(width, height, sarNum, sarDen uint32) *[2]uint32 {
	if width == 0 || height == 0 {
		return nil
	}
	if sarNum == 0 || sarDen == 0 {
		sarNum = 1
		sarDen = 1
	}
	num := uint64(width) * uint64(sarNum)
	den := uint64(height) * uint64(sarDen)
	g := gcd(num, den)
	aspect := [2]uint32{uint32(num / g), uint32(den / g)}
	return &aspect
}

func validateDisplayAspect(actual *[2]uint32, expected [2]uint32) (bool, string) {
	if actual == nil {
		return false, fmt.Sprintf("Display aspect unavailable, expected %d:%d", expected[0], expected[1])
	}
	if actual[0] == expected[0] && actual[1] == expected[1] {
		return true, fmt.Sprintf("Display aspect matches: %d:%d", actual[0], actual[1])
	}
	return false, fmt.Sprintf("Display aspect mismatch: got %d:%d, expected %d:%d", actual[0], actual[1], expected[0], expected[1])
}

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// validateDuration checks that duration is within acceptable tolerance.
func validateDuration(actual, expected float64) (bool, string) {
	diff := math.Abs(actual - expected)

	if diff <= durationToleranceSecs {
		return true, fmt.Sprintf("Duration matches input (%.1fs)", actual)
	}
	return false, fmt.Sprintf("Duration mismatch: got %.1fs, expected %.1fs (diff: %.1fs)",
		actual, expected, diff)
}

// validateAudio checks audio codec and track count.
func validateAudio(streams []media.AudioStreamInfo, expectedTracks *int) (bool, bool, []string, string) {
	isOpus := true
	var codecs []string

	for _, stream := range streams {
		codec := strings.ToLower(stream.CodecName)
		codecs = append(codecs, codec)
		if codec != "opus" {
			isOpus = false
		}
	}

	trackCountCorrect := true
	if expectedTracks != nil {
		trackCountCorrect = len(streams) == *expectedTracks
	}

	var message string
	if len(streams) == 0 {
		message = "No audio tracks"
	} else if len(streams) == 1 {
		if isOpus {
			message = "Audio track is Opus"
		} else {
			message = fmt.Sprintf("Audio track is %s (expected Opus)", codecs[0])
		}
	} else {
		if isOpus {
			message = fmt.Sprintf("%d audio tracks, all Opus", len(streams))
		} else {
			message = fmt.Sprintf("%d audio tracks: %s", len(streams), strings.Join(codecs, ", "))
		}
	}

	return isOpus, trackCountCorrect, codecs, message
}

func audioOverruns(streams []media.AudioStreamInfo, videoDurationSecs float64) (int, float64) {
	count := 0
	maxOverrun := 0.0
	for _, stream := range streams {
		overrun := stream.StartOffsetSecs + stream.DurationSecs - videoDurationSecs
		if overrun <= durationToleranceSecs {
			continue
		}
		count++
		maxOverrun = max(maxOverrun, overrun)
	}
	return count, maxOverrun
}

// validateSync compares each audio track's timing relative to the video stream.
func validateSync(inputStreams, outputStreams []media.AudioStreamInfo, videoDurationSecs float64) (bool, *float64, string) {
	trackCount := min(len(inputStreams), len(outputStreams))
	maxDriftMs := 0.0
	maxTrack := 0
	for i := 0; i < trackCount; i++ {
		driftMs := math.Abs(inputStreams[i].StartOffsetSecs-outputStreams[i].StartOffsetSecs) * 1000
		if driftMs > maxDriftMs {
			maxDriftMs = driftMs
			maxTrack = inputStreams[i].Index + 1
		}
	}

	if len(inputStreams) != len(outputStreams) {
		return false, &maxDriftMs, fmt.Sprintf(
			"Audio/video sync track count mismatch: input has %d tracks, output has %d (maximum compared drift: %.1fms)",
			len(inputStreams), len(outputStreams), maxDriftMs,
		)
	}
	if maxDriftMs > maxSyncDriftMs {
		return false, &maxDriftMs, fmt.Sprintf(
			"Audio/video sync drift too large for track %d: %.1fms (max: %.1fms; maximum drift: %.1fms)",
			maxTrack, maxDriftMs, maxSyncDriftMs, maxDriftMs,
		)
	}
	for i, stream := range outputStreams {
		end := stream.StartOffsetSecs + stream.DurationSecs
		if end > videoDurationSecs+durationToleranceSecs {
			return false, &maxDriftMs, fmt.Sprintf(
				"Audio/video sync track %d ends %.1fs after video (video: %.1fs)",
				i+1, end-videoDurationSecs, videoDurationSecs,
			)
		}
	}
	return true, &maxDriftMs, fmt.Sprintf("Audio/video sync preserved (maximum drift: %.1fms)", maxDriftMs)
}
