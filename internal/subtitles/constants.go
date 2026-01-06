package subtitles

const (
	progressStageGenerating = "Generating AI subtitles"

	whisperXCommand        = "uvx"
	ffmpegCommand          = "ffmpeg"
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
	stableTSCommand        = "uvx"
	stableTSPackage        = "stable-ts-whisperless"
	whisperXPackage        = "whisperx"
	ffsubsyncCommand       = "uvx"
	ffsubsyncPackage       = "ffsubsync"
)

const (
	whisperXVADMethodPyannote = "pyannote"
	whisperXVADMethodSilero   = "silero"
)
