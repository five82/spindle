# AGENTS.md

This file provides guidance when working with code in this repository. It teaches agents how to collaborate effectively without duplicating the full README.

## TL;DR

- Use `uv` for everything (install, run, test). Never reach for `pip` or ad-hoc virtualenvs.
- Finish the work you start. Ask the user before dropping scope or leaving TODOs.
- Keep the daemon-only model intact; commands interact with a running background process.
- Queue statuses matter: handle `PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED`, and be ready for `FAILED` or `REVIEW` detours.
- Before handing work back, run `./check-ci.sh` or explain why you couldn’t.

## Critical Expectations

**This is a personal project in rapid development.** Architectural churn is embraced. Optimize for clarity, not backwards compatibility.

- Break things forward. Remove deprecated paths; do not build compatibility shims.
- Prefer maintainable architecture, rich typing, and explicit logging over clever tricks.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters for UX.
- Follow the repo Contributing rhythm: implement, self-test, then summarize what changed and how to validate.

## Project Snapshot

Spindle automates the journey from optical disc to organized Plex library. It coordinates disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata lookup (TMDB), Plex library updates, and notifications (ntfy).

- **Environment**: Python managed exclusively via `uv`.
- **Operation mode**: Daemon only. The CLI controls a background process.
- **Inputs**: Mounted discs at `/media/cdrom` or `/media/cdrom0`, or files dropped into watch folders.
- **Outputs**: Structured library tree plus ntfy progress.

See `README.md` for install details, disc mounting notes, and end-user usage.

## Architecture Map

High-level modules you will touch most often:

- **Core orchestration**: `core/daemon.py`, `core/orchestrator.py`, `core/workflow.py`
- **Process guardrails**: `process_manager.py` (single-instance enforcement) and `system_check.py` (dependency validation)
- **Components**: `components/disc_handler.py`, `components/encoder.py`, `components/organizer.py`
- **Services**: `services/makemkv.py`, `services/drapto.py`, `services/tmdb.py`, `services/tmdb_cache.py`, `services/plex.py`, `services/ntfy.py`
- **Storage**: `storage/queue.py` (SQLite queue, schema auto-heals) and `storage/cache.py`
- **Legacy layer kept for low-level operations**: `disc/`
- **CLI and config**: `cli.py`, `config.py`
- **Error surface**: `error_handling.py`

When new capabilities land, update this map and the README together so future agents know where to look.

## Workflow Lifecycle

`storage/queue.py` defines the lifecycle and is the source of truth. Items typically advance:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED
```

- **FAILED** marks irrecoverable runs. Surface the root cause and keep progress context.
- **REVIEW** is for manual intervention (for example, uncertain identification).
- Disc ejection is tied to a successful transition to `RIPPED`.

If you add or reorder phases, update the enum, orchestrator routing, CLI presentation, docs, and tests in one pull.

## Development Workflow

- Install dev dependencies once with `uv pip install -e ".[dev]"` (documented in `README.md`).
- Use `uv run` for every Python entry point (`uv run spindle start`, `uv run pytest`, etc.).
- Configuration lives at `~/.config/spindle/config.toml`. Use test staging/library dirs and a test TMDB key when running integration flows.
- Before committing, execute `./check-ci.sh` (runs pytest with coverage, `black --check`, `ruff`, package build). If you cannot run it, state why in your handoff.

## Testing & Quality

The test suite is intentionally integration-heavy and mirrors user behavior:

- Key files: `tests/test_queue.py`, `tests/test_disc_processing.py`, `tests/test_identification.py`, `tests/test_encoding.py`, `tests/test_cli.py`, plus supporting suites for configuration, error handling, organization, and rip specs.
- Use mocks for external services (TMDB, Plex, Drapto, MakeMKV) and real SQLite databases in temporary directories.
- Add tests alongside features. Keep coverage focused on observable behavior rather than private helpers.

Formatting (`black`) and linting (`ruff`) are enforced by `./check-ci.sh`; let the script highlight anything you missed.

## Operations Reference

- Daemon control: `uv run spindle start|stop|status`.
- Logs: `uv run spindle show --follow` for live tails with color, `--lines N` for snapshots.
- Queue resets, health checks, and other maintenance live in `storage/queue.py` and `system_check.py`. Consult function docstrings before invoking from the CLI.
- For day-to-day command syntax, rely on `README.md` to avoid duplicating authority here.

## Debugging & Troubleshooting

- **Disc issues**: Verify mounts and MakeMKV availability. `disc/` helpers and `components/disc_handler.py` give visibility.
- **Identification stalls**: Inspect TMDB configuration and cached state (`services/tmdb_cache.py`).
- **Encoding hiccups**: Drapto integration streams JSON progress (`services/drapto.py`). Capture progress logs before retrying.
- **Queue visibility**: `sqlite3 path/to/queue.db 'SELECT id, disc_title, status, progress_stage FROM queue_items;'` is often faster than adding debug prints.
- **Single instance conflicts**: `process_manager.py` prevents duplicate daemons; ensure you do not spawn workarounds that bypass it.

Surface recurring issues in `docs/` so future agents know the resolution path.

## Reference Links

- `README.md`: Installation, configuration, CLI usage, disc mounting instructions.
- `check-ci.sh`: Source of truth for local CI expectations.
- `docs/`: Additional design notes and deep dives (extend when you introduce new subsystems).

Keep AGENTS.md short enough for a fast read. When the workflow evolves, trim obsolete guidance instead of stacking new paragraphs on top.
