// Package makemkv mediates access to the MakeMKV CLI used during ripping.
//
// It normalizes command invocation, parses robot-mode progress messages,
// exposes a testable interface for the ripping stage, and manufactures
// placeholder output when MakeMKV is unavailable so developers can exercise the
// workflow end-to-end without hardware.
//
// Prefer this package over ad-hoc exec.Command usage when interacting with
// MakeMKV so progress reporting and timeout handling remain consistent.
package makemkv
