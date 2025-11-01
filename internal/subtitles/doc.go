// Package subtitles generates external subtitle files using the WhisperX
// transcription/alignment toolchain. It invokes WhisperX (on GPU when
// config.WhisperXCUDAEnabled is true, otherwise on CPU), then reshapes the
// aligned output to satisfy Netflix subtitle guidelines (line length, reading
// speed, cue durations) so both the workflow manager and CLI can produce
// Plex-compatible subtitles on demand.
package subtitles
