// Package daemon coordinates the long-running Spindle process and system
// integration points.
//
// It wires configuration, queue storage, the workflow manager, and the disc
// monitor into a single lifecycle with flock-based locking to prevent multiple
// instances. The daemon exposes queue maintenance helpers, manages manual file
// ingestion, emits dependency health summaries, and owns notifications triggered
// by queue start/completion events.
//
// Keep orchestration logic here: individual workflow steps should live in their
// respective packages while the daemon focuses on startup, shutdown, and high
// level coordination.
package daemon
