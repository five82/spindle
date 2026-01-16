# AGENTS.md

This file provides guidance when working with code in this repository.

CLAUDE.md and GEMINI.md are symlinks to AGENTS.md so all agent guidance stays in one place.
Do not modify this header.

## TL;DR

- Do not run `git commit` or `git push` unless the user explicitly asks for them.
- Use the Go toolchain (`go build`, `go test`, `golangci-lint`); avoid introducing alternate build systems.
- Finish the work you start. Ask the user before dropping scope or leaving TODOs.
- Most commands work with or without a running daemon; queue commands access the database directly when the daemon is stopped.
- Use `spindle stop` to completely stop the daemon.
- Queue statuses matter: handle `PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → [EPISODE_IDENTIFYING → EPISODE_IDENTIFIED] → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED`, and be ready for `FAILED` detours.
- Before handing work back, run `./check-ci.sh` or explain why you couldn’t.

## Related Repos (Local Dev Layout)

Spindle is one of three sibling repos that are developed together on this machine:

- **spindle** (this repo): `~/projects/spindle/` — daemon + CLI + workflow orchestration
- **flyer**: `~/projects/flyer/` — read-only TUI that polls Spindle’s API/logs and renders queue state
- **drapto**: `~/projects/drapto/` — ffmpeg encoding wrapper invoked by Spindle during `ENCODING`

Integration contracts to keep in mind while changing code:

- Spindle shells out to Drapto (external binary) and consumes Drapto’s `--progress-json` event stream; keep those JSON objects compatible with Spindle’s consumer.
- Flyer should remain read-only (no queue mutations) and must tolerate Spindle being down or misconfigured (clear error states, no panics).

## Critical Expectations

**This is a personal project in rapid development.** Architectural churn is embraced. Optimize for clarity, not backwards compatibility.

- Break things forward. Remove deprecated paths; do not build compatibility shims.
- Prefer maintainable architecture, rich typing, and explicit logging over clever tricks.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters for UX.
- Follow the repo Contributing rhythm: implement, self-test, then summarize what changed and how to validate.

## MCP

Always use Context7 MCP when I need library/API documentation, code generation, setup or configuration steps without me having to explicitly ask.

## Project Snapshot

Spindle is a **personal project maintained by a single developer** that automates the journey from optical disc to organized Jellyfin library. It coordinates disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata lookup (TMDB), Jellyfin library updates, and notifications (ntfy).

- **Scope**: Single-developer personal project - avoid over-engineering
- **Environment**: Go 1.25+ toolchain plus MakeMKV/Drapto binaries.
- **Operation mode**: Daemon with optional direct database access. Queue commands work without a running daemon.
- **Inputs**: Optical discs via `optical_drive` (defaults to `/dev/sr0`).
- **Outputs**: Structured library tree plus ntfy progress.

See `README.md` for install details, disc mounting notes, and end-user usage.

## Logging Guidance

### Level Semantics

| Level | Use For | Required Fields |
|-------|---------|-----------------|
| INFO | Stage start, stage summary, decisions that **change output** | `event_type` |
| DEBUG | Skip decisions, search results, internal state, raw metrics | (none) |
| WARN | Degraded behavior, work continues but user should know | WARN triad |
| ERROR | Operation failed, will stop or retry | `event_type`, `error_hint`, `error` |

### Stage Logging Pattern

Every stage emits exactly:
1. **DEBUG** on prepare: `"starting {stage} preparation"`
2. **INFO** for decisions that affect the final output file
3. **DEBUG** for skip decisions explaining why something didn't happen
4. **INFO** on completion: `"{stage} stage summary"` with:
   - `event_type: "stage_complete"`
   - `stage_duration`
   - Key metrics (file count, bytes, cache status, etc.)

### Progress Messages

Use consistent format: `"Phase N/M - Action (context)"`

Examples:
- `"Phase 1/3 - Scanning disc with MakeMKV"`
- `"Phase 2/3 - Ripping selected titles (5 of 12)"`
- `"Phase 3/3 - Validating ripped files"`

### Decision Logs

Only log decisions at INFO when they **change the final file**:
- Track selection (audio, subtitle tracks)
- Preset selection
- Subtitle source (OpenSubtitles vs WhisperX)
- Encoding parameters

Internal skip logic (movie content, no episodes, cache miss) belongs at DEBUG.

### WARN Triad (Enforced)

All WARN logs must include:
- `event_type`: what happened (e.g., `"cache_inspection_failed"`)
- `error_hint`: actionable next step
- `impact`: user-facing consequence

Use `logging.WarnWithContext()` helper to enforce this.

### Condensing Verbose Output

When logging lists (search results, candidates, matches):
- Log summary at INFO: count, best match, key decision factors
- Include `*_hidden_count` field when truncating
- Full details belong at DEBUG only

## Architecture Map

High-level modules you will touch most often:

- **Core orchestration**: `internal/workflow`, `internal/daemon`, `internal/queue`
- **Stage handlers**: `internal/identification`, `internal/ripping`, `internal/episodeid`, `internal/encoding`, `internal/subtitles`, `internal/organizer`
- **Content intelligence**: `internal/contentid`, `internal/ripspec`, `internal/media`, `internal/ripcache`
- **External services**: `internal/services`, `internal/notifications`, `internal/disc`
- **CLI and daemon entry point**: `cmd/spindle`
- **Configuration & logging**: `internal/config`, `internal/logging`
- **Communication layer**: `internal/api` (wire-format DTOs), `internal/ipc` (JSON-RPC daemon communication)
- **Utilities**: `internal/logs` (log tailing), `internal/deps` (dependency checks), `internal/encodingstate` (Drapto telemetry)

Notable sub-packages within larger modules:

- `internal/services/`: `drapto/`, `jellyfin/`, `makemkv/`, `presetllm/`
- `internal/media/`: `audio/` (track selection), `commentary/` (detection), `ffprobe/` (media inspection)
- `internal/identification/`: `tmdb/`, `keydb/`, `overrides/`
- `internal/subtitles/`: `opensubtitles/`
- `internal/disc/`: `fingerprint/`

For title/episode mapping invariants, read `docs/content-identification.md` before changing the identifier stages.

When new capabilities land, update this map and the README together so future agents know where to look.

## Quick Navigation

Where to look first for common tasks:

| Task | Start here |
|------|------------|
| Queue status/lifecycle | `internal/queue/models.go` (Status enum), `internal/queue/store.go` (persistence) |
| Stage logic | `internal/{stagename}/handler.go` or `internal/{stagename}/{stagename}.go` |
| CLI commands | `cmd/spindle/{command}.go` (e.g., `cmd/spindle/queue.go`) |
| Config fields | `internal/config/config.go` (struct definitions + defaults) |
| Error classification | `internal/services/errors.go` (ServiceError, ErrorKind) |
| API/IPC contracts | `internal/api/` (DTOs), `internal/ipc/methods.go` (RPC handlers) |
| Drapto integration | `internal/services/drapto/runner.go`, `internal/encodingstate/` |
| Flyer integration | `internal/api/` (converters transform internal models to wire format) |

## Key Interfaces

Critical abstractions for testing and extension:

```go
// Queue persistence - stub this in tests with in-memory SQLite
queue.Store interface {
    Create, Get, Update, List, UpdateStatus, ...
}

// Stage execution contract - each stage implements this
workflow.StageHandler interface {
    Handle(ctx, item) error
}

// TMDB client - stub for identification tests
tmdb.Searcher interface {
    SearchMovie, SearchTV, GetMovieDetails, GetTVDetails, ...
}

// Notification abstraction
services.Notifier interface {
    Send(ctx, event) error
}

// Error classification - used for logging and diagnostics
services.ServiceError struct {
    Kind ErrorKind  // validation, configuration, not_found, transient, etc.
    // ... stage context, message, hints
}
```

## Common Patterns

**Error propagation**: Stages return errors that bubble up to the workflow manager. All failures result in `StatusFailed`; error classification via `services.ServiceError` is used for logging and diagnostics, not status routing.

**Progress tracking**: Stages call `item.SetProgress(stage, message)` for incremental updates and `item.SetProgressComplete(stage)` when done. These updates are persisted and visible via `queue show <id>`.

**State transitions**: Only the workflow manager should call `store.UpdateStatus()`. Stages signal completion by returning nil; the manager advances the item to the next status.

**Testing pattern**: Create a temporary SQLite database with `testsupport.NewTestDB()`, inject stub implementations of external service interfaces, and assert on final queue item state.

## Workflow Lifecycle

`internal/queue` defines the lifecycle and is the source of truth. Items typically advance:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → [EPISODE_IDENTIFYING → EPISODE_IDENTIFIED] → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED
```

- In the SQLite DB / HTTP API these appear as lower-case snake-case (see `internal/queue.Status`).
- **FAILED** marks irrecoverable runs. Surface the root cause and keep progress context. Items with `NeedsReview = true` indicate manual intervention may be needed (files routed to `review_dir`).
- Rip completion triggers an ntfy notification at `RIPPED`; users eject the disc manually when convenient.
- Episode identification runs after ripping for TV shows when `opensubtitles_enabled = true`: queue items enter `EPISODE_IDENTIFYING`, WhisperX transcribes each ripped episode, OpenSubtitles downloads reference subtitles, and the matcher correlates ripped files to definitive episode numbers before flipping to `EPISODE_IDENTIFIED`. Movies and items without OpenSubtitles skip this stage automatically.
- Subtitles run after encoding when `subtitles_enabled = true`: queue items enter `SUBTITLING`, prefer OpenSubtitles downloads, fall back to WhisperX, and flip to `SUBTITLED` before the organizer starts. `NeedsReview` flag is set when subtitle offsets look suspicious.

If you add or reorder phases, update the enums, workflow routing, CLI presentation, docs, and tests in one pull.

## Build, Test, Lint Commands

```bash
# Build
go install ./cmd/spindle          # Build and install binary

# Test
go test ./...                     # Run all tests
go test -race ./...               # Run all tests with race detector
go test ./internal/queue          # Run tests for a specific package
go test ./internal/queue -run TestStore  # Run a single test by name
go test ./internal/identification -run TestIdentifier/movie  # Run subtest

# Lint
golangci-lint run                 # Run linter
golangci-lint run --fix           # Auto-fix safe issues

# Full CI check (recommended before handing off)
./check-ci.sh                     # Runs: go mod tidy, go test, go test -race,
                                  # CGO build, golangci-lint, govulncheck
```

## Development Workflow

- Install Go 1.25+ and keep `golangci-lint` v2.0+ up to date via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.
- Configuration lives at `~/.config/spindle/config.toml`. Use dedicated staging/library directories and a test TMDB key for integration flows.
- Before handing off, execute `./check-ci.sh`. If you cannot run it, state why.

## Configuration Cheat Sheet

- `tmdb_api_key` (required) plus `tmdb_language`/`tmdb_confidence_threshold` control identification; use `spindle config init` to scaffold a sample (fills defaults for everything else).
- Paths: `staging_dir`, `library_dir`, `review_dir`, `log_dir`, `opensubtitles_cache_dir`, `whisperx_cache_dir`, and `rip_cache_dir`; keep them on fast storage because stages stream large files.
- Subtitles & WhisperX: toggle with `subtitles_enabled`; OpenSubtitles requires `opensubtitles_enabled`, `opensubtitles_api_key`, `opensubtitles_user_agent`, optional `opensubtitles_user_token`, and `opensubtitles_languages`; WhisperX tuning lives behind `whisperx_cuda_enabled`, `whisperx_vad_method`, and `whisperx_hf_token`.
- Rip cache: enable via `rip_cache_enabled`, size with `rip_cache_max_gib`, and point at a volume that can hold repeated rips; the cache honors a 20% free-space floor automatically.
- Jellyfin: `jellyfin.enabled`, `jellyfin.url`, and `jellyfin.api_key` must be set before organizer-triggered refreshes run; otherwise the organizer skips Jellyfin refreshes.
- Notifications: `ntfy_topic` enables push updates; granular per-event toggles available under `notifications.*`.
- MakeMKV: `keydb_path`/`keydb_download_url` keep MakeMKV happy; `optical_drive` defaults to `/dev/sr0`.
- Commentary detection: `commentary_detection.*` exposes ~9 audio analysis parameters for tuning commentary track identification.
- LLM preset selection: `preset_decider.*` configures OpenRouter integration (`api_key`, `base_url`, `model`, `timeout`) for automatic encoding preset classification.
- Workflow tuning: `workflow.*` controls poll intervals, heartbeat settings, and error retry behavior.
- Logging: `logging.level`, `logging.format`, `logging.retention_days`; use `logging.stage_overrides` for per-component log level tuning.
- Network: `api_bind` exposes the queue health endpoint and IPC socket.

See `internal/config/config.go` for the complete Config struct with all fields and defaults.

## Package Documentation

- Every Go package has a `doc.go` with a concise, high-signal overview. Keep it in sync when you touch the package.
- Cover: the package’s role in the workflow, its primary collaborators/entry points, notable invariants or side effects, and where to look for deeper docs.
- Favour short paragraphs over exhaustive prose; the goal is to orient future contributors and coding agents quickly.
- When adding stages or new behaviour, update the relevant `doc.go` in the same change so downstream readers do not need to skim history.
- If you discover an undocumented package and cannot author a useful summary immediately, coordinate with the maintainer rather than committing an empty placeholder.

## Testing & Quality

The Go tests lean heavily on integration-style coverage:

- Key packages: `internal/queue`, `internal/workflow`, `internal/identification`, `internal/ripping`, `internal/encoding`, `internal/organizer`, and `cmd/spindle` integration tests.
- Use interfaces to stub external services (TMDB, Jellyfin, Drapto, MakeMKV) and temporary SQLite databases for queue tests.
- Add tests alongside features and keep assertions at observable boundaries.

Formatting and linting are enforced by `golangci-lint`; run it directly or via `./check-ci.sh`.

## Database Schema Changes

The queue database (`internal/queue`) is **transient**—it tracks in-flight jobs, not permanent data. Do not implement migrations.

When changing the schema:

1. Update `schema.sql` with the new columns/tables
2. Bump `schemaVersion` in `schema.go`
3. Update the comment in `schema.sql` to match the new version

Users with an existing database will see a clear error on daemon start telling them to run `spindle queue clear` or delete the database file. This is intentional—the queue is ephemeral and can always be recreated.

## Operations Reference

- Daemon control: `spindle start|stop|status`. `spindle stop` completely terminates the daemon.
- Logs: `spindle show --follow` for live tails with color, `--lines N` for snapshots (requires running daemon). Filter with `--component`, `--lane`, `--request`, `--item`.
- Queue operations: `spindle queue` subcommands work with or without a running daemon:
  - `queue status` - aggregate counts by status
  - `queue list` - list all items
  - `queue show <id>` - detailed item view with per-episode progress
  - `queue stop <id>` - stop a queued item
  - `queue retry <id>` - reset a failed item for retry
  - `queue clear [--completed]` - remove items from queue
  - `queue clear-failed` - remove all failed items
  - `queue reset-stuck` - recover items stuck via heartbeat timeout
  - `queue health` - database diagnostics
- Subtitle tooling: `spindle gensubtitle /path/to/video.mkv [--forceai]` regenerates SRTs for historic encodes using the same OpenSubtitles/WhisperX pipeline as the queue.
- Disc identification: `spindle identify [/dev/... | --verbose]` runs the TMDB matcher without touching the queue so you can debug metadata issues offline.
- Configuration helpers: `spindle config init` scaffolds a config, `spindle config validate` sanity-checks the active file before launches.
- Notifications & Jellyfin: `spindle test-notify` exercises ntfy; provide Jellyfin credentials so organizer-triggered scans succeed.
- Rip cache: `spindle cache stats|prune` inspects or trims cached rips; `cache rip <path>` processes a directory without a disc; `cache populate <dir>` fills cache from existing rips; `cache commentary` runs commentary detection on cached items.
- LLM preset debugging: `spindle preset-decider-test` tests the OpenRouter-based encoding preset selection.
- Global flags: `--log-level` adjusts verbosity for any subcommand.
- For day-to-day command syntax, rely on `README.md` to avoid duplicating authority here.

## Debugging & Troubleshooting

- **Disc issues**: Verify mounts and MakeMKV availability. `internal/disc` helpers expose scan failures clearly in logs.
- **Identification stalls**: Inspect TMDB configuration, confirm the API key, and review identifier warnings for cache/HTTP issues.
- **Encoding hiccups**: Drapto integration streams JSON progress from `internal/encoding`; capture the log payload before retrying.
- **Subtitle stalls**: Confirm `subtitles_enabled`/`opensubtitles_*` configuration plus WhisperX toggles (`whisperx_cuda_enabled`, `whisperx_hf_token` when using `pyannote` VAD). Logs from `internal/subtitles` include `subtitle_source`, rejection reasons, and offsets—set `SPD_DEBUG_SUBTITLES_KEEP=1` to retain intermediate files under the item's staging folder for inspection. Use `spindle gensubtitle` for targeted retries once config issues are resolved.
- **Rip cache surprises**: When `rip_cache_enabled` is true, ripped titles persist under `rip_cache_dir` and shortcut future encodes. If disk pressure grows, run `spindle cache stats` to review usage or `spindle cache prune` to reclaim space; the manager also auto-prunes when free space dips below ~20%, so verify `rip_cache_max_gib` aligns with local capacity.
- **Queue visibility**: `sqlite3 path/to/queue.db 'SELECT id, disc_title, status, progress_stage FROM queue_items;'` is often faster than adding debug prints.
- **Single instance conflicts**: `internal/daemon` enforces single-instance operation; avoid bypassing it with ad-hoc process launches.
- **Daemon persistence**: `spindle stop` completely terminates the daemon. Queue commands continue to work by accessing the database directly.

Surface recurring issues in `docs/` so future agents know the resolution path.

## Reference Links

- `README.md`: Installation, configuration, CLI usage, disc mounting instructions.
- `check-ci.sh`: Source of truth for local CI expectations.
- `docs/`: Additional design notes and deep dives (extend when you introduce new subsystems).

When the workflow evolves, update this file and trim obsolete guidance rather than stacking new paragraphs on top.

## GitHub Repositories

spindle - https://github.com/five82/spindle
drapto - https://github.com/five82/drapto
flyer - https://github.com/five82/flyer
