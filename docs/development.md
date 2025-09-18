# Development Playbook

Notes for day-to-day work on Spindle. Public setup and usage docs live in `README.md`; agent-specific guidance is in `AGENTS.md`.

## Environment & Tooling

- Install/update tooling with `uv`; never touch `pip` directly.
- Keep MakeMKV and Drapto binaries on the PATH (`/usr/local/bin` for the lab machine).
- Primary staging/library roots (override in `~/.config/spindle/config.toml` when needed):
  - `~/media/spindle/staging`
  - `~/media/spindle/library`
- Test TMDB API key: stored in `.env.dev` next to the config; rotate yearly.
- ntfy topic used for notifications: `spindle-dev`.

## Daily Workflow

1. `uv pip install -e ".[dev]"` after dependency bumps (mostly redundant but keeps local env fresh).
2. Drive work through `uv run` entry points:
   - `uv run spindle start|status|stop`
   - `uv run spindle show --follow` during manual runs
   - `uv run pytest` for ad-hoc verification
3. Queue database reset when experimenting: `uv run python -m spindle.storage.queue reset` (or delete the file in `~/.local/share/spindle/logs`).
4. Keep logs handy: `tail -f ~/.local/share/spindle/logs/spindle.log` while testing daemon changes.

## Testing & Quality

- Canonical pre-commit check is `./check-ci.sh`; it runs pytest (with coverage), `black --check`, `ruff`, `uv build`, and `twine check`.
- When `check-ci.sh` fails, fix formatting first (`uv run black src/`), then lint (`uv run ruff check src/ --fix`), rerun targeted pytest files as needed.
- Integration-heavy suites worth running individually:
  - `uv run pytest tests/test_queue.py`
  - `uv run pytest tests/test_disc_processing.py`
  - `uv run pytest tests/test_cli.py`
- Capture expectations for new workflow stages or services inside the relevant test modules.

## Database & Workflow Notes

- `storage/queue.py` now recreates the schema if it detects a mismatch—no separate migration scripts.
- Use SQLite CLI for quick inspections:
  - `sqlite3 ~/.local/share/spindle/logs/queue.db "SELECT id, disc_title, status, progress_stage FROM queue_items;"`
  - `sqlite3 ~/.local/share/spindle/logs/queue.db ".schema queue_items"`
- Remember the full status set includes `FAILED` and `REVIEW`; orchestrator and CLI need updates when adding new transitions.

## Release Checklist

1. Ensure `uv.lock` is up to date (`uv lock --upgrade` if dependencies changed).
2. Run `./check-ci.sh` and confirm all green.
3. Sanity-test daemon start/stop on the target machine and confirm ntfy notifications fire.
4. Update `CHANGELOG.md` (or release notes draft) with user-visible changes.
5. Tag release and publish (
   `git tag -a vX.Y.Z -m "Spindle vX.Y.Z"` → `git push --tags`).
6. Build artifacts with `uv build`; upload via `twine` if distributing.

## Parking Lot / Ideas

- Future experiment: integrate subtitle workflow once the AI-generated flow is ready.
