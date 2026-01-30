// Package audioanalysis performs audio track selection and commentary detection.
//
// This stage runs after ripping and before encoding. It handles:
//
//  1. Primary audio selection - selecting the best English audio track
//  2. Commentary detection - identifying commentary tracks via transcription
//
// # Commentary Detection
//
// Commentary tracks are detected through a multi-step process:
//
//  1. Candidate filtering: Find all English 2-channel stereo tracks
//  2. Stereo downmix detection: Compare candidate transcripts to primary audio
//     using cosine similarity. Tracks with similarity > threshold are stereo
//     downmixes, not commentary.
//  3. LLM classification: Send remaining candidate transcripts to an LLM to
//     determine if they contain commentary (people discussing the film) vs
//     other content (audio descriptions, alternate dubs).
//
// # Stage Flow
//
// RIPPING -> AUDIO_ANALYZING -> AUDIO_ANALYZED -> EPISODE_IDENTIFYING/ENCODING
//
// The stage stores analysis results in the queue item's RipSpec for use by
// the encoding and organizing stages.
package audioanalysis
