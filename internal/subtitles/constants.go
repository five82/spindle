package subtitles

// Progress stage labels.
const progressStageGenerating = "Generating AI subtitles"

// Progress percentage allocation for subtitle stage.
// The stage uses 5% for preparation and 90% for generation (5-95%).
const (
	progressPercentAfterPrep = 5.0  // Progress after preparation phase
	progressPercentForGen    = 90.0 // Progress allocated to generation phase
)

// WhisperX configuration.
const (
	whisperXCommand        = "uvx"
	whisperXCUDAIndexURL   = "https://download.pytorch.org/whl/cu128"
	whisperXPypiIndexURL   = "https://pypi.org/simple"
	whisperXModel          = "large-v3"
	whisperXAlignModel     = "WAV2VEC2_ASR_LARGE_LV60K_960H"
	whisperXBatchSize      = "4"
	whisperXChunkSize      = "15"
	whisperXVADOnset       = "0.08"
	whisperXVADOffset      = "0.07"
	whisperXBeamSize       = "10"
	whisperXBestOf         = "10"
	whisperXTemperature    = "0.0"
	whisperXPatience       = "1.0"
	whisperXSegmentRes     = "sentence"
	whisperXOutputFormat   = "all"
	whisperXCPUDevice      = "cpu"
	whisperXCUDADevice     = "cuda"
	whisperXCPUComputeType = "float32"
	whisperXPackage        = "whisperx"

	whisperXVADMethodPyannote = "pyannote"
	whisperXVADMethodSilero   = "silero"
)

// External tool commands.
const (
	ffmpegCommand    = "ffmpeg"
	stableTSCommand  = "uvx"
	stableTSPackage  = "stable-ts-whisperless"
	ffsubsyncCommand = "uvx"
	ffsubsyncPackage = "ffsubsync"
)

// Duration validation thresholds for subtitle/video matching.
const (
	// Intro allowance for subtitle-to-video gap at start/end.
	subtitleIntroAllowanceSeconds = 45.0  // Max gap at end of video (credits)
	subtitleIntroMinimumSeconds   = 5.0   // Min gap at start to trigger exception
	subtitleIntroMaximumSeconds   = 300.0 // Max gap at start (5 min) - larger gaps are suspicious

	// Suspect offset detection thresholds.
	suspectOffsetSeconds        = 60.0
	suspectRuntimeMismatchRatio = 0.07

	// Basic duration tolerance for final validation.
	subtitleDurationToleranceSeconds = 8.0

	// Asymmetric tolerances: credits are normal (no dialogue at end).
	// Subtitle shorter than video: allow up to 10 minutes for credits.
	// Subtitle longer than video: only allow small tolerance (suspicious mismatch).
	earlyDurationCreditsToleranceSeconds = 600.0 // 10 minutes for credits (early check)
	earlyDurationOverlapToleranceSeconds = 60.0  // 1 minute if subtitle is longer (early check)
	postAlignmentCreditsToleranceSeconds = 600.0 // 10 minutes for credits (post-alignment)

	// Maximum OpenSubtitles candidates to evaluate before giving up.
	maxOpenSubtitlesCandidates = 15

	// Segment density validation: minimum cues per minute expected for valid subtitles.
	// Typical movies have 6-12 cues/minute. 2 cues/minute is a very low threshold that
	// catches obviously sparse/incomplete subtitles (like 143 cues for a 126-min movie = 1.1/min).
	minCuesPerMinute = 2.0

	// Coverage validation: minimum percentage of video duration that should have subtitle coverage.
	// A subtitle that ends way before the video (excluding credits) is likely incomplete.
	// This catches cases where subtitles cover only part of the movie.
	minSubtitleCoverageRatio = 0.75 // Subtitle should cover at least 75% of video duration
)
