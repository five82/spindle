# AGENTS.md

This file provides guidance when working with code in this repository.

CLAUDE.md and GEMINI.md are symlinks to AGENTS.md so all agent guidance stays in one place.
Do not modify this header.

## TL;DR

- Do not run `git commit` or `git push` unless the user explicitly asks.
- Use the Go toolchain (`go build`, `go test`, `golangci-lint`); avoid alternate build systems.
- Finish the work you start. Ask before dropping scope or leaving TODOs.
- Before handing work back, run `./check-ci.sh` or explain why you couldn't.

## Project Snapshot

Spindle is a **personal project** that automates optical disc to Jellyfin library: disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata (TMDB), subtitles (Qwen3-ASR, forced subs via OpenSubtitles), Jellyfin refresh, and ntfy notifications.

- **Scope**: Single-developer project - avoid over-engineering
- **Operation**: Daemon + optional direct DB access. Queue commands work without daemon.

## Related Repos

| Repo | Path | Role |
|------|------|------|
| drapto | `~/projects/drapto/` | FFmpeg encoding wrapper |
| spindle | `~/projects/spindle/` | Orchestrator that uses Drapto as a library (this repo) |
| flyer | `~/projects/flyer/` | Read-only TUI for Spindle |

GitHub: [drapto](https://github.com/five82/drapto) | [spindle](https://github.com/five82/spindle) | [flyer](https://github.com/five82/flyer)

## Critical Expectations

**Favor clear forward progress over backwards compatibility.**

- Follow the spec docs. Propose spec changes when needed, but do not edit spec documents without approval.
- Prefer the most maintainable solution, not the quickest patch.
- Break things forward. Remove deprecated paths; no compatibility shims.
- Prefer maintainable architecture and explicit logging over clever tricks.
- Prefer minimalism. Identify and close real gaps. Simplify. Avoid overengineering. Avoid chasing edge cases that we are unlikely to encounter.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Prefer ASCII edits unless the file already uses non-ASCII characters.
- When troubleshooting, gather evidence first and verify with tests.
- Prioritize observability. If we cannot see a decision or failure, we cannot debug it.
- Behavior-preserving simplification only. Any user-visible behavior change requires explicit approval.
- Do not cargo-cult reference code. Understand why it works, then design the right solution for this codebase.

## Drapto Dependency Workflow

- Local dev uses `go.work` (gitignored) to reference `../drapto` directly.
- CI uses the version in `go.mod`. After pushing drapto changes, update spindle:
  ```bash
  go get github.com/five82/drapto@main && go mod tidy
  ```

## Logging Guidance

| Level | Use For |
|-------|---------|
| INFO | All decisions that affect output: stage start/complete, track selection, preset choice, skip decisions, fallback logic, cache hits |
| DEBUG | Raw data dumps, metrics, internal state (not decisions) |
| WARN | Degraded behavior (include `event_type`, `error_hint`, `impact`) |
| ERROR | Operation failed (include `event_type`, `error_hint`, `error`) |

**Decision logging pattern:** All decisions use `decision_type`, `decision_result`, `decision_reason` attributes.

**Rationale:** If a decision changes what happens next, it must be visible without enabling DEBUG. Invisible decisions make debugging impossible.

**Progress format**: `"Phase N/M - Action (context)"` (e.g., `"Phase 2/3 - Ripping selected titles (5 of 12)"`)

## Database Schema

The queue DB is transient (in-flight jobs only). No migrations, no schema versioning. If the schema changes, clear the database.
