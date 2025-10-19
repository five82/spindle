// Package subtitles generates external subtitle files using AI transcription services.
//
// The package coordinates audio extraction, chunk uploads to the Mistral
// Voxtral transcription API, and SRT post-processing so both the workflow
// manager and CLI can produce Plex-compatible subtitles on demand.
package subtitles
