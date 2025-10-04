// Package services defines shared utilities consumed by the workflow stage
// handlers and external integrations.
//
// Key responsibilities:
//   - Context helpers that stamp queue item IDs, stage names, and correlation
//     identifiers for logging and tracing.
//   - Structured error markers plus the Wrap helper that translate failures
//     into consistent queue statuses (failed vs review).
//   - Thin abstractions that make command execution and progress streaming from
//     external tools testable.
//
// Use these helpers when wiring new stage logic so operational behaviour (error
// handling, observability, retries) stays uniform across the pipeline.
package services
