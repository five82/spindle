# AGENTS.md

This file provides guidance when working with code in this repository.

CLAUDE.md and QWEN.md are symbolic links to this file so all agent guidance stays in one place.

## TL;DR

- Do not run `git commit` or `git push` unless the user explicitly asks for them.
- Use the Go toolchain (`go build`, `go test`, `golangci-lint`); avoid introducing alternate build systems.
- Finish the work you start. Ask the user before dropping scope or leaving TODOs.
- Keep the daemon-only model intact; commands interact with a running background process.
- Queue statuses matter: handle `PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED`, and be ready for `FAILED` or `REVIEW` detours.
- Before handing work back, run `./check-ci.sh` or explain why you couldn’t.
- Treat the Python reference tree (`src/spindle/**`) as read-only; only edit it if the user explicitly tells you to.

## Critical Expectations

**This is a personal project in rapid development.** Architectural churn is embraced. Optimize for clarity, not backwards compatibility.

- Break things forward. Remove deprecated paths; do not build compatibility shims.
- Prefer maintainable architecture, rich typing, and explicit logging over clever tricks.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters for UX.
- Follow the repo Contributing rhythm: implement, self-test, then summarize what changed and how to validate.

## Project Snapshot

Spindle automates the journey from optical disc to organized Plex library. It coordinates disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata lookup (TMDB), Plex library updates, and notifications (ntfy).

- **Environment**: Go 1.22 toolchain plus MakeMKV/Drapto binaries.
- **Operation mode**: Daemon only. The CLI controls a background process.
- **Inputs**: Mounted discs at `/media/cdrom` or `/media/cdrom0`, or files dropped into watch folders.
- **Outputs**: Structured library tree plus ntfy progress.

See `README.md` for install details, disc mounting notes, and end-user usage.

## Architecture Map

High-level modules you will touch most often:

- **Core orchestration**: `internal/workflow`, `internal/daemon`, and `internal/queue`
- **Stage handlers**: `internal/identification`, `internal/ripping`, `internal/encoding`, `internal/organizer`
- **External services**: `internal/services`, `internal/notifications`, `internal/identification/tmdb`, `internal/disc`
- **CLI and daemon entry point**: `cmd/spindle`
- **Configuration & logging**: `internal/config`, `internal/logging`

When new capabilities land, update this map and the README together so future agents know where to look.

## Workflow Lifecycle

`internal/queue` defines the lifecycle and is the source of truth. Items typically advance:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED
```

- **FAILED** marks irrecoverable runs. Surface the root cause and keep progress context.
- **REVIEW** is for manual intervention (for example, uncertain identification).
- Disc ejection is tied to a successful transition to `RIPPED`.

If you add or reorder phases, update the enums, workflow routing, CLI presentation, docs, and tests in one pull.

## Development Workflow

- Install Go 1.25+ locally and keep `golangci-lint` up to date via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.
- Build the binary from source while iterating: `go install ./cmd/spindle`.
- Configuration lives at `~/.config/spindle/config.toml`. Use dedicated staging/library directories and a test TMDB key for integration flows.
- Before handing off, execute `./check-ci.sh` (runs `go test ./...` and `golangci-lint run`). If you cannot run it, state why.

## Testing & Quality

The Go tests lean heavily on integration-style coverage:

- Key packages: `internal/queue`, `internal/workflow`, `internal/identification`, `internal/ripping`, `internal/encoding`, `internal/organizer`, and `cmd/spindle` integration tests.
- Use interfaces to stub external services (TMDB, Plex, Drapto, MakeMKV) and temporary SQLite databases for queue tests.
- Add tests alongside features and keep assertions at observable boundaries.

Formatting and linting are enforced by `golangci-lint`; run it directly or via `./check-ci.sh`.

## Operations Reference

- Daemon control: `spindle start|stop|status`.
- Logs: `spindle show --follow` for live tails with color, `--lines N` for snapshots.
- Queue resets, health checks, and other maintenance flow through `spindle queue` subcommands (`reset-stuck`, `health`, `clear`, etc.).
- For day-to-day command syntax, rely on `README.md` to avoid duplicating authority here.

## Debugging & Troubleshooting

- **Disc issues**: Verify mounts and MakeMKV availability. `internal/disc` helpers expose scan failures clearly in logs.
- **Identification stalls**: Inspect TMDB configuration, confirm the API key, and review identifier warnings for cache/HTTP issues.
- **Encoding hiccups**: Drapto integration streams JSON progress from `internal/encoding`; capture the log payload before retrying.
- **Queue visibility**: `sqlite3 path/to/queue.db 'SELECT id, disc_title, status, progress_stage FROM queue_items;'` is often faster than adding debug prints.
- **Single instance conflicts**: `internal/daemon` enforces single-instance operation; avoid bypassing it with ad-hoc process launches.

Surface recurring issues in `docs/` so future agents know the resolution path.

## Reference Links

- `README.md`: Installation, configuration, CLI usage, disc mounting instructions.
- `check-ci.sh`: Source of truth for local CI expectations.
- `docs/`: Additional design notes and deep dives (extend when you introduce new subsystems).

Keep AGENTS.md short enough for a fast read. When the workflow evolves, trim obsolete guidance instead of stacking new paragraphs on top.

## GitHub Repositories

spindle - https://github.com/five82/spindle
drapto - https://github.com/five82/drapto
