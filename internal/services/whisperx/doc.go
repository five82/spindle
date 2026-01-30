// Package whisperx provides WhisperX transcription utilities for audio analysis.
//
// This package handles:
//   - Audio segment extraction (full file or time-range)
//   - WhisperX transcription invocation
//   - Transcript text extraction from results
//
// The primary use case is commentary track detection in the audio analysis stage,
// but the utilities are generic enough for other transcription needs.
//
// Configuration options (model, CUDA, VAD method) are passed via Config.
package whisperx
