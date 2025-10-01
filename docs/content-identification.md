# Content Identification

Reference notes for how the Go daemon classifies discs and prepares metadata. Keep this in sync with `internal/identification`, `internal/disc`, and `internal/identification/tmdb`.

## Pipeline Summary

1. **MakeMKV scan** – `internal/disc.Scanner` calls `makemkvcon info` to capture the title list; Spindle now computes its own fingerprint from disc metadata before the scan runs.
2. **Identification stage** – `internal/identification.Identifier` enriches queue items, checks for duplicates, and performs TMDB lookups.
3. **TMDB client** – `internal/identification/tmdb` wraps the REST API with simple rate limiting and caching to avoid duplicate requests during a run.
4. The stage writes `MetadataJSON` and `RipSpecData` back to the queue so downstream stages can pick title selections and user-facing details.

## Disc Scan Details

- MakeMKV output is parsed into `disc.ScanResult`, preserving the fingerprint and a normalized list of titles (id, name, duration in seconds).
- Fingerprints come from hashing the disc's unencrypted metadata (BDMV structures for Blu-ray, IFO files for DVD). If hashing fails, treat it as a mount/drive issue.
- Raw JSON is stored alongside parsed data to help with later diagnostics (`rip_spec_data` contains the structured payload).

## Duplicate Detection & Review Flow

- The identifier checks `queue.Store.FindByFingerprint`. If another item already claimed the fingerprint, the current disc is moved straight to `REVIEW` with the message “Duplicate disc fingerprint”.
- When TMDB lookup fails or produces no confident match, the identifier now flags the queue item for manual review but still allows ripping and Drapto encoding to complete. The organizer stage then relocates the encoded file into `<review_dir>` and marks the queue item `COMPLETED` once the artifact is safely staged, so automation continues uninterrupted.
- Review filenames are generated from the review reason whenever possible (for example `no-confident-tmdb-match-1.mkv`); if you prefer a different scheme, adjust the prefix logic in `internal/organizer/organizer.go`.
- Notifications fire through `notifications.Service.Publish` using events such as `EventDiscDetected`, `EventIdentificationCompleted`, and `EventUnidentifiedMedia` so operators know when a manual follow-up is required and when the encoded asset is ready in the review directory.

## TMDB Matching

- All lookups route through `tmdb.Client.SearchMovie`. The identifier keeps an in-memory cache keyed by the normalized query to avoid hammering the API.
- A simple rate limiter (250 ms minimum gap) protects against short bursts when multiple discs enter the same stage.
- Candidate scoring favors:
  - Title substring match against the cleaned query.
  - Higher `vote_average` (scaled 0–1) and `vote_count`.
- The first candidate above `tmdb_confidence_threshold` wins. The chosen title is written into `MetadataJSON` and echoed in the progress message (“Identified as: …”).

## Output Shape

The identifier persists two JSON blobs on the queue item:

- `MetadataJSON` – compact TMDB fields (`id`, canonical title/name, overview, media_type, vote stats).
- `RipSpecData` – map containing `fingerprint`, `titles` (the MakeMKV list), and `metadata` (same structure as `MetadataJSON`).

Downstream stages rely on these fields for logging, rip configuration, and Plex naming.

## Configuration Knobs

Relevant settings live in `internal/config`:

- `tmdb_api_key`, `tmdb_base_url`, `tmdb_language` – TMDB connectivity.
- `tmdb_confidence_threshold` – minimum acceptable normalized vote score.
- `optical_drive` – MakeMKV device path (defaults to `/dev/sr0`).
- Logging hints: `log_dir` determines where queue snapshots and structured logs land for inspection.

Update this list when the identifier begins consuming additional config (runtime hints, enhanced metadata, etc.).

## Failure Modes & Mitigation

- **MakeMKV scan failure** – surfaces as `FAILED` with `MakeMKV disc scan failed`. Verify the binary location and drive permissions.
- **Missing TMDB matches** – item is flagged for review, finishes rip/encode, and then is marked complete with the encoded file parked under `review_dir`. Adjust the metadata manually and rerun `spindle queue retry <id>` (if you want Spindle to reorganize the file after you update metadata) or simply leave the queue entry as-is once you’ve handled it.
- **Notification errors** – logged as warnings; they do not fail the stage but keep an eye on ntfy credentials.

## Troubleshooting Tips

- Inspect cached responses by tailing the daemon logs; identifier logs the raw query and any TMDB errors at warn level.
- For ambiguous discs, update `progress_message` via manual queue edits only after taking a snapshot; otherwise prefer retrying with better metadata.
- If TMDB throttles requests, widen the rate limit window in code or improve the caching strategy before considering retries.

## Future Notes

- Enhanced metadata overlay (bd_info, BDMT parsing) is a planned addition—document it here once implemented in `internal/disc`.
- Consider persisting TMDB caches on disk if repeated scans of box sets become common.
