# Spindle Workflow Guide

Stage-by-stage breakdown of what happens after you insert a disc. See the [README](../README.md) for installation and initial setup.

The daemon owns disc detection and the automated pipeline. Queue commands work with or without the daemon; log tailing (`spindle show`) requires a running daemon.

## Lifecycle at a Glance

Every item moves through the queue in order. The statuses you will see are:

- `PENDING` - disc queued, awaiting identification
- `IDENTIFYING` -> `IDENTIFIED` - MakeMKV scan + TMDB lookup completed
- `RIPPING` -> `RIPPED` - video copied to staging; you’ll get a notification so the disc can be ejected manually
- `EPISODE_IDENTIFYING` -> `EPISODE_IDENTIFIED` *(optional)* - for TV discs with OpenSubtitles enabled, WhisperX + OpenSubtitles correlate ripped files to definitive episode numbers
- `ENCODING` -> `ENCODED` - Drapto transcodes the rip in the background
- `AUDIO_ANALYZING` -> `AUDIO_ANALYZED` *(optional)* - detects commentary tracks for exclusion (requires `commentary.enabled = true`)
- `SUBTITLING` -> `SUBTITLED` *(optional)* - WhisperX transcription generates subtitle sidecars; forced subtitles optionally fetched from OpenSubtitles
- `ORGANIZING` -> `COMPLETED` - files moved into your library; Jellyfin refresh triggered when configured
- `FAILED` - an error stopped progress; fix the root cause and retry

Items may also have a `NeedsReview` flag set, which routes output to `review_dir` without stopping the workflow.

Use `spindle queue list` to inspect items and `spindle queue health` for lifecycle totals.

## How the Workflow Runs

Spindle runs two independent lanes:

- **Foreground**: identification and ripping.
- **Background**: episode identification, encoding, audio analysis (commentary detection), subtitles, and organizing.

This lets you rip disc B while disc A is still encoding or organizing.

## Stage 1: Disc Detection & Queueing (PENDING)

1. The daemon polls your optical drive (`optical_drive`, default `/dev/sr0`).
2. When a disc is detected, Spindle fingerprints it and looks for existing queue items.
3. Existing items are handled based on status:
   - **In workflow or completed**: no new work is queued.
   - **Failed or review**: the item is reset to `PENDING` so it can be reprocessed.
4. New discs are inserted into the queue with status `PENDING`.

Disc-detected notifications are emitted when identification begins.

Use `spindle disc pause` to temporarily stop queueing new discs without stopping the daemon. This is useful when you need to swap drives or perform maintenance. Use `spindle disc resume` to resume detection. The pause state resets when the daemon restarts.

## Stage 2: Content Identification (IDENTIFYING -> IDENTIFIED)

1. Spindle scans the disc with MakeMKV, capturing the fingerprint and title list.
2. Identification uses KeyDB (if configured), optional overrides, and heuristics to decide TV vs movie. Heuristics include season markers ("Season", `Sxx`), "complete series" strings, and discs dominated by episode-length titles (~18–35 minutes).
3. TMDB search runs using the derived title/season hints. If a confident match is found, Spindle:
   - Stores metadata in `metadata_json`.
   - Writes a rip specification (`rip_spec`) that maps MakeMKV titles to the intended output.
   - Updates `disc_title` to a canonical name (movie: `Title (Year)`, TV: `Show Season XX (Year)` when available).
   - Sends a notification when a year is known.
4. If no confident match is found (or TMDB lookup fails), the item is marked `NeedsReview` with a reason. The status remains `IDENTIFIED` so downstream stages can still run, and the organizer will route output to `review_dir`.
5. Duplicate fingerprints are treated as immediate failure: the item is placed in `FAILED` with `NeedsReview = true` and the workflow stops.

Progress messages in `spindle show --follow` describe the identification steps and any review reasons.

## Stage 3: Ripping the Disc (RIPPING -> RIPPED)

1. Identified items flow into the MakeMKV ripper. The queue updates to `RIPPING` and streams progress as `makemkvcon` runs.
2. Video files are written to `<staging_dir>/<fingerprint-or-queue-id>/rips/`.
3. Rips are post-processed to keep the primary audio stream (preferring English when available); other audio streams are dropped.
4. When ripping succeeds, the item is marked `RIPPED` and an ntfy notification fires so you know the drive is free to eject manually.
5. If MakeMKV fails or the disc is defective, the item becomes `FAILED` with the error recorded in the queue.

If the rip cache is enabled, raw rips are stored for reuse along with the identification metadata
(disc fingerprint, rip spec, TMDB metadata). Cached entries can be re-queued without inserting a disc
via `spindle cache process <number>`; restored rips are reprocessed for audio refinement.

## Stage 4: Episode Identification (EPISODE_IDENTIFYING -> EPISODE_IDENTIFIED)

1. For TV shows with OpenSubtitles enabled, Spindle compares WhisperX transcripts against OpenSubtitles references to map ripped files to definitive episode numbers.
2. Results are written back into the rip specification so encoding/organizing use correct episode labels.
3. Movies, discs without OpenSubtitles enabled, or invalid rip specs skip this stage and proceed to encoding.

## Stage 5: Encoding to AV1 (ENCODING -> ENCODED)

1. The encoder builds a job plan from the rip spec and runs Drapto for each episode (or a single file for movies).
2. When `preset_decider.enabled = true`, an OpenRouter LLM can select a Drapto preset (`clean`, `grain`, or default); otherwise the default profile is used.
3. Encoded output is written to `<staging_dir>/<fingerprint-or-queue-id>/encoded/`. The rip spec is updated after each episode so progress is recoverable.
4. When encoding completes, the item flips to `ENCODED`. Failures surface as `FAILED` (with `NeedsReview = true` for validation/configuration errors).

## Stage 6: Audio Analysis (AUDIO_ANALYZING -> AUDIO_ANALYZED)

When `commentary.enabled = true`, Spindle analyzes encoded files to detect and exclude commentary tracks before subtitle generation.

1. Extracts audio from each encoded asset.
2. Uses WhisperX transcription and LLM classification to identify commentary vs. primary audio tracks.
3. Updates the rip spec with analysis results for downstream stages.
4. Skipped when commentary detection is disabled or no encoded assets exist.

## Stage 7: Subtitle Generation (SUBTITLING -> SUBTITLED)

When `subtitles_enabled = true`, Spindle generates subtitles from the actual audio using WhisperX transcription. Subtitles are generated per encoded asset.

1. Spindle extracts the primary audio track.
2. **WhisperX transcription**: transcribes with the `large-v3` model, aligns with wav2vec2, and formats with Stable-TS. If Stable-TS fails, the raw WhisperX SRT is used.
3. **Forced subtitles** (optional): when `--fetch-forced` is used and OpenSubtitles is configured, foreign-parts-only subtitles are fetched from OpenSubtitles and aligned against the WhisperX output via text-based matching.
4. SRTs are written beside the encoded media as `<basename>.<lang>.srt` (for example, `Movie.en.srt`).

`spindle gensubtitle /path/to/video.mkv` runs the same pipeline for an existing encode. It derives a title from the filename and uses TMDB for metadata context.

## Stage 8: Organizing & Jellyfin Refresh (ORGANIZING -> COMPLETED)

1. Spindle moves encoded artifacts into your library using TMDB metadata. Movies land under `library_dir/movies`, TV under `library_dir/tv/<Show>/Season XX/`.
2. When `NeedsReview` is set, or when the library target is unavailable, outputs are moved to `review_dir` instead. The queue item still completes, but progress is labeled "Manual review".
3. Jellyfin scans are triggered after organizing when credentials are supplied.

## Review vs Failed

- **`FAILED` status**: Something went wrong and the workflow stopped. This includes external tool failures, read errors, validation issues, duplicate fingerprints, and manual stop requests (`spindle queue stop <id>`). Items stopped by user have `ReviewReason = "Stop requested by user"`. Fix the root cause, then use `spindle queue retry <id>` to requeue.
- **`NeedsReview` flag**: Workflow continues but final artifacts are routed to `review_dir` instead of the library. The item completes with progress stage "Manual review" so the pipeline stays unblocked. This is used for low-confidence matches, missing metadata, or other issues that need manual attention but shouldn't block processing.

## Monitoring & Control Tips

- `spindle show --follow` - tail daemon logs (requires running daemon).
- `spindle status` - status summary; uses the daemon when available, otherwise inspects the queue database.
- `spindle queue list`, `spindle queue status`, `spindle queue health` - queue inspection (works with or without daemon).
- `spindle queue retry <id>` - retry failed items only.
- `spindle queue stop <id>` - halt processing for a specific item (takes effect after the current stage if already running).
- `spindle queue reset-stuck` - return in-flight items to the start of their current stage.
- `spindle disc pause` / `spindle disc resume` - pause or resume detection of new discs (already-queued items continue processing).
- `spindle stop` - cleanly stop the daemon.

## Where Files Live

- **Staging**: `<staging_dir>/<fingerprint-or-queue-id>/rips/` for MakeMKV output, `<staging_dir>/<fingerprint-or-queue-id>/encoded/` for Drapto output while waiting on organization. Subtitle sidecars land beside encoded media.
- **Library**: Under `library_dir`, using `movies/` and `tv/` subfolders unless customized in config.
- **Review**: `<review_dir>/` holds outputs that require manual attention. Items routed here still complete so the pipeline stays unblocked.
- **Logs & diagnostics**: `<log_dir>/` stores `spindle-*.log` (one per daemon start), `spindle.log` symlink to the latest run (when available), the queue database (`queue.db`), and per-item logs under `<log_dir>/items/`. Log retention is controlled by `[logging].retention_days` in `config.toml` (default 60; set 0 to disable pruning).

## Notifications

If `ntfy_topic` is set, Spindle posts compact notifications at key steps: disc detected, disc identified (with year when available), rip completed, encoding completed, library import completed, and any errors. You can test the channel any time with `spindle test-notify`.

