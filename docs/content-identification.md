# Content Identification

This document is for coding agents touching `internal/identification`, `internal/episodeid`,
`internal/disc`, `internal/ripspec`, or any code that depends on clean metadata. It calls
out the contractual behavior and invariants that are easy to miss when skimming the
implementation.

## Responsibilities

**Disc Identification Stage (`internal/identification`):**
- Normalize raw disc data (MakeMKV, `bd_info`, KEYDB) into a single canonical
  title plus fingerprint.
- Decide whether a disc is a movie, TV season disc, or "needs manual review".
- Populate queue items with `MetadataJSON` (TMDB payload) and `RipSpecData`
  (per-title playlists, initial episode guesses, future filenames).

**Episode Identification Stage (`internal/episodeid`):**
- Correlate ripped TV episode files to definitive episode numbers using WhisperX
  transcription and OpenSubtitles reference comparison.
- Update `RipSpecData` with confirmed episode mappings after matching.
- Skip automatically for movies or when OpenSubtitles is disabled.

## Data Flow

1. **Scan** (`internal/disc.Scanner`):
   - Runs `makemkvcon info` and, when available, `bd_info`.
   - Computes a deterministic fingerprint from disc metadata (hash of BDMV/IFO
     structures). Every downstream lookup hinges on this value.
2. **Enrich**:
   - Merge MakeMKV titles with `bd_info` and KEYDB aliases; strip generic names.
3. **Match** (`internal/identification.Identifier`):
   - Choose TV vs movie heuristics (runtime clusters, KEYDB hints, `_Season_`
     strings, etc.).
   - Query TMDB (`internal/identification/tmdb`) with rate limiting + caching.
   - Score candidates; accept the first one over the confidence thresholds.
4. **Persist**:
   - Update `queue.Item.DiscTitle` to "Title (Year)" or
     "Show Name Season XX (Year)".
   - Store `MetadataJSON` and `RipSpecData`.
5. **Rip** (`internal/ripping`):
   - MakeMKV extracts disc content to MKV files in staging directory.
   - Assigns episode assets to rip spec based on initial heuristic ordering.
6. **Episode verification** (`internal/episodeid`):
   - After ripping completes, WhisperX + OpenSubtitles compares each ripped
     playlist to reference subtitles to confirm the episode map.
   - Fields inside the rip spec are rewritten when better matches are found.
   - Skipped entirely for movies or when OpenSubtitles is disabled.

## Title Source Priority

1. KEYDB alias (Disc ID match)
2. MakeMKV main title (when not generic)
3. `bd_info` volume/label
4. User-specified queue title
5. `"Unknown Disc"` fallback

Never insert user-facing status messages until this priority chain settles, or
Spindle surfaces noisy/incorrect names.

## Outputs & Contracts

- `MetadataJSON` must include at minimum: TMDB ID, canonical name/title,
  `media_type`, release/air date, and for TV discs the `season_number` plus
  ordered `episode_numbers`. Downstream logging and organizer filenames rely on
  these values being present.
- `RipSpecData` tracks:
  - `titles[]`: each MakeMKV playlist with duration, playlist ID, and the
    inferred `season/episode` tuple (may be empty until post-rip verification).
  - `episodes[]`: flattened list used by encoding/organizer; includes TMDB data,
    output basenames, and an `assets` struct that later fills in ripped/encoded/
    final file paths. Never delete historical asset records; the restart logic
    counts on them.
- Queue status transitions:
  - Success path: `PENDING → IDENTIFYING → IDENTIFIED`.
  - Missing match: stay `IDENTIFIED` but set `NeedsReview = true`. Subsequent
    stages still run; organizer diverts files to `review_dir`.
  - Fingerprint collision: send item to `FAILED` with `NeedsReview = true` and message
    "Duplicate disc fingerprint".

## Episode Mapping & Verification

- The episode identification stage (`internal/episodeid`) only runs when OpenSubtitles
  credentials exist and the item is a TV show. Movies skip this stage automatically.
- Matching uses WhisperX transcription plus OpenSubtitles reference comparison with a
  similarity floor (~0.58). When no match clears the floor, the best-effort heuristic
  ordering remains but the queue item may be flagged for review.
- The stage does not drop to `FAILED` on matching failures—encode + organize should keep
  running with the heuristic episode ordering.
- All episode rewrites must be reflected in both `RipSpecData` and `MetadataJSON` so
  later retries stay consistent.

## Configuration Inputs

Key settings the identifier consumes (see `docs/configuration.md` for details):

- `tmdb_api_key`, `tmdb_language`, `tmdb_confidence_threshold`
- `keydb_path`, `keydb_download_url`, `keydb_download_timeout`
- `bd_info_enabled`
- `optical_drive`

When introducing new knobs, update this list and the configuration guide in the
same change.

## Failure Modes & Expectations

- **No TMDB match**: item stays `IDENTIFIED`, `NeedsReview = true`. Organizer
  relocates output to `review_dir`. Never leave the queue in `PENDING`.
- **Duplicate fingerprint**: set status `FAILED` with `NeedsReview = true`, preserve the error message, do
  not start MakeMKV.
- **MakeMKV/bd_info errors**: bubble them up as `FAILED` with the command stderr
  attached; retries should resume from `PENDING`.
- **KEYDB fetch failure**: warn and continue; identification must not fail solely
  because the key database could not refresh.

## Debugging Checklist

- `spindle identify /dev/sr0 --verbose` exercises the entire stage without
  touching the queue; use it when changing heuristics.
- `spindle queue list --status review` confirms discs flagged for manual work.
- Inspect `queue_items.metadata_json` and `queue_items.rip_spec_data` to confirm
  new fields before wiring downstream logic.
- Enable debug logging (`SPD_LOG_LEVEL=debug spindle start`) to see TMDB queries,
  scoring, and title-source decisions.

## When Modifying This Doc

- Update it whenever the identifier gains a new dependency, emits new rip spec
  fields, or changes how it signals review/failure states.
- If details become redundant with package `doc.go` files, summarize the
  invariant here and link to the package for deep dives rather than duplicating
  code comments.
