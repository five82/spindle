// Package ripping orchestrates the MakeMKV ripping stage for queued discs.
//
// The handler primes queue progress, streams MakeMKV updates back into the
// store, manages staging-directory layout, handles drive ejection, and emits
// notifications when ripping starts or finishes. When hardware or MakeMKV isn't
// available, it can synthesize placeholder outputs so the rest of the workflow
// keeps functioning in development environments.
//
// Prior to invoking MakeMKV the ripper also persists a deterministic selection
// rule that keeps the highest-quality English primary audio stream. This
// ensures consistent audio contents without requiring users to manage global
// MakeMKV settings by hand.
//
// Once MakeMKV produces the MKV container, the ripper re-runs ffprobe to
// inventory audio streams and then remuxes the working copy with ffmpeg. The
// remux keeps only the primary English audio track while dropping other audio.
//
// When the rip cache is enabled, raw rips are stored unchanged in the cache and
// copied into the staging directory for audio selection. The cache remains
// immutable so future runs can always re-derive picks.
//
// Centralize new ripping behaviours here so the workflow manager interacts with
// a single, well-tested abstraction.
package ripping
