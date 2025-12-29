# AGENTS.md

This file provides guidance when working with code in this repository.

CLAUDE.md and GEMINI.md are symlinks to this file so all agent guidance stays in one place.

## TL;DR

- Do not run `git commit` or `git push` unless the user explicitly asks for them.
- Use the Go toolchain (`go build`, `go test`, `golangci-lint`); avoid introducing alternate build systems.
- Finish the work you start. Ask the user before dropping scope or leaving TODOs.
- Most commands work with or without a running daemon; queue commands access the database directly when the daemon is stopped.
- Use `spindle stop` to completely stop the daemon.
- Queue statuses matter: handle `PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → [EPISODE_IDENTIFYING → EPISODE_IDENTIFIED] → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED`, and be ready for `FAILED` or `REVIEW` detours.
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

## Project Snapshot

Spindle is a **personal project maintained by a single developer** that automates the journey from optical disc to organized Jellyfin library. It coordinates disc detection, ripping (MakeMKV), encoding (Drapto AV1), metadata lookup (TMDB), Jellyfin library updates, and notifications (ntfy).

- **Scope**: Single-developer personal project - avoid over-engineering
- **Environment**: Go 1.25+ toolchain plus MakeMKV/Drapto binaries.
- **Operation mode**: Daemon with optional direct database access. Queue commands work without a running daemon.
- **Inputs**: Optical discs via `optical_drive` (defaults to `/dev/sr0`).
- **Outputs**: Structured library tree plus ntfy progress.

See `README.md` for install details, disc mounting notes, and end-user usage.

## Logging Guidance

- Log output must be clear, human-readable, and aligned to user workflows.
- INFO logs must capture every decision point with the options considered, the chosen outcome, and the reason.
- Prioritize signal over noise; decide whether a message belongs at INFO or DEBUG before logging it.
- Keep INFO logs easy to follow: short, structured, and readable at a glance.
- DEBUG logs are for raw metrics or large per-sample/per-stream dumps; INFO should still summarize decision evidence (counts, thresholds hit, top-N candidates, etc.).
- WARN/ERROR logs explain: cause, impact, and required user action (if any), plus the next troubleshooting step when possible.
- Include consistent context fields when available: stage, item id, episode key, decision type.

## Architecture Map

High-level modules you will touch most often:

- **Core orchestration**: `internal/workflow`, `internal/daemon`, and `internal/queue`
- **Stage handlers**: `internal/identification`, `internal/ripping`, `internal/episodeid`, `internal/encoding`, `internal/subtitles`, `internal/organizer`
- **Content intelligence**: `internal/contentid`, `internal/ripspec`, `internal/media`, and `internal/ripcache`
- **External services**: `internal/services`, `internal/notifications`, `internal/identification/tmdb`, `internal/disc`
- **CLI and daemon entry point**: `cmd/spindle`
- **Configuration & logging**: `internal/config`, `internal/logging`

For title/episode mapping invariants, read `docs/content-identification.md` before changing the identifier stages.

When new capabilities land, update this map and the README together so future agents know where to look.

## Workflow Lifecycle

`internal/queue` defines the lifecycle and is the source of truth. Items typically advance:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → [EPISODE_IDENTIFYING → EPISODE_IDENTIFIED] → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED
```

- In the SQLite DB / HTTP API these appear as lower-case snake-case (see `internal/queue.Status`).
- **FAILED** marks irrecoverable runs. Surface the root cause and keep progress context.
- **REVIEW** is for manual intervention (for example, uncertain identification).
- Rip completion triggers an ntfy notification at `RIPPED`; users eject the disc manually when convenient.
- Episode identification runs after ripping for TV shows when `opensubtitles_enabled = true`: queue items enter `EPISODE_IDENTIFYING`, WhisperX transcribes each ripped episode, OpenSubtitles downloads reference subtitles, and the matcher correlates ripped files to definitive episode numbers before flipping to `EPISODE_IDENTIFIED`. Movies and items without OpenSubtitles skip this stage automatically.
- Subtitles run after encoding when `subtitles_enabled = true`: queue items enter `SUBTITLING`, prefer OpenSubtitles downloads, fall back to WhisperX, and flip to `SUBTITLED` before the organizer starts. `NeedsReview` plus the `review` status are used when subtitle offsets look suspicious.

If you add or reorder phases, update the enums, workflow routing, CLI presentation, docs, and tests in one pull.

## Development Workflow

- Install Go 1.25+ and keep `golangci-lint` up to date via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.
- Build the binary from source while iterating: `go install ./cmd/spindle`.
- Configuration lives at `~/.config/spindle/config.toml`. Use dedicated staging/library directories and a test TMDB key for integration flows.
- Before handing off, execute `./check-ci.sh` (runs `go test ./...` and `golangci-lint run`). If you cannot run it, state why.

## Configuration Cheat Sheet

- `tmdb_api_key` (required) plus `tmdb_language`/`tmdb_confidence_threshold` control identification; use `spindle config init` to scaffold a sample (fills defaults for everything else).
- Paths: `staging_dir`, `library_dir`, `review_dir`, `log_dir`, `opensubtitles_cache_dir`, `whisperx_cache_dir`, and `rip_cache_dir`; keep them on fast storage because stages stream large files.
- Subtitles & WhisperX: toggle with `subtitles_enabled`; OpenSubtitles requires `opensubtitles_enabled`, `opensubtitles_api_key`, `opensubtitles_user_agent`, optional `opensubtitles_user_token`, and `opensubtitles_languages`; WhisperX tuning lives behind `whisperx_cuda_enabled`, `whisperx_vad_method`, and `whisperx_hf_token`.
- Rip cache: enable via `rip_cache_enabled`, size with `rip_cache_max_gib`, and point at a volume that can hold repeated rips; the cache honors a 20% free-space floor automatically.
- Jellyfin: `jellyfin.enabled`, `jellyfin.url`, and `jellyfin.api_key` must be set before organizer-triggered refreshes run; otherwise the organizer skips Jellyfin refreshes.
- Notifications & misc: `ntfy_topic` enables push updates, `keydb_path`/`keydb_download_url` keep MakeMKV happy, and `api_bind` exposes the queue health endpoint.

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

## Operations Reference

- Daemon control: `spindle start|stop|status`. `spindle stop` completely terminates the daemon.
- Logs: `spindle show --follow` for live tails with color, `--lines N` for snapshots (requires running daemon).
- Queue operations: `spindle queue` subcommands (`status`, `list`, `clear`, `reset-stuck`, `health`, etc.) work with or without a running daemon.
- Subtitle tooling: `spindle gensubtitle /path/to/video.mkv [--forceai]` regenerates SRTs for historic encodes using the same OpenSubtitles/WhisperX pipeline as the queue.
- Disc identification: `spindle identify [/dev/... | --verbose]` runs the TMDB matcher without touching the queue so you can debug metadata issues offline.
- Configuration helpers: `spindle config init` scaffolds a config, `spindle config validate` sanity-checks the active file before launches.
- Notifications & Jellyfin: `spindle test-notify` exercises ntfy; provide Jellyfin credentials so organizer-triggered scans succeed.
- Rip cache: `spindle cache stats|prune` inspects or trims cached rips; useful before/after enabling `rip_cache_enabled`.
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

Keep AGENTS.md short enough for a fast read. When the workflow evolves, trim obsolete guidance instead of stacking new paragraphs on top.

## GitHub Repositories

spindle - https://github.com/five82/spindle
drapto - https://github.com/five82/drapto
flyer - https://github.com/five82/flyer
