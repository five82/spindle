package transcription

import _ "embed"

const parakeetHelperFileName = "parakeet_transcribe.py"

//go:embed parakeet_transcribe.py
var parakeetTranscribeScript string
