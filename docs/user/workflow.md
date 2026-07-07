# Spindle Workflow Guide

Status: Active contract / user guide. Exact implementation behavior is defined by code and tests.

Stage-by-stage breakdown of what happens after you insert a disc. See the [README](../../README.md) for installation and initial setup, and [CONFIG.md](../CONFIG.md) for config loading, validation, and feature gates.

The daemon owns disc detection, queue access, and the automated pipeline. Queue commands require a running daemon except `spindle queue clear --all`, which can reset the transient queue while the daemon is stopped. `spindle logs --follow` and filtered log queries require a running daemon; unfiltered `spindle logs` can read the log file directly.

## Lifecycle at a Glance

Every item moves through the queue in order. Each item has a **stage** and an
**in_progress** flag. The stages you will see are:

- `identification` - disc queued; MakeMKV scan plus TMDB/disc metadata resolution
- `ripping` - video copied to staging or restored from rip cache; you'll get a notification when the drive is available
- `episode_identification` *(TV only)* - WhisperX plus OpenSubtitles correlate ripped files to definitive episode numbers
- `encoding` - Reel target-quality mode transcodes the rip in the background
- `audio_analysis` - refines encoded audio; optionally detects commentary when `commentary.enabled = true`
- `subtitling` *(optional)* - WhisperX transcription generates one English display SRT per output
- `organizing` - files are copied/moved into your library or review area; Jellyfin refresh is triggered when configured
- `completed` - all done
- `failed` - an error or user stop halted progress; fix the root cause and retry or clear

The `in_progress` flag indicates whether a stage is actively running. When a
stage finishes, the item advances to the next stage with `in_progress` cleared.

Items may also have a `needs_review` flag set. Review routes some or all output
to `review_dir` without necessarily stopping the workflow.

Use `spindle queue list` to inspect items and `spindle status` for lifecycle totals.

## How the Workflow Runs

Spindle can process multiple items at different stages at the same time. Shared
resources are protected by one-at-a-time semaphores: the optical drive for
identification/ripping, the encoder for Reel, and WhisperX for transcription
work.

## Stage 1: Disc Detection and Queueing

1. The daemon listens for disc-insertion events on `makemkv.optical_drive` (default `/dev/sr0`); detection can also be triggered manually with `spindle disc detect`.
2. When a disc is detected, Spindle resolves a mount point, fingerprints the disc, and checks for an existing queue item with the same fingerprint.
3. Duplicate fingerprints are not queued again. In-workflow, completed, failed, and user-stopped items all suppress a new queue item. Non-user-stopped terminal items with a placeholder title may have their title refreshed from the reinserted disc label.
4. New discs are inserted into the queue at stage `identification`.

When a new queue item is actually created, Spindle emits an item-queued
notification. Duplicate or already-known discs do not generate that queue
acceptance notification.

Use `spindle disc pause` to temporarily stop queueing new discs without stopping the daemon. Already-queued items continue processing. Use `spindle disc resume` to resume detection. The pause state resets when the daemon restarts.

## Stage 2: Content Identification (`identification`)

1. Spindle probes the disc source, optionally runs `bd_info` for Blu-ray metadata, and scans the disc with MakeMKV.
2. Title resolution uses KeyDB when available, then BDInfo, MakeMKV, the disc label, and finally a fallback title.
3. Identification uses the Blu-ray disc ID cache when enabled and valid. Otherwise it searches TMDB using the cleaned title, year hints, and TV/movie hints.
4. For TV discs, Spindle selects likely episode titles and excludes obvious extras, duplicates, and play-all style duplicates.
5. When a confident match is found, Spindle stores metadata, writes the RipSpec, updates `disc_title` to a canonical name, and sends an identification-complete notification.
6. If no confident TMDB match is found, the item is marked for review. TV-hinted discs fail at identification and do not advance to ripping; non-TV/unknown discs continue as degraded items and the organizer routes output to review.

The queue reports coarse identification progress for cleanup, scanning/metadata resolution, and finalization so dashboards can show activity even though identification is not stream-oriented.

## Stage 3: Ripping (`ripping`)

1. The ripper resets the item's staging directory and first tries to restore raw MKVs from the rip cache when it is enabled and complete.
2. If there is no usable cache hit, the disc monitor is paused, MakeMKV rips the selected titles, and the monitor is resumed afterward.
3. Movies use the primary title selector. TV uses titles referenced by episode placeholders. Unknown/fallback media rips all titles above `makemkv.min_title_length`.
4. Video files are written to `<staging_dir>/<fingerprint-or-queue-id>/ripped/`.
5. Ripped assets are written back into the RipSpec after each title finishes so dashboards can advance ripped counts live.
6. The displayed percent is cumulative across the whole ripping stage, not just the current title.
7. When ripping succeeds, an ntfy notification fires so you know the drive is free to eject manually.

If the rip cache is enabled, fresh raw rips are stored for reuse along with the identification metadata. Cached entries can be re-queued through the running daemon without inserting a disc via `spindle cache process <number>`; the item starts at ripping, restores the cache, and then runs the normal encoding/audio/subtitle/organize stages.

For discs with multiple feature-length titles, use `spindle cache rip --choose` (interactive) or `spindle cache rip --title <id>` while the daemon is stopped to select which title to cache.

## Stage 4: Episode Identification (`episode_identification`)

1. Movies and non-TV items skip this stage.
2. For TV, Spindle transcribes ripped assets with WhisperX, fetches reference subtitles from OpenSubtitles, and matches content to TMDB season episodes.
3. Results are written back into the RipSpec so encoding and organizing use correct episode labels. The current implementation can also conservatively infer an opening double-length episode on disc 1 when runtime and sequence evidence support it.
4. TV items without required matcher clients, with no valid transcriptions, or with no reference subtitles are marked for review and continue as degraded items.
5. TMDB season acquisition errors, transcription errors, and reference-acquisition errors that remain after OpenSubtitles retry fail the stage so the external dependency can be fixed and the item retried.

## Stage 5: Encoding (`encoding`)

1. The encoder builds jobs from completed ripped assets: one job for a movie or one job per TV asset.
2. Reel runs in target-quality mode with Reel defaults.
3. For multi-file encodes, displayed percent is cumulative across the whole encoding stage.
4. Encoded output is written to `<staging_dir>/<fingerprint-or-queue-id>/encoded/`.
5. The RipSpec and encoding telemetry snapshot are updated as jobs progress so progress is recoverable and encoded counts can advance live.
6. If Reel validation fails, the affected asset is flagged for review. If any encode job fails, the stage fails after recording per-asset failure state.

## Stage 6: Audio Analysis (`audio_analysis`)

Audio analysis runs after encoding and before subtitle generation.

1. The stage collects completed encoded assets and fails if none exist.
2. When `commentary.enabled = true` and an LLM client is configured, it examines non-primary tracks on the first encoded asset, using transcription similarity to exclude stereo downmixes and LLM classification to detect commentary.
3. Audio refinement strips non-English/redundant tracks while preserving primary audio and detected commentary tracks. Refinement failure is logged as a warning and the workflow continues with existing tracks.
4. The stage performs post-refinement primary-audio selection and commentary disposition/labeling.
5. Audio analysis results are stored in the RipSpec for dashboards and downstream stages.

## Stage 7: Subtitle Generation (`subtitling`)

When `subtitles.enabled = false`, this stage logs a skip and advances.

When enabled, Spindle generates subtitles per encoded asset:

1. Selects the primary audio track.
2. Generates canonical WhisperX transcript artifacts through the shared transcription wrapper.
3. Formats display subtitles with hallucination filtering, Stable-TS formatting, line wrapping, retiming, and SRT validation.
4. Uses the encoded media duration for validation when available; transcript duration is only a fallback.
5. Writes one primary English display subtitle as `<basename>.<lang>.srt` beside the encoded media.
6. If `subtitles.mux_into_mkv = true` (the default), the generated subtitle is muxed into the MKV and existing subtitle tracks are replaced. If muxing fails or muxing is disabled, the SRT sidecar remains available.
7. Subtitle failures are recorded per asset and processing continues with other assets when possible. If every attempted subtitle job fails, the stage fails.

Spindle intentionally does not use PGS subtitles as final library output. Final primary display subtitles are SRT because SRT works better with Jellyfin and downstream tooling.

`spindle debug subtitle /path/to/video.mkv` generates a WhisperX English SRT for an existing encode. By default, the generated subtitle is muxed into MKV output when subtitle muxing is enabled; `--external` writes a sidecar SRT instead.

## Stage 8: Organizing and Jellyfin Refresh (`organizing` -> `completed`)

1. The organizer chooses subtitled assets when present, otherwise encoded assets.
2. Clean movie outputs are copied to `library_dir/movies/Title (Year)/Title (Year).mkv` by default.
3. Clean TV outputs are copied to `library_dir/tv/<Show>/Season NN/<Show> - S01E01.mkv` by default, with episode ranges such as `S01E01-E02` when applicable.
4. Organizing progress is byte-based across the total copy workload.
5. Final assets are written back into the RipSpec after each copied item so completed counts can advance live.
6. When `needs_review` is set, movies and unresolved TV items go to `review_dir`. For TV with some clean resolved episodes, clean episodes go to the library and unresolved/flagged episodes go to review.
7. Sidecar SRTs next to the source video are copied next to the final video.
8. Jellyfin scans are triggered after organizing when Jellyfin is enabled and credentials are supplied.
9. Staging is cleaned after successful library/review routing.

## Review vs Failed

- **`failed` stage**: Something stopped progress. This includes external tool failures, read errors, validation/configuration issues, queue persistence failures, and manual cancel requests (`spindle queue cancel <id>`). Fix the root cause, then use `spindle queue retry <id>` to requeue. A normal stage failure records `failed_at_stage` and the retry resumes from that stage. A user stop records the review reason `Stop requested by user` and retry un-stops the item.
- **`needs_review` flag**: Workflow can continue but manual-review routing is enabled. For movies this means final output goes to `review_dir`. For TV, the flag is aggregate: clean resolved episodes may still land in the library while unresolved or episode-flagged outputs go to `review_dir`.

## Recovery Procedures

### Retrying failed items

1. Check the error: `spindle queue show <id>` shows the error message and failed stage when one was recorded.
2. Fix the root cause (disc readability, network, credentials, disk space, etc.).
3. Retry: `spindle queue retry <id>`.
4. Retry all failed items at once: `spindle queue retry` with no ID.

### Retrying a single failed episode (TV)

If one episode in a batch failed but others succeeded:

1. `spindle queue retry <id> --episode s01e05` clears only that episode's failed asset.
2. The item is reprocessed for that episode only; already-completed episodes are skipped.

### Items routed to review

Files in `review_dir` need manual attention:

1. Check the review reason: `spindle queue show <id>` (look for `review_reason`).
2. Common reasons:
   - **Low-confidence TMDB match**: The disc title did not match well. Move the file to the correct library folder manually, or update the Blu-ray disc ID cache / KeyDB inputs and retry.
   - **Unresolved episode numbers**: Episode identification ran but could not map all episodes confidently. Check the file names and move to the correct library folder.
   - **SRT validation review issues**: Subtitles may have quality problems. Review the SRT file and fix or regenerate with `spindle debug subtitle`. Routine below-threshold QC observations do not route items to review.
3. After manually organizing files, clear the completed item while the daemon is running: `spindle queue clear <id>`.

### Stuck items

If items appear stuck (in-progress but not advancing):

1. Check if the daemon is running: `spindle status`. When stopped, this reports only `Daemon stopped`.
2. If the daemon crashed, restart it: `spindle start`. Stale in-progress items are automatically recovered on startup.
3. If you want to discard the transient queue while the daemon is stopped, run `spindle queue clear --all`. This deletes only the queue DB files, not staging or media outputs.

### Canceling an item

`spindle queue cancel <id>` marks the item as failed with a user-stop review reason. The item will not be automatically reprocessed even if the same disc is re-inserted. Use `spindle queue retry <id>` to un-cancel it.

## Monitoring and Control Tips

- `spindle logs -f` - tail daemon logs through the API (requires running daemon).
- `spindle logs` - tail the daemon log file directly.
- `spindle status` - reports `Daemon stopped` when the daemon is not running; otherwise shows daemon state, dependency checks, library paths, and queue counts from the daemon API.
- `spindle queue list` - queue inspection through the daemon API.
- `spindle queue retry <id>` - retry failed items only.
- `spindle queue cancel <id>` - halt processing for a specific item; if already running, finalization is ignored after the stop state wins.
- `spindle disc pause` / `spindle disc resume` - pause or resume detection of new discs; already-queued items continue processing.
- `spindle staging list` / `spindle staging clean` - inspect or remove stale staging directories. Safe clean asks the daemon for active queue items; `staging clean --all` skips that daemon check, but `queue-*` fallback staging directories remain protected.
- `spindle discid list` / `spindle discid clear` - inspect or reset the optional disc ID cache.
- `spindle stop` - cleanly stop the daemon.

## Where Files Live

- **Staging**: `<staging_dir>/<fingerprint-or-queue-id>/ripped/` for MakeMKV output, `<staging_dir>/<fingerprint-or-queue-id>/encoded/` for Reel output while waiting on organization. Subtitle sidecars land beside encoded media.
- **Library**: Under `library_dir`, using `movies/` and `tv/` subfolders unless customized in config. Movie outputs include a per-movie folder; TV outputs use show and season folders.
- **Review**: `<review_dir>/<sanitized-primary-reason>_<fingerprint-prefix>/` holds outputs that require manual attention. The fingerprint prefix is the first 8 characters when available, otherwise `id<queue-id>`. Items routed here still complete so the pipeline stays unblocked.
- **State**: `<state_dir>/` stores `spindle-*.log` (one per daemon start, DEBUG-level JSON), `daemon.log` symlink/hardlink to the latest run when available, and the queue database (`queue.db`). Log retention is controlled by `[logging].retention_days` in `config.toml` (default 60; values less than or equal to 0 currently fall back to 30 days).
- **Cache**: XDG cache contains rip cache entries and the optional disc ID cache file.

## LLM Use

Spindle uses the configured OpenRouter-compatible LLM client only for bounded
classification/verification tasks:

- TV episode ID can ask whether one ripped transcript excerpt and one reference
  subtitle excerpt are from the same episode.
- Commentary detection can ask whether a transcribed non-primary audio track is
  commentary.

Spindle does not use LLMs to identify discs from memory, choose arbitrary
episodes, format subtitles, encode video, or control the queue.

## Notifications

If `ntfy_topic` is set, Spindle posts compact notifications at key steps: item queued, identification completed, rip cache hit, rip completed (including drive-available notice), encoding completed, final clean completion, final review-required completion, queue backlog start/finish, and processing errors. Items routed to `review_dir` generate an explicit review-required notification rather than looking like a clean success. You can test the channel any time with `spindle debug notify`.
