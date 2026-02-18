// Package subtitles generates external subtitle files using WhisperX
// transcription. Regular subtitles always come from WhisperX, which
// demuxes the primary audio stream, runs transcription (GPU when
// config.Subtitles.WhisperXCUDAEnabled is true, otherwise CPU), then feeds
// the alignment JSON to Stable-TS to regroup phrases and timing before
// emitting library-compatible SRT sidecars. A post-transcription filter
// then removes known hallucination patterns (e.g. repeated "Thank you."
// during silence) and trims credits-section noise. If Stable-TS cannot
// complete, the raw WhisperX SRT is copied so subtitle generation never
// leaves the pipeline empty-handed.
//
// Forced (foreign-parts-only) subtitles are still fetched from OpenSubtitles
// when enabled and configured, then aligned against the WhisperX output via
// text-based matching.
//
// # Debugging
//
// Set SPD_DEBUG_SUBTITLES_KEEP=1 to retain intermediate files (demuxed audio,
// raw WhisperX output, alignment JSON) in the item's staging folder. Useful
// for diagnosing transcription or timing issues.
package subtitles
