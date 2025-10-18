# Content Identification

Reference notes for how the Go daemon classifies discs and prepares metadata so Plex receives the right title and release year for every rip. Keep this in sync with `internal/identification`, `internal/disc`, and `internal/identification/tmdb`.

## Pipeline Summary

1. **MakeMKV scan** – `internal/disc.Scanner` calls `makemkvcon info` to capture the title list; Spindle now computes its own fingerprint from disc metadata before the scan runs.
2. **bd_info enrichment** – The scanner always runs `bd_info` when the binary is available, harvesting Blu-ray disc IDs, studio hints, and a cleaned disc name. It still only overwrites the MakeMKV title when the original metadata is empty or generic.
3. **KEYDB lookup** – When `bd_info` captures an AACS Disc ID, the identifier consults the KEYDB catalog (automatically refreshed weekly) to fetch curated titles/aliases before doing any string heuristics.
4. **Identification stage** – `internal/identification.Identifier` enriches queue items, checks for duplicates, and performs TMDB lookups using the enhanced title OR KEYDB alias data.
4. **TMDB client** – `internal/identification/tmdb` wraps the REST API with simple rate limiting and caching to avoid duplicate requests during a run.
5. The stage writes `MetadataJSON` and `RipSpecData` back to the queue so downstream stages can pick title selections and user-facing details.

## Disc Scan Details

- MakeMKV output is parsed into `disc.ScanResult`, preserving the fingerprint and a normalized list of titles (id, name, duration in seconds).
- **bd_info integration**: The scanner always executes `bd_info` when present so that Blu-ray discs expose their unique Disc ID for downstream lookups. The richer metadata includes:
  - Volume identifier (e.g., "00000095_50_FIRST_DATES")
  - Disc name parsed from volume identifier
  - Year extraction from volume identifier when available
  - Studio information extraction from provider data (e.g., "Sony Pictures")
  - Blu-ray and AACS detection flags
  - Provider information when available
- Generic label detection uses patterns like `LOGICAL_VOLUME_ID`, `DVD_VIDEO`, `BLURAY`, `BD_ROM`, `UNTITLED`, `UNKNOWN DISC`, numeric-only, and short alphanumeric codes.
- Enhanced titles from bd_info replace generic MakeMKV titles before TMDB lookup.
- KEYDB aliases override both MakeMKV and bd_info names when a Disc ID match exists (e.g., translating `VOLUME_ID [Michael Clayton]` to “Michael Clayton”).

### Title Source Priority

1. **KEYDB match** – If the Disc ID is known, the curated KEYDB title wins immediately.
2. **MakeMKV main title** – The first MakeMKV title is preferred when it looks like a real title.
3. **bd_info disc name** – Used when the MakeMKV title is missing or technical noise.
4. **Queue item title** – Falls back to whatever label the user originally queued.
5. **Derived path / Unknown** – Ultimately `deriveTitle` or `"Unknown Disc"` keep the pipeline moving when no metadata is available.
- Fingerprints come from hashing the disc's unencrypted metadata (BDMV structures for Blu-ray, IFO files for DVD). If hashing fails, treat it as a mount/drive issue.
- Raw JSON is stored alongside parsed data to help with later diagnostics (`rip_spec_data` contains the structured payload).

## Duplicate Detection & Review Flow

- The identifier checks `queue.Store.FindByFingerprint`. If another item already claimed the fingerprint, the current disc is moved straight to `REVIEW` with the message “Duplicate disc fingerprint”.
- When TMDB lookup fails or produces no confident match, the identifier now flags the queue item for manual review but still allows ripping and Drapto encoding to complete. The organizer stage then relocates the encoded file into `<review_dir>` and marks the queue item `COMPLETED` once the artifact is safely staged, so automation continues uninterrupted.
- Review filenames are generated from the review reason whenever possible (for example `no-confident-tmdb-match-1.mkv`); if you prefer a different scheme, adjust the prefix logic in `internal/organizer/organizer.go`.
- Notifications fire through `notifications.Service.Publish` to announce newly detected discs and confirmed matches when a release year is known; identification guidance and review prompts stay in the logs to reduce notification noise.

## TMDB Matching

- All lookups route through `tmdb.Client.SearchMovieWithOptions` with enhanced search parameters. The identifier keeps an in-memory cache keyed by the normalized query to avoid hammering the API.
- A simple rate limiter (250 ms minimum gap) protects against short bursts when multiple discs enter the same stage.
- **Enhanced search parameters** from bd_info and MakeMKV data:
  - Year filtering via `primary_release_year` (extracted from bd_info)
  - Runtime range filtering (±10 minutes from main title duration)
  - Studio information extraction (available for future filtering)
- **Confidence scoring logic**:
  - Exact title matches: Accept if vote_average ≥ 2.0 (lenient for perfect matches)
  - Partial matches: Require vote_average ≥ 3.0 and minimum calculated score
  - Score formula: title_match + (vote_average/10) + (vote_count/1000)
- The first candidate above the confidence threshold wins. The chosen title is written into `MetadataJSON`, the DiscTitle is updated to "Title (Year)" format, and echoed in the progress message ("Identified as: …").
- **Enhanced logging**: All TMDB queries, responses, and confidence scoring are logged for transparency.

## Output Shape

The identifier persists two JSON blobs on the queue item:

- `MetadataJSON` – enhanced TMDB fields (`id`, canonical title/name, `release_date`, overview, media_type, vote stats). The release_date enables proper year extraction for Plex filename generation.
- `RipSpecData` – structured payload with `fingerprint`, `content_key`, `metadata`, and `titles`. Each title carries a stable `content_fingerprint` derived from duration/track metadata so episodes or bonus features can be tracked independently of the disc hash.

**Title Propagation**: After successful identification, `item.DiscTitle` is updated from the raw disc title to the proper TMDB title with year format (e.g., "50 First Dates (2004)"), ensuring all subsequent stages use the clean, properly formatted title.

Downstream stages rely on these fields for logging, rip configuration, and Plex naming.

## Configuration Knobs

Relevant settings live in `internal/config`:

- `tmdb_api_key`, `tmdb_base_url`, `tmdb_language` – TMDB connectivity.
- `tmdb_confidence_threshold` – defined for future tuning but the current confidence logic still uses hard-coded vote/score thresholds.
- `optical_drive` – MakeMKV device path (defaults to `/dev/sr0`).
- `keydb_path`, `keydb_download_url`, `keydb_download_timeout` – KEYDB cache location and refresh behaviour.
- Logging hints: `log_dir` determines where queue snapshots and structured logs land for inspection.

Update this list when the identifier begins consuming additional config (runtime hints, enhanced metadata, etc.).

## Failure Modes & Mitigation

- **MakeMKV scan failure** – surfaces as `FAILED` with `MakeMKV disc scan failed`. Verify the binary location and drive permissions.
- **bd_info unavailability** – If `bd_info` isn’t on PATH (libbluray-utils not installed), the scanner logs at info level (“bd_info command not found; skipping enhanced disc metadata”) and continues with MakeMKV data only. Install `libbluray-utils` for enhanced disc identification.
- **Missing TMDB matches** – item is flagged for review, finishes rip/encode, and then is marked complete with the encoded file parked under `review_dir`. Adjust the metadata manually and rerun `spindle queue retry <id>` (if you want Spindle to reorganize the file after you update metadata) or simply leave the queue entry as-is once you've handled it.
- **Unknown discs** – even when TMDB search fails, the identifier now emits a rip spec with `content_key` `unknown:<fingerprint>` and per-title `content_fingerprint` values so manual annotations can stick across retries.
- **Notification errors** – logged as warnings; they do not fail the stage but keep an eye on ntfy credentials.

## Troubleshooting Tips

- **Enhanced logging**: The identifier now provides comprehensive logging of the entire TMDB process:
  - Complete query details (title, year, runtime range)
  - All TMDB results with scores and metadata
  - Confidence scoring analysis and threshold decisions
  - Clear explanations of why matches are accepted or rejected
- **spindle identify command**: Use `spindle identify` or `spindle identify --verbose` to troubleshoot disc identification without affecting the queue. This command shows all enhanced logging and expected Plex filename format.
- **spindle queue show**: Run `spindle queue show <id>` to inspect a queued disc’s content key and per-title fingerprints without re-running identification.
- Inspect cached responses by tailing the daemon logs; identifier logs the raw query and any TMDB errors at warn level.
- For ambiguous discs, update `progress_message` via manual queue edits only after taking a snapshot; otherwise prefer retrying with better metadata.
- If TMDB throttles requests, widen the rate limit window in code or improve the caching strategy before considering retries.

## Future Notes

- Enhanced metadata overlay (BDMT parsing, additional XML metadata) could further improve identification accuracy.
- Consider persisting TMDB caches on disk if repeated scans of box sets become common.
- Volume identifier parsing patterns can be extended to handle more studio-specific naming schemes.
- Studio filtering: TMDB studio filtering could be implemented by adding company lookup API calls to convert studio names to TMDB company IDs.
- Additional TMDB search parameters: Consider adding language filtering, genre filtering, or other TMDB API parameters for even more precise matching.
