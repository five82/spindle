// Package main hosts the Spindle CLI entrypoint and command graph.
//
// The Cobra-based command tree translates terminal invocations into IPC calls
// against the daemon, queue maintenance operations, log tailing, disc
// identification requests, and configuration scaffolding. It centralizes
// configuration resolution, socket discovery, and structured logging setup so
// subcommands can focus on user experience instead of wiring.
//
// Keep this package lean: add new functionality by extending the internal
// packages first, then surface it through dedicated commands or flags here.
// That separation keeps the CLI declarative while the heavy lifting lives in
// reusable workflow components.
package main
