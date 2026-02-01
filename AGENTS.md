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

Spindle is a **personal project** that automates optical disc to Jellyfin library: disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata (TMDB), subtitles (OpenSubtitles/WhisperX), Jellyfin refresh, and ntfy notifications.

- **Scope**: Single-developer project - avoid over-engineering
- **Environment**: Go 1.25+, MakeMKV, FFmpeg
- **Operation**: Daemon + optional direct DB access. Queue commands work without daemon.

Queue lifecycle: `PENDING -> IDENTIFYING -> IDENTIFIED -> RIPPING -> RIPPED -> [EPISODE_IDENTIFYING -> EPISODE_IDENTIFIED] -> ENCODING -> ENCODED -> [AUDIO_ANALYZING -> AUDIO_ANALYZED] -> [SUBTITLING -> SUBTITLED] -> ORGANIZING -> COMPLETED` (with `FAILED`/`REVIEW` detours).

## Critical Expectations

**Architectural churn is embraced.** Optimize for clarity, not backwards compatibility.

- Break things forward. Remove deprecated paths; no compatibility shims.
- Prefer maintainable architecture and explicit logging over clever tricks.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters.

## Related Repos

| Repo | Path | Role |
|------|------|------|
| spindle | `~/projects/spindle/` | Daemon + CLI + orchestration (this repo) |
| flyer | `~/projects/flyer/` | Read-only TUI that polls Spindle's API/logs |
| drapto | `~/projects/drapto/` | Go library for AV1 encoding (imported as `github.com/five82/drapto`) |

**Integration contracts:**
- Spindle imports Drapto as a Go library and implements its `Reporter` interface for progress events.
- Flyer is read-only (no queue mutations) and must tolerate Spindle being down.

**Drapto dependency workflow:**
- Local dev uses `go.work` (gitignored) to reference `../drapto` directly.
- CI uses the version in `go.mod`. After pushing drapto changes, update spindle:
  ```bash
  go get github.com/five82/drapto@main && go mod tidy
  ```
- Formal version tags (v1.0.0, etc.) are deferred until the API stabilizes.

## Build, Test, Lint

```bash
go install ./cmd/spindle              # Build
go test ./...                         # Test
go test -race ./...                   # Race detector
golangci-lint run                     # Lint
./check-ci.sh                         # Full CI (recommended before handoff)
```

## Architecture Map

| Area | Packages |
|------|----------|
| Core orchestration | `internal/workflow`, `internal/daemon`, `internal/queue` |
| Stage handlers | `internal/identification`, `internal/ripping`, `internal/episodeid`, `internal/encoding`, `internal/subtitles`, `internal/organizer` |
| Content intelligence | `internal/contentid`, `internal/ripspec`, `internal/media`, `internal/ripcache` |
| External services | `internal/services/` (`drapto/`, `jellyfin/`, `makemkv/`, `presetllm/`) |
| CLI entry | `cmd/spindle` |
| Config & logging | `internal/config`, `internal/logging` |
| Communication | `internal/api` (DTOs), `internal/ipc` (JSON-RPC) |

## Quick Navigation

| Task | Start here |
|------|------------|
| Queue lifecycle | `internal/queue/models.go`, `internal/queue/store.go` |
| Stage logic | `internal/{stage}/handler.go` or `internal/{stage}/{stage}.go` |
| CLI commands | `cmd/spindle/{command}.go` |
| Config fields | `internal/config/config.go` |
| Error types | `internal/services/errors.go` |
| API/IPC | `internal/api/`, `internal/ipc/methods.go` |

## Common Patterns

- **Error propagation**: Stages return errors -> workflow manager -> `StatusFailed`. Use `services.ServiceError` for classification.
- **Progress tracking**: `item.SetProgress(stage, message)` for updates; `item.SetProgressComplete(stage)` when done.
- **State transitions**: Only workflow manager calls `store.UpdateStatus()`. Stages return nil to signal completion.
- **Testing**: `testsupport.NewTestDB()` for temp SQLite; stub external service interfaces.

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

## Log Streaming Architecture

Spindle exposes logs via `/api/logs` with two distinct audiences:

| Audience | Filter | Lane | ItemID | Content |
|----------|--------|------|--------|---------|
| Daemon logs (Flyer) | `daemon_only=1` | any | 0 only | Startup, workflow status, API events |
| Item logs (Flyer) | `item=N` | all | N | Encoding progress, subtitles, organizing |
| CLI/debug | `lane=*` | all | all | Everything |

**Key fields:**
- `ItemID`: Associates log with queue item (0 = daemon-level)
- `Lane`: `foreground` (disc ops) or `background` (encoding/subtitles/organizing)

**Defaults:** Without filters, API returns foreground-only logs (legacy behavior). Flyer uses `daemon_only=1` for daemon view and `item=N` for item view.

**When adding new logs:** Set `ItemID` via context for item-specific work. Daemon-level logs (startup, API, workflow manager) should have `ItemID=0`.

## Database Schema

The queue DB is **transient** (in-flight jobs only). No migrations - just bump `schemaVersion` in `schema.go` and update `schema.sql`. Users run `spindle queue clear` on mismatch.

## Troubleshooting Quick Reference

- **Queue inspection**: `sqlite3 queue.db 'SELECT id, disc_title, status, progress_stage FROM queue_items;'`
- **Subtitle debugging**: Set `SPD_DEBUG_SUBTITLES_KEEP=1` to retain intermediate files
- **Daemon issues**: Single-instance enforced in `internal/daemon`; use `spindle stop` to fully terminate

## Deep Dive Documentation

For detailed guidance beyond this file:

| Topic | Location |
|-------|----------|
| Configuration options | `docs/configuration.md` |
| Workflow stages | `docs/workflow.md` |
| Development setup | `docs/development.md` |
| Content identification | `docs/content-identification.md` |
| CLI reference | `docs/cli.md`, `README.md` |
| API endpoints | `docs/api.md` |
| Package internals | Each package has `doc.go` |

## GitHub

- spindle: https://github.com/five82/spindle
- drapto: https://github.com/five82/drapto
- flyer: https://github.com/five82/flyer
