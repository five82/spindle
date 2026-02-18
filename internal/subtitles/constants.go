package subtitles

// Progress stage labels.
const progressStageGenerating = "Generating AI subtitles"

// Progress percentage allocation for subtitle stage.
// The stage uses 5% for preparation and 90% for generation (5-95%).
const (
	progressPercentAfterPrep = 5.0  // Progress after preparation phase
	progressPercentForGen    = 90.0 // Progress allocated to generation phase
)

// WhisperX configuration (parameters are in services/whisperx package).
const (
	whisperXCommand        = "uvx"
	whisperXCUDAIndexURL   = "https://download.pytorch.org/whl/cu128"
	whisperXPypiIndexURL   = "https://pypi.org/simple"
	whisperXCPUDevice      = "cpu"
	whisperXCUDADevice     = "cuda"
	whisperXCPUComputeType = "float32"
	whisperXPackage        = "whisperx"

	whisperXVADMethodPyannote = "pyannote"
	whisperXVADMethodSilero   = "silero"
)

// External tool commands.
const (
	ffmpegCommand   = "ffmpeg"
	stableTSCommand = "uvx"
	stableTSPackage = "stable-ts-whisperless"
)

// Maximum OpenSubtitles candidates to evaluate for forced subtitle search.
const maxOpenSubtitlesCandidates = 15
