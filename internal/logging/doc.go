// Package logging assembles structured slog loggers and formatting helpers used
// across Spindle services.
//
// It owns the configurable console/JSON handlers, centralizes level and output
// plumbing, and exposes context-aware helpers so stage code can automatically
// tag log lines with queue item IDs, stages, and correlation IDs. The package
// also provides a no-op logger for tests and wiring code that cannot fail.
//
// Logging contract:
//   - INFO: narrative milestones plus decisions that change the final encoded
//     output or delivered subtitle artifacts (track selection, subtitle source,
//     encode preset, output mapping).
//   - WARN: degraded behavior or user action needed (fallbacks, review states).
//   - ERROR: operation failed; will stop or retry (include short error_message).
//   - DEBUG: raw diagnostics, per-candidate scoring, tool payloads, and
//     decisions that do not affect the final encoded file.
//
// Common fields:
//   - progress_stage/progress_percent/progress_message/progress_eta for progress.
//   - decision_type + decision_* for decision logs.
//   - event_type for lifecycle events (stage_start, stage_complete, stage_failure).
//   - error_kind/error_operation/error_detail_path/error_code/error_hint for failures.
//
// Prefer these constructors over hand-rolled slog setup to ensure new
// components emit data with the same shape and routing guarantees as the rest
// of the system.
package logging
