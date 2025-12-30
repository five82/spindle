// Package commentary detects audio commentary tracks by combining metadata
// heuristics with conservative audio analysis (fingerprint + speech activity).
//
// The detector prioritizes precision over recall: ambiguous candidates are
// dropped unless relative scoring or WhisperX transcript hints can separate
// commentary from audio-description tracks. Commentary detection can be
// disabled or fail-closed when required dependencies are missing. Audio-
// description-like tracks are rejected when speech primarily occurs during
// primary-track silence with low overlap; WhisperX can override ambiguous or
// audio-description candidates when available.
// The primary audio track is always preserved, with commentary tracks kept as
// non-default secondary streams.
package commentary
