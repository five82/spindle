# Development Playbook

Notes for day-to-day work on Spindle. Public setup and usage docs live in `README.md`; agent-specific guidance is in `AGENTS.md`.

## Environment & Tooling

- Install Go 1.25 or newer (`go version` should confirm) and keep `GOBIN`/`GOPATH` on your `PATH` so the `spindle` binary is discoverable during iteration.
- Install lint tooling with `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.
- Keep MakeMKV and Drapto binaries on the PATH (`/usr/local/bin` on the lab machine by convention).
- Default staging/library roots (override in `~/.config/spindle/config.toml` when needed):
  - `~/media/spindle/staging`
  - `~/media/spindle/library`
- Shared TMDB API key for development lives in `.env.dev`; rotate annually.
- ntfy topic used for internal notifications: `spindle-dev`.

## Daily Workflow

1. After dependency or API updates, run `go mod tidy` to keep `go.mod`/`go.sum` focused and reproducible.
2. Build the binary locally: `go install ./cmd/spindle`.
3. Drive manual runs through the Go binaries:
   - `spindle start|status|stop`
   - `spindle show --follow` for live logs
4. Reset queue state with first-class commands instead of direct DB edits: `spindle queue reset-stuck`, `spindle queue clear --completed`, etc.
5. Logs live under `<log_dir>/spindle-<timestamp>.log`; follow the current run with `spindle show --follow` or `tail -f <log_dir>/spindle.log` when the symlink is available. Retention is controlled by `logging.retention_days` (default 60) so disk usage stays bounded.

## Testing & Quality

- Canonical pre-commit check is `./check-ci.sh`; it verifies Go version, runs `go test ./...`, runs a CGO-enabled `go build ./...` (requires `gcc`), and executes `golangci-lint run`.
- If `check-ci.sh` fails, fix lint findings first (`golangci-lint run --fix` when safe), then rerun targeted packages with `go test ./internal/queue ./internal/workflow`, etc.
- Focused test runs:
  - `go test ./internal/queue`
  - `go test ./internal/workflow`
  - `go test ./internal/identification -run TestIdentifier`
- Stage-level integration tests live alongside packages; keep new behavior observable via public interfaces so tests remain table-driven.

## Database & Workflow Notes

- `internal/queue` owns schema migrations and recovery logic; the SQLite database is `<log_dir>/queue.db` by default.
- Inspect state directly when needed with SQLite:
  - `sqlite3 ~/.local/share/spindle/logs/queue.db "SELECT id, disc_title, status, progress_stage FROM queue_items;"`
  - `sqlite3 ~/.local/share/spindle/logs/queue.db ".schema queue_items"`
- Status progression remains `PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → [EPISODE_IDENTIFYING → EPISODE_IDENTIFIED] → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED` with `FAILED`/`REVIEW` detours. When introducing new stages, update enums, orchestrator routing, CLI output, docs, and tests together.

## Release Checklist

1. Ensure `go.mod` and `go.sum` capture the intended dependency graph (`go mod tidy`).
2. Run `./check-ci.sh` and confirm a clean pass.
3. Sanity-test daemon start/stop on the target machine and confirm ntfy notifications.
4. Update `CHANGELOG.md` (or release notes draft) with user-visible changes.
5. Tag the release (`git tag -a vX.Y.Z -m "Spindle vX.Y.Z"`; `git push --tags`).
6. Build distributable binaries as needed with `go build ./cmd/spindle`.

## Parking Lot / Ideas

- Explore WhisperX diarization or parallel chunk tuning once the current CUDA pipeline proves stable.
