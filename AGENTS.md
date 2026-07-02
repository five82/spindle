# AGENTS.md

This file provides guidance when working with code in this repository.

## TL;DR

- Do not run `git commit` or `git push` unless the user explicitly asks.
- Use the Go toolchain (`go build`, `go test`, `golangci-lint`); avoid alternate build systems.
- Finish the work you start. Ask before dropping scope or leaving TODOs.
- Before handing work back, run `./check-ci.sh` or explain why you couldn't.
- Prefer deletion, consolidation, and stronger invariants over additive fixes.

## Project Snapshot

Spindle is a **personal project** that automates optical disc to Jellyfin library: disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata (TMDB), subtitles (WhisperX, forced subs via OpenSubtitles), Jellyfin refresh, and ntfy notifications.

- **Scope**: Single-developer project — avoid over-engineering.
- **Operation**: Daemon-owned queue access through the HTTP API. Queue commands require the daemon except stopped-daemon `queue clear --all`, which deletes the transient queue DB files.

## Related Repos

| Repo | Path | Role |
|------|------|------|
| drapto | `~/projects/drapto/` | FFmpeg encoding wrapper |
| spindle | `~/projects/spindle/` | Orchestrator that uses Drapto as a library (this repo) |
| flyer | `~/projects/flyer/` | Read-only TUI for Spindle |

GitHub: [drapto](https://github.com/five82/drapto) | [spindle](https://github.com/five82/spindle) | [flyer](https://github.com/five82/flyer)

## Critical Expectations

Architectural churn is embraced. Optimize for clarity, not backwards compatibility.

- Apply YAGNI ("You Aren't Gonna Need It") and KISS ("Keep It Simple, Stupid"). Build only what the current task requires -- do not add abstractions, generality, or "future-proofing" for needs that do not yet exist. When two approaches work, take the simpler one. (Configuration/knobs are covered by the next bullet.)
- Find the smallest maintainable change. Prefer stronger invariants, deletion, and consolidation over new code.
- Break things forward. Remove deprecated paths; no compatibility shims.
- Simplify by reducing concepts, branches, states, and files. Do not simplify by adding abstraction layers.
- Prefer maintainable architecture and explicit logging over clever tricks.
- Identify and close real gaps. Avoid over-engineering and unlikely edge cases.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters.
- When troubleshooting, gather evidence and test. Do not blindly guess.
- Observability is key. If a decision changes what happens next, it must be visible without enabling DEBUG.
- Simplification must not remove user-visible functionality. Eliminating a code path that produces distinct output (log messages, CLI feedback, status indicators) is a behavior change, not a simplification.
- When examining reference code, understand why it works before adapting it. Do not copy-paste.

## Complexity Budget

Spindle is feature-complete. Production code growth is suspect by default.

Before implementing any fix or refactor:
1. Reproduce or identify the failing behavior.
2. Identify the invariant that should make the bug impossible.
3. Look for existing code that becomes redundant if that invariant is enforced.
4. Apply the smallest maintainable change.

Rules:
- Tests may grow freely. Production LOC should be flat or negative.
- Do not add new packages, interfaces, exported symbols, config flags, background workers, queues, caches, registries, or abstraction layers unless they clearly reduce total complexity.
- Avoid helper sprawl: do not extract single-use helpers unless they represent a real domain concept.
- If adding production code, explain what existing complexity it replaces or why deletion wasn't enough.

Before handing back non-trivial work, summarize:
- Production lines added/deleted
- Tests added/changed
- New exported symbols, packages, config flags, or goroutines
- Code paths removed or simplified
- Why the change fixes the class of issue rather than masking one symptom

## Refactor Policy

Refactors must reduce conceptual surface area.

Good refactors:
- Remove a code path
- Collapse duplicate logic
- Make invalid states unrepresentable
- Reduce exported API surface
- Move behavior closer to the owning domain concept
- Improve logging of real decisions without changing behavior

Suspicious refactors:
- Introduce new interfaces without multiple real implementations
- Add managers, processors, registries, factories, or builders
- Split small cohesive files into many tiny files
- Add configuration to avoid making a design decision
- Preserve old behavior through compatibility layers

## Subtitle Policy

- Do not use PGS subtitles as final library output. Spindle's Jellyfin-facing subtitle output is SRT.
- Source subtitle tracks may be detected for metadata/forced-subtitle signals, but primary display subtitles are generated/handled as SRT.

## Drapto Dependency Workflow

- Local dev uses `go.work` (gitignored) to reference `../drapto` directly.
- CI uses the version in `go.mod`. After pushing drapto changes, update spindle:
  ```bash
  go get github.com/five82/drapto@main && go mod tidy
  ```

## Logging Guidance

| Level | Use For |
|-------|---------|
| INFO  | All decisions that affect output: stage start/complete, track selection, preset choice, skip decisions, fallback logic, cache hits |
| DEBUG | Raw data dumps, metrics, internal state (not decisions) |
| WARN  | Degraded behavior (include `event_type`, `error_hint`, `impact`) |
| ERROR | Operation failed (include `event_type`, `error_hint`, `error`) |

Decision logging pattern: All decisions use `decision_type`, `decision_result`, `decision_reason` attributes.

Progress format: `"Phase N/M - Action (context)"` (e.g., `"Phase 2/3 - Ripping selected titles (5 of 12)"`)

## Database Schema

The queue DB is transient (in-flight jobs only). No migrations, no schema versioning. If the schema changes, clear the database.
