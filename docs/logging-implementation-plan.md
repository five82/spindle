# Log Output Improvement Plan

Date: 2025-12-29
Owner: Spindle
Scope: Improve log signal-to-noise, make stage progress and decisions easy to follow, and make warnings/errors human readable and easy to find.

## Goals

- Make stage boundaries unmistakable (start/decision/progress/summary).
- Provide decision context for output-impacting choices (including subtitle generation): possible options, chosen option, and why.
- Reduce noisy progress logs (especially MakeMKV and encoding).
- Keep WARN/ERROR lines short and readable while pointing to detailed diagnostics.
- Preserve structured fields for the API stream so CLI/UI can filter or summarize.
- Add predictable logging semantics so future stages stay consistent.

## Non-Goals

- Reworking the overall workflow or stage ordering.
- Building a new log viewer UI (only log output and API filtering improvements).
- Changing external tool behavior (MakeMKV, Drapto, WhisperX) beyond how we capture their output.

## Current Problems Observed (from recent logs)

- MakeMKV progress logs spam every ~5s even when percent is unchanged; progress messages change but are not shown, causing the sampler to think the log is new.
- Progress logs use the key `stage` and overwrite the workflow stage in the event stream.
- Identification stage emits large volumes of INFO logs for TMDB queries and per-result scoring, drowning key decisions.
- Errors from external tools embed full stderr in a single error string, producing huge and unreadable ERROR lines.
- Warnings often lack the fallback behavior and the “what now” guidance.

## Design Principles

- **Signal over noise:** INFO should read like a narrative focused on choices that
  change the final encoded output or delivered subtitle artifacts; details go to DEBUG.
- **Single-line WARN/ERROR:** make them scannable; store full stderr elsewhere.
- **Structured fields first:** logs should be parseable and filtered in API/CLI.
- **Consistent progress keys:** `progress_stage`, `progress_percent`, `progress_message`, `progress_eta`.
- **Decision-first logging:** one summary line for decisions that impact the
  final encoded file or delivered subtitle artifacts; other decision detail belongs in DEBUG.
- **Stable semantics:** predictable log levels and optional per-stage overrides.

## Implementation Plan

### 1) Progress Logging Cleanup

**MakeMKV progress** (internal/ripping/ripper.go)

- Use `progress_stage`, `progress_percent`, `progress_message` instead of `stage`/`percent` to avoid collision with workflow stage.
- Change sampler input: ignore “Progress ...” messages when deciding whether to log; only include non-progress messages.
- Log only when:
  - Progress stage changes
  - Percent bucket crosses (5%)
  - Non-progress message changes

**Optional safety net** (internal/logging/console_handler.go)

- Suppress INFO logs if all fields are filtered and the message hasn’t changed for the same item key.

### 2) Decision Logs: “choices + why” without spam

**Identification**

- Move per-result scoring to DEBUG (internal/identification/confidence.go).
- Add INFO decision summary:
  - Top N candidates (e.g., 3) with score/votes
  - Selected candidate
  - Thresholds used and pass/fail reasoning
- Replace repeated “tmdb query details” INFO with a single INFO plan line, move per-attempt to DEBUG (internal/identification/identifier_tmdb.go).
- Demote `prepared title fingerprint` to DEBUG (internal/identification/identifier_ripspec.go).

**Ripping**

- Add “primary title decision” summary in `ripping.ChoosePrimaryTitle` or the caller:
  - Candidate list (IDs, durations)
  - Rule applied (longest within tolerance; feature-length preferred)
  - Selected ID

**Commentary detection**

- Move per-stream metrics to DEBUG (internal/media/commentary/detector.go).
- Add INFO summary:
  - Candidate indices
  - Included/excluded indices
  - Reason counts
  - Thresholds applied

**Preset selection**

- Add single INFO decision summary and make fallback explicit:
  - Suggested profile, confidence, threshold, applied vs fallback
  - When fallback occurs, log WARN with `alert=preset_decider_fallback` (internal/encoding/preset_selector.go)
- Move raw LLM payload to DEBUG.

**Subtitles**

- Ensure summary says whether OpenSubtitles or WhisperX was used and why (internal/subtitles/stage.go).
- Alert when fallback to WhisperX occurs if OpenSubtitles was expected.

### 3) Errors and Warnings: Human-Readable + Detailed Paths

**Structured error type** (internal/services/errors.go)

- Extend errors with fields:
  - Kind (validation/config/transient/external)
  - Stage
  - Operation
  - Message (short summary)
  - Cause (original error)
  - DetailPath (optional)
- Keep `errors.Is` semantics for existing checks.

**External tool stderr capture**

- For WhisperX/FFmpeg/StableTS/etc. in subtitles (internal/subtitles/service.go):
  - Write stderr to `log_dir/tool/<timestamp>-<tool>.log`.
  - Return error with `DetailPath` and short summary.
  - Do not inline massive stderr in log lines.

**Workflow failure logging** (internal/workflow/manager.go)

- Emit short `error_message` plus structured fields for:
  - `error_kind`, `error_operation`, `error_detail_path`, `error_hint`.
- Keep WARN/ERROR single line; details remain in the tool log file.

**Console formatting** (internal/logging/console_fields.go / value_format.go)

- Truncate `error`/`error_message` to a safe max (e.g., 200 chars) and append “see error_detail_path” when present.

### 4) Make WARN/ERROR Easy to Find

**API filtering** (internal/daemon/api_server.go)

- Add `level` query param; filter events by level.

**CLI filtering** (cmd/spindle/show_command.go)

- Add `--level warn|error|info|debug` flag.

**Optional console marker**

- Prefix WARN/ERROR with `WARN!` / `ERROR!` in console handler if desired for scanning.

### 5) Schema Alignment for Log Details

- Standardize structured fields:
  - `progress_stage`, `progress_percent`, `progress_message`, `progress_eta`
  - `decision_*` fields for decision logs
  - `decision_type` for filtering by decision category
- Update `internal/logging/console_fields.go` highlight list to include decision fields.

### 6) Logging Contract and Taxonomy

- Define log level semantics in `internal/logging/doc.go`:
  - INFO = narrative milestones and decisions
  - WARN = user action likely / degraded behavior
  - ERROR = operation failed and will stop or retry
  - DEBUG = raw diagnostics
- Add `decision_type` and `event_type` fields for key events (e.g., `stage_start`, `stage_complete`, `decision`, `summary`).

### 7) User-Facing Status Snapshots

- Emit periodic per-item “status line” events from workflow manager or progress handlers:
  - `status`, `progress_stage`, `progress_percent`, `progress_eta`, `progress_message`
- Ensures a single line can describe current state at any time.

### 8) Explicit “Why Not” Fields

- For decisions, include compact exclusion reasons:
  - Example: `excluded_candidates=[id:reason,…]` or `decision_rejects` array
- Applies to TMDB matching, primary title selection, commentary detection.

### 9) Error Codes + Hints

- Add stable error codes (`E_*`) and short hints to structured errors.
- Include `error_code` and `error_hint` in WARN/ERROR lines.

### 10) Automatic Redaction

- Redact secrets/tokens from any error output or tool stderr stored in logs:
  - API keys, HF tokens, TMDB key, OpenSubtitles token
- Implement in logging formatter or in error wrapping before logging.

### 11) Per-Stage Verbosity Overrides

- Add config section `logging.stage_overrides`:
  - Example: `logging.stage_overrides.subtitles = "debug"`
- Hook into logger construction or stage logger creation.

### 12) Dependency/Health Snapshot Logging

- On daemon start, log a single health/config snapshot with key dependencies:
  - TMDB key present, MakeMKV binary, Drapto binary, OpenSubtitles enabled, WhisperX settings

### 13) Artifact Paths and Tool Logs

- Standardize `detail_path`, `staging_dir`, `encoded_dir`, `ripped_file`, `encoded_file`, `final_file` fields.
- Keep tool logs under `log_dir/tool/` with retention.

### 14) Global Log Sampling

- Add a global sampler to suppress repeated INFO messages across all components when identical for the same item and context.

### 15) Log API Quality-of-Life

- Add filters: `level`, `alert`, `decision_type`, and `search` (substring match).

### 16) Retention for Tool Logs

- Extend log cleanup to include `log_dir/tool/` with same retention days.

### 17) Tests & Docs

- Update `internal/logging/logger_test.go` for:
  - Progress fields display
  - Truncated error text
  - No empty INFO lines
- Add tests in `internal/ripping/ripper.go` for progress sampler behavior.
- Update `internal/logging/doc.go` to document the new logging contract.

## File Touchpoints

- `internal/ripping/ripper.go` (progress sampling + log keys)
- `internal/identification/confidence.go` (decision logging)
- `internal/identification/identifier_tmdb.go` (TMDB query logging)
- `internal/identification/identifier_ripspec.go` (fingerprint logs)
- `internal/media/commentary/detector.go` (commentary decision logging)
- `internal/encoding/preset_selector.go` (preset decision + warnings)
- `internal/subtitles/service.go` (external tool stderr capture)
- `internal/workflow/manager.go` (failure logging fields)
- `internal/logging/console_handler.go` (optional suppression)
- `internal/logging/console_fields.go` (highlight keys + truncation)
- `internal/services/errors.go` (structured error type)
- `internal/daemon/api_server.go` (level + search filters)
- `cmd/spindle/show_command.go` (level flag)
- `internal/logging/doc.go` (logging contract)
- `cmd/spindle/daemon_run.go` (startup health snapshot)
- `internal/logging/retention.go` (tool log retention)

## Rollout Strategy

1. Land progress key fix + MakeMKV sampler change (largest noise reduction).
2. Land decision summary changes (identification, ripping, commentary, preset).
3. Land structured error + stderr capture (improves WARN/ERROR clarity).
4. Add CLI/API filtering and decision taxonomy fields.
5. Add logging contract, status snapshots, redaction, and per-stage overrides.
6. Update tests and docs.

## Risks / Considerations

- Changing structured keys could affect any external log parsers; keep old keys only if needed for compatibility (prefer forward-only per repo guidance).
- Stderr file handling must respect log_dir permissions and retention.
- Ensure WARN/ERROR summaries remain actionable without requiring the detail file.
- Redaction must be careful not to over-strip user paths or content.

## Validation Plan

- Run `./check-ci.sh`.
- Execute a sample full pipeline with a known disc; verify:
  - Stage start/complete lines present
  - Progress logs show buckets only
  - Decision logs appear once with clear reasoning
  - WARN/ERROR lines are short and include detail path
- Use `spindle show --level warn` to verify filtering works.
