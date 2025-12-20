// Package subtitles generates external subtitle files. The primary path is to
// download reference subtitles from OpenSubtitles (when enabled/configured).
// When OpenSubtitles is unavailable, has no match, or fails, the stage falls
// back to local WhisperX transcription/alignment.
//
// WhisperX execution demuxes the primary audio stream, runs WhisperX (GPU when
// config.Subtitles.WhisperXCUDAEnabled is true, otherwise CPU), then feeds the alignment
// JSON to Stable-TS to regroup phrases and timing before emitting Plex-
// compatible SRT sidecars. If Stable-TS cannot complete, the raw WhisperX SRT
// is copied so subtitle generation never leaves the pipeline empty-handed.
package subtitles
