package whisperx

// Config captures runtime settings for WhisperX operations.
type Config struct {
	// Model is the WhisperX model to use (e.g., "large-v3-turbo").
	Model string
	// CUDAEnabled enables GPU acceleration.
	CUDAEnabled bool
	// VADMethod selects the voice activity detection method ("silero" or "pyannote").
	VADMethod string
	// HFToken is the Hugging Face token for pyannote VAD.
	HFToken string
}

// WhisperX configuration constants.
const (
	DefaultModel      = "large-v3"
	CUDAIndexURL      = "https://download.pytorch.org/whl/cu128"
	PypiIndexURL      = "https://pypi.org/simple"
	BatchSize         = "4"
	ChunkSize         = "15"
	VADOnset          = "0.08"
	VADOffset         = "0.07"
	BeamSize          = "10"
	BestOf            = "10"
	Temperature       = "0.0"
	Patience          = "1.0"
	SegmentResolution = "sentence"
	OutputFormat      = "all"
	CPUDevice         = "cpu"
	CUDADevice        = "cuda"
	CPUComputeType    = "float32"
	VADMethodPyannote = "pyannote"
	VADMethodSilero   = "silero"
)

// Command names for external tools.
const (
	UVXCommand    = "uvx"
	FFmpegCommand = "ffmpeg"
)
