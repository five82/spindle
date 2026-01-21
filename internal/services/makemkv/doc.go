// Package makemkv mediates access to the MakeMKV CLI used during ripping.
//
// It normalizes command invocation, parses robot-mode progress messages,
// and exposes a testable interface for the ripping stage.
//
// Prefer this package over ad-hoc exec.Command usage when interacting with
// MakeMKV so progress reporting and timeout handling remain consistent.
package makemkv
