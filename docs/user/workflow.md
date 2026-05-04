# Spindle Workflow Guide

Status: Active contract / user guide. Exact implementation behavior is defined by code and tests.

Stage-by-stage breakdown of what happens after you insert a disc. See the [README](../../README.md) for installation and initial setup.

The daemon owns disc detection, queue access, and the automated pipeline. Queue commands require a running daemon except `spindle queue clear --all`, which can reset the transient queue while the daemon is stopped. Log tailing (`spindle logs --follow`) requires a running daemon.

## Lifecycle at a Glance

Every item moves through the queue in order. Each item has a **stage** and an
**in_progress** flag. The stages you will see are:

- `identification` - disc queued; MakeMKV scan + TMDB lookup
- `ripping` - video copied to staging; you'll get a notification so the disc can be ejected manually
- `episode_identification` *(TV only)* - WhisperX + OpenSubtitles correlate ripped files to definitive episode numbers
- `encoding` - Drapto transcodes the rip in the background
- `audio_analysis` - refines encoded audio; optionally detects commentary when `commentary.enabled = true`
- `subtitling` *(optional)* - WhisperX transcription generates subtitle sidecars; forced subtitles optionally fetched from OpenSubtitles
- `organizing` - files moved into your library; Jellyfin refresh triggered when configured
- `completed` - all done
- `failed` - an error stopped progress; fix the root cause and retry

The `in_progress` flag indicates whether a stage is actively running. When a
stage finishes, the item advances to the next stage with `in_progress` cleared.

Items may also have a `needs_review` flag set, which routes output to `review_dir` without stopping the workflow.

Use `spindle queue list` to inspect items and `spindle status` for lifecycle totals.

## How the Workflow Runs

Spindle processes items through a single pipeline. Multiple items can be in
flight concurrently -- for example, disc A can be encoding while disc B is
being identified and ripped. The optical drive is guarded by a semaphore so
only one disc operation (identification or ripping) runs at a time.

## Stage 1: Disc Detection & Queueing (identification)

1. The daemon listens for disc-insertion events on your optical drive (`optical_drive`, default `/dev/sr0`); detection can also be triggered manually.
2. When a disc is detected, Spindle fingerprints it and looks for existing queue items.
3. Existing items are handled based on their stage:
   - **In workflow, completed, or failed**: no new work is queued (the disc is already known).
4. New discs are inserted into the queue at stage `identification`.

When a new queue item is actually created, Spindle emits an item-queued
notification. Duplicate or already-known discs do not generate that queue
acceptance notification.

Use `spindle disc pause` to temporarily stop queueing new discs without stopping the daemon. This is useful when you need to swap drives or perform maintenance. Use `spindle disc resume` to resume detection. The pause state resets when the daemon restarts.

## Stage 2: Content Identification (identification)

1. Spindle scans the disc with MakeMKV, capturing the fingerprint and title list.
2. Identification uses KeyDB (if configured), the Blu-ray disc ID cache, and media heuristics to decide TV vs movie. For TV discs, Spindle selects likely episode titles and excludes obvious extras/play-all duplicates, including hidden double-length playlists that only concatenate already-selected episode segments.
3. TMDB search runs using the derived title/season hints. If a confident match is found, Spindle:
   - Stores metadata in `metadata_json`.
   - Writes a rip specification (`rip_spec`) that maps MakeMKV titles to the intended output.
   - Updates `disc_title` to a canonical name (movie: `Title (Year)`, TV: `Show Season XX (Year)` when available).
   - Sends an identification-complete notification.
4. If no confident match is found (or TMDB lookup fails), the item is marked `needs_review` with a reason. **TV-hinted discs fail at identification and do not advance to ripping.** Non-TV/unknown discs continue as degraded items so downstream stages can still run, and the organizer will route output to `review_dir`.
5. Duplicate fingerprints are normally skipped during disc detection before a new item reaches identification.

The queue also reports coarse identification progress for cleanup, scanning/metadata resolution, and finalization so dashboards can show stage activity even though identification is not a stream-oriented stage.

Progress messages in `spindle logs -f` describe the identification steps and any review reasons.

## Stage 3: Ripping the Disc (ripping)

1. Identified items flow into the MakeMKV ripper. The queue updates to `ripping` and streams progress as `makemkvcon` runs.
2. For discs with multiple rip targets (for example TV episodes), the displayed percent is cumulative across the whole ripping stage, not just the current title.
3. Video files are written to `<staging_dir>/<fingerprint-or-queue-id>/ripped/`.
4. Ripped assets are written back into the rip spec after each title finishes so dashboards can advance ripped counts live.
5. When ripping succeeds, the item advances to the next stage and an ntfy notification fires so you know the drive is free to eject manually.
6. If MakeMKV fails or the disc is defective, the item becomes `failed` with the error recorded in the queue.

If the rip cache is enabled, raw rips are stored for reuse along with the identification metadata
(disc fingerprint, rip spec, TMDB metadata). Cached entries can be re-queued through the running daemon without inserting a disc
via `spindle cache process <number>`; restored rips are reprocessed for audio refinement.

For discs with multiple feature-length titles (e.g., director's cut and theatrical cut),
use `spindle cache rip --title` to interactively select which title to rip.

## Stage 4: Episode Identification (episode_identification)

1. For TV shows with an OpenSubtitles API key configured, Spindle compares WhisperX transcripts against OpenSubtitles reference subtitles to map ripped files to definitive episode numbers.
2. Results are written back into the rip specification so encoding/organizing use correct episode labels. The current implementation also supports a conservative disc-1 opening double-length inference: when the first selected title has a probable double-episode runtime profile and the resolved sequence supports it, Spindle can promote that title to a range like `S01E01-E02`.
3. Movies skip this stage. TV items without required API clients (for example, no OpenSubtitles API key) are marked for review with degraded behavior and proceed to encoding. Transient OpenSubtitles failures are retried; runtime transcription/reference-acquisition errors that remain after retry and invalid rip specs fail the stage and require retry after the root cause is fixed.

## Stage 5: Encoding to AV1 (encoding)

1. The encoder builds a job plan from the rip spec and runs Drapto for each planned TV asset (usually one episode, but possibly a conservative range asset such as `S01E01-E02`) or a single file for movies.
2. For multi-file encodes, the displayed percent is cumulative across the whole encoding stage, not just the current file.
3. Encoded output is written to `<staging_dir>/<fingerprint-or-queue-id>/encoded/`. The rip spec is updated after each episode so progress is recoverable and encoded counts can advance live.
4. When encoding completes, the item advances to the next stage. Failures surface as `failed` (with `needs_review = true` for validation/configuration errors).

## Stage 6: Audio Analysis (audio_analysis)

Spindle analyzes encoded files before subtitle generation so downstream stages have refined audio and primary-track metadata. Commentary detection is optional.

1. Collects encoded assets and starts coarse phase progress.
2. When `commentary.enabled = true` and an LLM client is configured, uses WhisperX transcription and LLM classification to identify commentary tracks for exclusion.
3. Runs audio refinement to strip non-English/redundant tracks while preserving primary audio and any detected commentary tracks.
4. Performs post-refinement primary-audio selection and commentary disposition.
5. Updates the rip spec with analysis results for downstream stages. If no encoded assets are available, the stage fails because there is nothing to analyze.

## Stage 7: Subtitle Generation (subtitling)

When `subtitles.enabled = true`, Spindle generates subtitles from the actual audio using WhisperX transcription. Subtitles are generated per encoded asset.

1. Spindle extracts the primary audio track.
2. **WhisperX transcription**: generates canonical transcript artifacts (raw SRT + JSON/alignment output) through a Spindle-owned wrapper that applies VAD-guided long-form transcription settings.
3. **Subtitle formatting**: the subtitle stage filters obvious WhisperX artifacts, uses Stable-TS formatting, and applies a final readability pass.
4. Subtitle filtering and validation use the actual encoded-media duration when available; transcript-tail duration is only a fallback.
5. Spindle intentionally does not use PGS subtitles as final library output. Final primary display subtitles are SRT because SRT works better with Jellyfin and downstream tooling.
6. Subtitling progress is cumulative across the full subtitle stage, and completed subtitle assets are persisted after each item so counts can advance live.
7. **Forced subtitles** (optional): when OpenSubtitles is configured and a forced subtitle track is detected, foreign-parts-only subtitles are downloaded from OpenSubtitles with retry for transient service/network failures and used as-is (no alignment against WhisperX output).
8. SRTs are written beside the encoded media as `<basename>.<lang>.srt` (for example, `Movie.en.srt`). If subtitle formatting fails, or if severe subtitle validation issues are detected for an episode, that episode is recorded as a subtitle failure and processing continues with other episodes when possible.

`spindle gensubtitle /path/to/video.mkv` generates display subtitles for an existing encode. With `--fetch-forced`, it derives title/year or TV season/episode context from the filename, uses TMDB metadata when configured, and searches OpenSubtitles using the credentials and languages from `config.toml`. Use `--tmdb-id`, `--media-type`, `--season`, and `--episode` when filename inference is insufficient. By default, generated regular and forced subtitles are muxed into MKV output when subtitle muxing is enabled; `--external` writes sidecar SRT files instead.

## Stage 8: Organizing & Jellyfin Refresh (organizing -> completed)

1. Spindle moves encoded artifacts into your library using TMDB metadata. Movies land under `library_dir/movies`, TV under `library_dir/tv/<Show>/Season XX/`.
2. Organizing progress is byte-based across the total copy workload, so dashboards can show both percent and bytes copied while files are being placed.
3. Final assets are written back into the rip spec after each copied item so completed counts can advance live.
4. When `needs_review` is set, review routing is applied. Movies go fully to `review_dir`. TV is organized per episode: clean resolved episodes go to the library, while unresolved or episode-flagged outputs go to `review_dir`. The queue item still completes.
5. Jellyfin scans are triggered after organizing when credentials are supplied.

## Review vs Failed

- **`failed` stage**: Something went wrong and the workflow stopped. This includes external tool failures, read errors, validation issues, manual stop requests (`spindle queue stop <id>`), and episode-identification transcription/reference acquisition failures (for example WhisperX or OpenSubtitles service/auth/network problems). Items stopped by user have `"Stop requested by user"` in their `review_reason` array. Fix the root cause, then use `spindle queue retry <id>` to requeue.
- **`needs_review` flag**: Workflow continues but manual-review routing is enabled instead of failing the entire pipeline. For movies this means the final artifact goes to `review_dir`. For TV, the flag is aggregate: clean resolved episodes may still land in the library while unresolved or episode-flagged outputs go to `review_dir`. This is used for low-confidence matches, missing metadata, unresolved episode numbers after successful episode-ID reference acquisition, or other issues that need manual attention but shouldn't block processing.

## Recovery Procedures

### Retrying failed items

1. Check the error: `spindle queue show <id>` shows the error message and which stage failed.
2. Fix the root cause (e.g., disc is readable, network is available, disk space).
3. Retry: `spindle queue retry <id>`. The item restarts from the failed stage, not from the beginning.
4. Retry all failed items at once: `spindle queue retry` (no ID).

### Retrying a single failed episode (TV)

If one episode in a batch failed but others succeeded:
1. `spindle queue retry <id> --episode s01e05` clears only that episode's failed asset.
2. The item is reprocessed for that episode only; already-completed episodes are skipped.

### Items routed to review

Files in `review_dir` need manual attention:
1. Check the review reason: `spindle queue show <id>` (look for `review_reason`).
2. Common reasons:
   - **Low-confidence TMDB match**: The disc title didn't match well. Move the file to the correct library folder manually, or update the Blu-ray disc ID cache / KeyDB inputs and retry.
   - **Unresolved episode numbers**: Episode identification ran successfully but couldn't map all episodes confidently. Check the file names and move to the correct library folder.
   - **SRT validation review issues**: Subtitles may have quality problems. Review the SRT file and fix or regenerate with `spindle gensubtitle`. Routine below-threshold QC observations do not route items to review.
3. After manually organizing files, clear the completed item while the daemon is running: `spindle queue clear <id>`.

### Stuck items

If items appear stuck (in-progress but not advancing):
1. Check if the daemon is running: `spindle status`. When stopped, this reports only `Daemon stopped`.
2. If the daemon crashed, restart it: `spindle start`. Stale in-progress items are automatically recovered on startup.
3. If you want to discard the transient queue while the daemon is stopped, run `spindle queue clear --all`. This deletes only the queue DB files, not staging or media outputs.

### Stopping an item

`spindle queue stop <id>` marks the item as failed with a user-stop reason. The item will not be automatically reprocessed even if the same disc is re-inserted. Use `spindle queue retry <id>` to un-stop it.

## Monitoring & Control Tips

- `spindle logs -f` - tail daemon logs (requires running daemon).
- `spindle status` - reports `Daemon stopped` when the daemon is not running; otherwise shows daemon state, dependency checks, library paths, and queue counts from the daemon API.
- `spindle queue list` - queue inspection through the daemon API.
- `spindle queue retry <id>` - retry failed items only.
- `spindle queue stop <id>` - halt processing for a specific item (takes effect after the current stage if already running).
- `spindle disc pause` / `spindle disc resume` - pause or resume detection of new discs (already-queued items continue processing).
- `spindle stop` - cleanly stop the daemon.

## Where Files Live

- **Staging**: `<staging_dir>/<fingerprint-or-queue-id>/ripped/` for MakeMKV output, `<staging_dir>/<fingerprint-or-queue-id>/encoded/` for Drapto output while waiting on organization. Subtitle sidecars land beside encoded media.
- **Library**: Under `library_dir`, using `movies/` and `tv/` subfolders unless customized in config.
- **Review**: `<review_dir>/` holds outputs that require manual attention. Items routed here still complete so the pipeline stays unblocked.
- **State**: `<state_dir>/` stores `spindle-*.log` (one per daemon start, DEBUG-level JSON), `daemon.log` symlink/hardlink to the latest run (when available), and the queue database (`queue.db`). Log retention is controlled by `[logging].retention_days` in `config.toml` (default 60; set 0 to disable pruning).

## Notifications

If `ntfy_topic` is set, Spindle posts compact notifications at key steps: item queued, identification completed, rip cache hit, rip completed (including drive-available notice), encoding completed, final clean completion, final review-required completion, queue backlog start/finish, and fatal errors. Items routed to `review_dir` are treated as a problem outcome and generate an explicit review-required notification rather than looking like a clean success. You can test the channel any time with `spindle test-notify`.
