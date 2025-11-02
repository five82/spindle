// Package subtitles generates external subtitle files using the WhisperX
// transcription/alignment toolchain. It demuxes the primary audio stream,
// invokes WhisperX (on GPU when config.WhisperXCUDAEnabled is true, otherwise
// on CPU), then feeds the alignment JSON to Stable-TS to regroup phrases and
// timing before emitting Plex-compatible SRT sidecars. If Stable-TS cannot
// complete, the raw WhisperX SRT is copied so subtitle generation never leaves
// the pipeline empty-handed.
package subtitles
