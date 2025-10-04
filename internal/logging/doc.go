// Package logging assembles structured slog loggers and formatting helpers used
// across Spindle services.
//
// It owns the configurable console/JSON handlers, centralizes level and output
// plumbing, and exposes context-aware helpers so stage code can automatically
// tag log lines with queue item IDs, stages, and correlation IDs. The package
// also provides a no-op logger for tests and wiring code that cannot fail.
//
// Prefer these constructors over hand-rolled slog setup to ensure new
// components emit data with the same shape and routing guarantees as the rest
// of the system.
package logging
