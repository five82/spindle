# AGENTS.md

This file provides guidance when working with code in this repository.

CLAUDE.md and GEMINI.md are symlinks to AGENTS.md so all agent guidance stays in one place.
Do not modify this header.

## TL;DR

- Do not run `git commit` or `git push` unless the user explicitly asks.
- Use the Go toolchain (`go build`, `go test`, `golangci-lint`); avoid alternate build systems.
- Finish the work you start. Ask before dropping scope or leaving TODOs.
- Before handing work back, run `./check-ci.sh` or explain why you couldn't.
- Use Context7 MCP for library/API docs without being asked.

## Project Snapshot

Spindle is a **personal project** that automates optical disc to Jellyfin library: disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata (TMDB), subtitles (WhisperX, forced subs via OpenSubtitles), Jellyfin refresh, and ntfy notifications.

- **Scope**: Single-developer project - avoid over-engineering
- **Operation**: Daemon + optional direct DB access. Queue commands work without daemon.

## Critical Expectations

**Architectural churn is embraced.** Optimize for clarity, not backwards compatibility.

- Break things forward. Remove deprecated paths; no compatibility shims.
- Prefer maintainable architecture and explicit logging over clever tricks.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters.

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

The queue DB is transient (in-flight jobs only). No migrations - bump `schemaVersion` in `internal/queue/schema.go` on changes.
