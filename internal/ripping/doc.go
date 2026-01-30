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
// When the rip cache is enabled, rips are written directly to the cache path.
// Downstream stages (encoding, audio analysis) read from the cache path without
// any intermediate copy.
//
// Centralize new ripping behaviours here so the workflow manager interacts with
// a single, well-tested abstraction.
package ripping
