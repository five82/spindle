package subtitles

// Progress stage labels.
const progressStageGenerating = "Generating AI subtitles"

// Progress percentage allocation for subtitle stage.
// The stage uses 5% for preparation and 90% for generation (5-95%).
const (
	progressPercentAfterPrep = 5.0  // Progress after preparation phase
	progressPercentForGen    = 90.0 // Progress allocated to generation phase
)

// External tool commands.
const (
	ffmpegCommand   = "ffmpeg"
	stableTSCommand = "uvx"
	stableTSPackage = "stable-ts-whisperless"
)

// Maximum OpenSubtitles candidates to evaluate for forced subtitle search.
const maxOpenSubtitlesCandidates = 15
