package transcription

import _ "embed"

const (
	whisperXCommand        = "uvx"
	whisperXPackage        = "whisperx"
	transcriptionProfileID = "whisperx_wrapper_v2"

	whisperXBatchSize    = 16
	whisperXVADChunkSize = 30
	whisperXVADOnset     = 0.500
	whisperXVADOffset    = 0.363
)

//go:embed whisperx_wrapper.py
var whisperXWrapperScript string
