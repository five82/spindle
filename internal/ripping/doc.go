// Package ripping orchestrates the MakeMKV ripping stage for queued discs.
//
// The handler primes queue progress, streams MakeMKV updates back into the
// store, manages staging-directory layout, handles drive ejection, and emits
// notifications when ripping starts or finishes. When hardware or MakeMKV isn't
// available, it can synthesize placeholder outputs so the rest of the workflow
// keeps functioning in development environments.
//
// Prior to invoking MakeMKV the ripper also persists a deterministic selection
// rule that keeps the highest-quality English primary audio stream while
// re-enabling commentary tracks flagged by MakeMKV. This ensures consistent
// audio contents without requiring users to manage global MakeMKV settings by
// hand.
//
// Once MakeMKV produces the MKV container, the ripper re-runs ffprobe to
// inventory audio streams and then remuxes the file with ffmpeg. The remux keeps
// a single English primary track (preferring spatial > lossless > lossy mixes)
// plus any detected commentary tracks while dropping other audio. Commentary is
// inferred from MakeMKV/ffprobe metadata and heuristics that treat early English
// stereo mixes as likely commentaries when higher-channel mixes are present.
//
// Centralize new ripping behaviours here so the workflow manager interacts with
// a single, well-tested abstraction.
package ripping
