# Content Identification

Reference notes for how the Go daemon classifies discs and prepares metadata so Plex receives the right title and release year for every rip. Keep this in sync with `internal/identification`, `internal/disc`, and `internal/identification/tmdb`.

## Pipeline Summary

1. **MakeMKV scan** – `internal/disc.Scanner` calls `makemkvcon info` to capture the title list; Spindle now computes its own fingerprint from disc metadata before the scan runs.
2. **bd_info enrichment** – The scanner always runs `bd_info` when the binary is available, harvesting Blu-ray disc IDs, studio hints, and a cleaned disc name. It still only overwrites the MakeMKV title when the original metadata is empty or generic.
3. **KEYDB lookup** – When `bd_info` captures an AACS Disc ID, the identifier consults the KEYDB catalog (automatically refreshed weekly) to fetch curated titles/aliases before doing any string heuristics. The alias text is now parsed for show/season hints so TV discs can be detected even when MakeMKV emits generic titles.
4. **Identification stage** – `internal/identification.Identifier` enriches queue items, checks for duplicates, applies overrides from `identification_overrides_path`, and performs TMDB lookups using the enhanced title OR KEYDB/override hints. Episode-heavy discs automatically switch to the TV lookup flow before falling back to movies or TMDB’s multi-type search endpoint.
5. **TMDB client** – `internal/identification/tmdb` wraps the REST API with simple rate limiting and caching to avoid duplicate requests during a run.
6. The stage writes `MetadataJSON` and `RipSpecData` back to the queue. The rip spec now carries per-episode descriptors plus `assets` sections that record ripped/encoded/final file paths so the downstream stages can fan out work without guessing.
7. **Post-rip content ID** – After ripping maps MakeMKV titles to physical files, the ripper uses WhisperX + OpenSubtitles to re-verify episode order. Each ripped episode gets a WhisperX transcript, the matcher downloads candidate subtitles for the inferred season/disc range, and cosine similarity assigns the true `SxxEyy`. The rip spec and queue metadata are updated before encoding begins.

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
- KEYDB aliases override both MakeMKV and bd_info names when a Disc ID match exists (e.g., translating `VOLUME_ID [Michael Clayton]` to “Michael Clayton”). The alias text is also parsed for show names, season numbers, and disc ordinals so the TV heuristics stay consistent.

### Title Source Priority

1. **KEYDB match** – If the Disc ID is known, the curated KEYDB title wins immediately (and feeds the TV heuristics).
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

- All lookups route through TMDB with enhanced search parameters. Movie discs still call `/search/movie`, but episodic candidates are tried against `/search/tv` first and `/search/multi` last. The identifier keeps an in-memory cache keyed by the normalized query plus the search mode to avoid re-querying TMDB when several discs share aliases.
- A simple rate limiter (250 ms minimum gap) protects against short bursts when multiple discs enter the same stage.
- **Enhanced search parameters** from bd_info and MakeMKV data:
  - Year filtering via `primary_release_year` (extracted from bd_info)
  - Runtime range filtering (±10 minutes from main title duration)
  - Studio information extraction (available for future filtering)
- **TV heuristics**: multiple 20–30 minute titles, KEYDB/override aliases containing “Season XX”, or BDInfo volume identifiers matching `Sxx` all tip the search order toward `/search/tv`. Search queries are normalized to strip `_DISC1`, `Blu-ray`, etc. before hitting TMDB so obvious shows resolve reliably.
- **Confidence scoring logic**:
  - Exact title matches: Accept if vote_average ≥ 2.0 (lenient for perfect matches)
  - Partial matches: Require vote_average ≥ 3.0 and minimum calculated score
  - Score formula: title_match + (vote_average/10) + (vote_count/1000)
- The first candidate above the confidence threshold wins. The chosen title is written into `MetadataJSON`, the DiscTitle is updated to "Title (Year)" format, and echoed in the progress message ("Identified as: …").
- **Enhanced logging**: All TMDB queries, responses, and confidence scoring are logged for transparency.

## WhisperX + OpenSubtitles Content ID

- **Trigger point**: Right after MakeMKV finishes and `assignEpisodeAssets` maps playlist IDs to ripped files. The matcher only runs when OpenSubtitles credentials are configured—otherwise the ripper logs that the post-rip verification was skipped.
- **Transcript generation**: Each ripped episode goes through the existing subtitle service with `forceAI=true` so WhisperX produces JSON/SRT output specific to that playlist. The raw dialogue is converted into cosine-similarity fingerprints (lowercase tokens, de-duplicated stop words).
- **Reference download**: Using the TMDB show ID, inferred season, and (when available) disc number, the matcher fetches OpenSubtitles listings for the candidate episode numbers. Cleaned SRT payloads are normalized to the same fingerprint format.
- **Matching**: A `ripped × reference` similarity matrix is sorted by score and greedily assigned with a configurable floor (currently 0.58). Successful matches rewrite the rip spec’s `titles[*].season/episode` fields, update each `episodes[*]` entry (title, air date, output basename), and refresh `metadata.episode_numbers`.
- **Diagnostics**: The rip spec’s `attributes.content_id_matches` array captures the episode key, playlist ID, TMDB episode number, and similarity score. This is surfaced by `spindle rip-spec` and other tooling for auditing.
- **Failure tolerance**: Network hiccups or missing dependencies never fail the ripping stage. The matcher returns the error for logging, but the pipeline continues with the heuristic episode ordering so operators can still intervene manually.

## Output Shape

The identifier persists two JSON blobs on the queue item:

- `MetadataJSON` – enhanced TMDB fields (`id`, canonical title/name, `release_date` or `first_air_date`, overview, media_type, vote stats). TV matches also record `show_title`, `season_number`, and the list of matched `episode_numbers` so Plex filenames can include `SxxEyy` ranges.
- `RipSpecData` – structured payload with `fingerprint`, `content_key`, `metadata`, `titles`, and `episodes`. Each episode entry references a specific playlist/fingerprint, carries TMDB metadata (season/episode/title/air date), and declares the target output basename that the ripping/encoding/organizer stages follow. The `assets` section records ripped, encoded, and final file paths for every episode so recoveries can resume mid-stage.

**Title Propagation**: After successful identification, `item.DiscTitle` is updated from the raw disc title to the proper TMDB value (movies keep the "Title (Year)" format, while TV discs become `Show Name Season XX (Year)`), ensuring all subsequent stages use the clean, properly formatted title.

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

## Overrides

Add curated matches by editing the JSON file referenced by `identification_overrides_path` (defaults to `~/.config/spindle/overrides/identification.json`). Each entry may specify disc fingerprints or KEYDB Disc IDs plus the TMDB title/season metadata to prefer. When a disc matches an override, the identifier seeds the TMDB search with the curated show/title and trusts the override’s season guess before consulting heuristics.

## Future Notes

- Enhanced metadata overlay (BDMT parsing, additional XML metadata) could further improve identification accuracy.
- Consider persisting TMDB caches on disk if repeated scans of box sets become common.
- Volume identifier parsing patterns can be extended to handle more studio-specific naming schemes.
- Studio filtering: TMDB studio filtering could be implemented by adding company lookup API calls to convert studio names to TMDB company IDs.
- Additional TMDB search parameters: Consider adding language filtering, genre filtering, or other TMDB API parameters for even more precise matching.
