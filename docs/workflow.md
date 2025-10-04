# Spindle Workflow Guide

This guide walks through what happens after you start the Spindle daemon and insert a disc. It is written for daily users who want to understand each stage, where files land, and how to keep an eye on progress.

## Before You Start

- Install Spindle with `go install github.com/five82/spindle/cmd/spindle@latest` and create your config with `spindle init-config` (see the README for full setup).
- Edit `~/.config/spindle/config.toml` so `library_dir`, `staging_dir`, `tmdb_api_key`, `plex_url`, `plex_link_enabled`, and `ntfy_topic` (optional) are filled out. Run `spindle plex link` once to authorize Plex when automatic refreshes are enabled.
- Run `spindle config validate` to confirm the configuration and directories are ready.
- Start the background process with `spindle start`, then monitor logs with `spindle show --follow` (add `--lines N` for a snapshot without following).

Spindle only runs as a daemon. All commands talk to that background process.

## Lifecycle at a Glance

Every item moves through the queue in order. The statuses you will see are:

- `PENDING` - disc noticed, waiting for identification
- `IDENTIFYING` -> `IDENTIFIED` - MakeMKV scan + TMDB lookup completed
- `RIPPING` -> `RIPPED` - video copied to the staging area; you’ll get a notification so the disc can be ejected manually
- `ENCODING` -> `ENCODED` - Drapto transcodes the rip in the background
- `ORGANIZING` -> `COMPLETED` - file moved into your library, Plex refresh triggered
- `REVIEW` - manual attention required (no confident match found)
- `FAILED` - an error stopped progress; inspect logs and retry when ready

Use `spindle queue list` to inspect items and `spindle queue health` for lifecycle totals.

## Stage 1: Disc Detection & Queueing (PENDING)

1. The daemon watches your optical drive (`/dev/sr0` by default). When a disc arrives, it logs the discovery and sends an ntfy notification (if configured).
2. The disc fingerprint is checked against previous runs; known discs resume where they left off.
3. A new queue entry is created with status `PENDING`, ready for analysis.

You can keep inserting discs back-to-back; each one lands in the queue.

## Stage 2: Content Identification (IDENTIFYING -> IDENTIFIED)

1. Spindle triggers a MakeMKV scan to read every title on the disc.
2. The intelligent analyzer classifies the disc (movie vs TV set, extras, commentary tracks) and calls TMDB to find the best metadata match.
3. Success: the queue item is marked `IDENTIFIED`, media details (title, year, season/episode) are stored, and the rip specification is written to the queue database. When a release year is available, an ntfy notification announces the match so you know the daemon has the correct metadata before ripping starts.
4. No confident match: the item stays at `IDENTIFIED` but is flagged with `NeedsReview = true`, and you receive guidance in the logs. The pipeline keeps moving so downstream stages can finish while you decide how to handle the unknown metadata.

Progress messages in `spindle show --follow` tell you what the analyzer is doing ("Analyzing disc content", "Classifying disc contents", etc.).

## Stage 3: Ripping the Disc (RIPPING -> RIPPED)

1. Identified items flow into the MakeMKV ripper. Spindle updates the queue to `RIPPING` and streams progress ("Ripping disc", percentage updates) as Makemkvcon runs.
2. Video files are written to `<staging_dir>/rips/`.
3. When the rip succeeds, the item is marked `RIPPED` and an ntfy notification fires so you know the drive is free to eject manually.
4. If MakeMKV fails or a disc defect is detected, the item becomes `FAILED` with the error message recorded in the queue. You can retry after addressing the issue with `spindle queue retry <id>`.

## Stage 4: Encoding to AV1 (ENCODING -> ENCODED)

1. Ripped items are picked up by the Drapto encoder. The queue shows `ENCODING` with live progress updates as Drapto emits JSON status.
2. Encoded output is written to `<staging_dir>/encoded/`, typically ending in `_encoded.mkv`.
3. When encoding completes, the item flips to `ENCODED`. Failures surface as `FAILED` with the Drapto error text.

Encoding happens in the background, so you can insert the next disc while previous titles encode.

## Stage 5: Organizing & Plex Refresh (ORGANIZING -> COMPLETED)

1. Spindle moves the encoded file into your library, building a Plex-friendly path based on the TMDB metadata. Movies go under `library_dir/movies`, TV episodes go under `library_dir/tv/<Show Name>/Season XX/`.
2. Progress is reported as `ORGANIZING`, progressing from 20% up to 100% as the organizer creates directories, moves files, and calls Plex.
3. Plex scans are triggered for the appropriate library section (Movies vs TV Shows) when credentials are supplied.
4. The final status `COMPLETED` means the media is on disk and Plex has been asked to rescan. Items flagged for review land in your configured `review_dir`; otherwise titles appear in the main library. An ntfy notification confirms the import when the library update succeeds.

## Special Paths: REVIEW and FAILED

- **Review queue** (`REVIEW`): Configuration or validation issues (for example, missing TMDB credentials or duplicate fingerprints) halt the workflow and require manual fixes before retrying (`spindle queue retry <id>`).
- **Needs review flag**: Low-confidence identification keeps the status at `IDENTIFIED` but sets `NeedsReview = true`. Ripping/encoding/organizing still run, and the organizer drops the finished file into `review_dir` while marking the queue `COMPLETED` so the pipeline stays unblocked.
- **Failed items** (`FAILED`): Something went wrong (missing dependency, read error, encoding failure). Fix the root cause, then use `spindle queue retry <id>` to resume; Spindle resets progress to `PENDING` and walks the item back through the pipeline.

Use `spindle queue list` (filter with tools like `grep FAILED`), `spindle queue status` for a tally per lifecycle state, or `spindle queue health` for condensed diagnostics.

## Multi-Disc Sets & Manual Files

- **TV box sets**: Each disc is processed on its own. Enhanced metadata still helps label episodes, and cached TMDB matches keep naming consistent, but there is no waiting period or required order.
- **Manual files**: You can queue already-ripped videos with `spindle add-file /path/to/video.mkv`. These items start at `RIPPED`, skip the optical stages, and go straight to encoding and organization.

## Monitoring & Control Tips

- `spindle show --follow` - tails the daemon log with color formatting.
- `spindle status` - quick summary (daemon running, current disc, queue totals).
- `spindle queue list` - see every item, its status, and fingerprint.
- `spindle queue status` - table of lifecycle counts (pending, ripping, encoding, etc.).
- `spindle queue clear` - prune finished entries without touching active work; add `spindle queue clear-failed` to drop only failed items.
- `spindle stop` - cleanly stop the daemon (planned maintenance, shutdown).

Logs also live in `<log_dir>/spindle-<timestamp>.log` (one file per daemon start) and `<log_dir>/queue.db` (the queue database). Most systems expose a `spindle.log` symlink that points to the latest run. Use standard SQLite tools to inspect the queue if you prefer raw SQL.

## Where Files Live

- **Staging**: `<staging_dir>/rips/` for MakeMKV output, `<staging_dir>/encoded/` for Drapto output while waiting on organization.
- **Library**: Under `library_dir`, using `movies/` and `tv/` subfolders unless customized in the config.
- **Review**: `<review_dir>/` holds encoded files that still need manual identification. Each unidentified disc is stored with a unique filename (for example `unidentified-1.mkv`), and the queue item is marked complete so it doesn’t block subsequent work.
- **Logs & diagnostics**: `<log_dir>/` keeps `spindle-*.log` files for each run, the queue database, and analyzer/debug artifacts.

## Notifications

If `ntfy_topic` is set, Spindle posts compact notifications at key steps: disc detected, disc identified with title/year, rip completed, encoding completed, library import completed, and any errors. You can test the channel any time with `spindle test-notify`.

## Need More Detail?

- Setup and installation: see `README.md`
- Identification deep dive: `docs/content-identification.md`
- Development workflow (if you are hacking on Spindle): `docs/development.md`

With these pieces in mind, you can trust the daemon to run hands-free while still understanding exactly where each disc is in the journey from tray to Plex shelf.
