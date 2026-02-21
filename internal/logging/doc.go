// Package logging assembles structured slog loggers and formatting helpers used
// across Spindle services.
//
// It owns the configurable console/JSON handlers, centralizes level and output
// plumbing, and exposes context-aware helpers so stage code can automatically
// tag log lines with queue item IDs, stages, and correlation IDs. The package
// also provides a no-op logger for tests and wiring code that cannot fail.
//
// # Logging Contract
//
// Level semantics:
//   - INFO: narrative milestones plus decisions that change the final encoded
//     output or delivered subtitle artifacts (track selection, subtitle source,
//     encode preset, output mapping).
//   - WARN: degraded behavior or user action needed (fallbacks, review states).
//   - ERROR: operation failed; will stop or retry.
//   - DEBUG: raw diagnostics, per-candidate scoring, tool payloads, and
//     decisions that do not affect the final encoded file.
//
// # Required Fields by Level
//
// INFO logs must include:
//   - event_type: lifecycle event (e.g., "stage_start", "stage_complete", "status")
//
// WARN logs must include all three fields (the "WARN triad"):
//   - event_type: what happened (e.g., "rip_cache_inspection_failed")
//   - error_hint: actionable next step (e.g., "check rip_cache_dir permissions")
//   - impact: user-facing consequence (e.g., "rip cache bypassed; MakeMKV rip will proceed")
//
// Use WarnWithContext() helper to enforce the WARN triad automatically.
//
// ERROR logs must include:
//   - event_type: what failed
//   - error_hint: actionable next step
//   - error (via logging.Error()): the underlying error
//
// Use ErrorWithContext() helper to enforce error fields automatically.
//
// # Decision Logging
//
// Decision logs record choices that affect output. Required fields:
//   - decision_type: category (e.g., "tmdb_confidence", "commentary", "encoding_job_plan")
//   - decision_result: outcome (e.g., "accepted", "rejected", "applied", "fallback")
//   - decision_reason: why (e.g., "exact_match", "confidence_below_threshold")
//   - decision_options: alternatives considered (e.g., "accept, reject")
//   - decision_selected: chosen value (optional, for explicit selection)
//
// When truncating lists to top-N items, include a *_hidden_count field to
// surface how many entries were omitted (e.g., "candidate_hidden_count": 5).
//
// # Common Fields
//
// Progress: progress_stage, progress_percent, progress_message, progress_eta
// Decision: decision_type, decision_result, decision_reason, decision_options, decision_selected
// Events: event_type (stage_start, stage_complete, stage_failure)
// Errors: error_kind, error_operation, error_detail_path, error_code, error_hint, impact
//
// Prefer these constructors over hand-rolled slog setup to ensure new
// components emit data with the same shape and routing guarantees as the rest
// of the system.
package logging
