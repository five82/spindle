# System Design: Queue Database

SQLite queue database schema, item model, status state machine, and store operations.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. SQLite Setup

- **Location**: `{state_dir}/queue.db`
- **Driver**: `modernc.org/sqlite` (pure-Go, no CGo). Avoids CGo build
  complexity with negligible performance difference at this scale.
- **Transient**: No migration system. On startup, `CREATE TABLE IF NOT EXISTS`
  ensures the table exists. If the schema changes, clear the database.

**Connection pragmas** (applied on every `Open()`):

| Pragma | Value | Purpose |
|--------|-------|---------|
| `journal_mode` | WAL | Write-Ahead Logging for concurrent readers |
| `foreign_keys` | ON | Enforce referential integrity |
| `busy_timeout` | 5000 | Wait up to 5 seconds on lock contention |

**Busy retry mechanism**: All write operations (`exec`) are wrapped in
`retryOnBusy()` with exponential backoff -- 5 attempts, 10ms initial delay,
200ms max delay, doubling per attempt. Detects busy via SQLite error code 5,
`SQLITE_BUSY`, or `"database is locked"` in error message. Context cancellation
aborts the retry loop.

**CLI read-only access**: When the CLI opens the database directly (daemon not
running), it applies `PRAGMA query_only = ON` to prevent accidental writes.
This ensures the fallback path is strictly read-only and cannot corrupt state
that the daemon expects to own.

## 2. Schema

```sql
CREATE TABLE IF NOT EXISTS queue_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_title TEXT,
    stage TEXT NOT NULL,
    in_progress INTEGER NOT NULL DEFAULT 0,
    failed_at_stage TEXT,
    error_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    rip_spec_data TEXT,
    disc_fingerprint TEXT,
    metadata_json TEXT,
    needs_review INTEGER NOT NULL DEFAULT 0,
    review_reason TEXT,
    progress_stage TEXT,
    progress_percent REAL DEFAULT 0.0,
    progress_message TEXT,
    active_episode_key TEXT,
    progress_bytes_copied INTEGER DEFAULT 0,
    progress_total_bytes INTEGER DEFAULT 0,
    encoding_details_json TEXT,
    ripped_file TEXT,
    encoded_file TEXT,
    final_file TEXT
);

CREATE INDEX IF NOT EXISTS idx_queue_stage ON queue_items(stage);
CREATE INDEX IF NOT EXISTS idx_queue_fingerprint ON queue_items(disc_fingerprint);
```

Progress fields are on the same table as core fields. SQLite WAL mode handles
the write frequency (every 2-5 seconds during encoding/ripping) without
contention issues at this scale. This eliminates the join complexity and lazy
row creation of a separate progress table.

## 3. Item Model (22 columns)

| Column                 | Type      | Purpose                                           |
|------------------------|-----------|---------------------------------------------------|
| `id`                   | INTEGER   | Auto-increment primary key                        |
| `disc_title`           | TEXT      | Disc label / identified title                     |
| `stage`                | TEXT      | Current pipeline stage                            |
| `in_progress`          | INTEGER   | 1 if item is actively being processed             |
| `failed_at_stage`      | TEXT      | Stage when failure occurred (for retry routing)   |
| `error_message`        | TEXT      | Last error message                                |
| `created_at`           | TIMESTAMP | Item creation time                                |
| `updated_at`           | TIMESTAMP | Last update time                                  |
| `rip_spec_data`        | TEXT      | JSON-encoded RipSpec envelope                     |
| `disc_fingerprint`     | TEXT      | SHA-256 hash of disc filesystem metadata          |
| `metadata_json`        | TEXT      | Denormalized TMDB metadata for display (see Section 14) |
| `needs_review`         | INTEGER   | 1 if item requires manual review                  |
| `review_reason`        | TEXT      | Why review is needed (JSON array, e.g. `["reason1", "reason2"]`). Only column storing a raw JSON array rather than a plain string or JSON object. |
| `progress_stage`       | TEXT      | Current stage display name                        |
| `progress_percent`     | REAL      | Progress percentage (0-100); for multi-target stages this is aggregate whole-stage progress, not just the current target |
| `progress_message`     | TEXT      | Human-readable progress message                   |
| `active_episode_key`   | TEXT      | Currently processing episode (e.g., s01e03)       |
| `progress_bytes_copied`| INTEGER   | Bytes copied during organizing                    |
| `progress_total_bytes` | INTEGER   | Total bytes to copy during organizing             |
| `encoding_details_json`| TEXT      | Drapto encoding snapshot JSON                     |
| `ripped_file`          | TEXT      | Path to ripped MKV (staging or cache)             |
| `encoded_file`         | TEXT      | Path to encoded AV1 file (staging)                |
| `final_file`           | TEXT      | Path to final file in library or review dir       |

## 4. Stage Model (9 stages + in_progress flag)

Items track their position in the pipeline with a `stage` field (TEXT) and an
`in_progress` flag (INTEGER, 0 or 1). The stage says *where* the item is
(the current or next step), and the flag says *whether work is actively
happening*. Each pipeline handler owns exactly one stage and picks up items
whose `stage` matches.

**Type-safe stages in Go:**

```go
type Stage string

const (
    StageIdentification       Stage = "identification"
    StageRipping              Stage = "ripping"
    StageEpisodeIdentification Stage = "episode_identification"
    StageEncoding             Stage = "encoding"
    StageAudioAnalysis        Stage = "audio_analysis"
    StageSubtitling           Stage = "subtitling"
    StageOrganizing           Stage = "organizing"
    StageCompleted            Stage = "completed"
    StageFailed               Stage = "failed"
)
```

Stage transitions are implicit: each stage advances to the next entry in the
pipeline's stage slice (see DESIGN_OVERVIEW.md Section 4.4). Any stage can
transition to `failed` on error. No explicit transition map is needed because
the pipeline is strictly linear -- the stage slice defines the only valid
progression.

**Stage values** (9):

```
identification -> ripping -> episode_identification (TV only)
   -> encoding -> audio_analysis -> subtitling -> organizing -> completed

Any stage --> failed (on error)
```

| Stage                  | Purpose                                           |
|------------------------|---------------------------------------------------|
| `identification`       | Queued; MakeMKV scan + TMDB lookup                |
| `ripping`              | MakeMKV rip to staging                            |
| `episode_identification`| Transcription + OpenSubtitles episode matching (TV) |
| `encoding`             | Drapto AV1 encode                                 |
| `audio_analysis`       | Audio refinement + commentary detection            |
| `subtitling`           | Parakeet transcription + forced subs                  |
| `organizing`           | Library copy + Jellyfin refresh                    |
| `completed`            | Terminal: successfully organized                   |
| `failed`               | Terminal: error occurred                           |

**in_progress semantics:**
- `0`: Item is ready for this stage (waiting to be picked up)
- `1`: Item is actively being processed by a goroutine

When a stage completes, the item advances to the next stage with
`in_progress = 0`. The next pipeline iteration picks it up and sets
`in_progress = 1` before executing.

**Movie skip**: The `episode_identification` handler returns immediately for
movies (see DESIGN_STAGES.md Section 3.1). The stage still runs; it just
completes as a no-op.

**Stage skip for optional stages**: When `episode_identification`,
`audio_analysis`, or `subtitling` handlers are absent, stage advancement
skips directly to the next configured stage.

### Stage Priority

The pipeline poll loop fetches items ordered by stage priority (earlier stages
first) to free the disc drive as quickly as possible:

| Priority | Stages | Disc Semaphore |
|----------|--------|----------------|
| 1 (highest) | pending, identification, ripping | Required |
| 2 | episode_identification through organizing | Not required |

Within the same priority, items are ordered by creation time (FIFO).

## 5. Startup Recovery (replaces heartbeat monitoring)

The daemon is a single binary -- if the process dies, all goroutines die with
it. There is no distributed coordination requiring heartbeats.

**On startup**, the daemon scans for stale in-progress items:
- Any item with `in_progress = 1` was interrupted by a crash or unclean
  shutdown.
- These items are reset to `in_progress = 0` (retaining their current stage)
  so they are picked up again by the pipeline poll loop.
- Logged at INFO with `decision_type: "startup_recovery"` for each reset item.

**On clean shutdown**, `ResetInProgressOnShutdown()` clears `in_progress` on
all active items. Items retain their stage so they resume from the correct
point on next startup.

**Context cancellation**: Each stage execution receives a `context.Context`
derived from the daemon's root context. When the daemon shuts down, contexts
are cancelled, and stage handlers observe cancellation via `ctx.Done()`.
Cleanup logic runs in the handler's deferred functions, not via heartbeat
rollback.

This eliminates the heartbeat goroutine, heartbeat interval/timeout config,
and stale item reclamation polling.

## 6. Shutdown Behavior

On daemon shutdown:
1. Root context is cancelled, which cancels all in-flight stage contexts.
2. In-flight stage goroutines finish (handlers observe cancellation).
3. `ResetInProgressOnShutdown()` clears `in_progress` on all active items
   with a **5-second timeout context**. Items retain their current stage.
4. On next startup, these items are picked up and re-executed from the
   beginning of their current stage.

## 7. NextReady Query

`NextReady(stageOrder []Stage)` is the primary queue fetch used by the
pipeline poll loop. It returns the oldest item where `in_progress = 0` and
`stage` is not a terminal stage (`completed`, `failed`), ordered by:

1. Stage priority (position in `stageOrder` slice -- disc-dependent stages first)
2. Creation time (FIFO within the same stage)

This is built on top of `NextForStatuses()` with the stage order derived
from the pipeline configuration.

## 8. Review vs Failed Semantics

- **Failed**: Operation error. Item can be retried with `queue retry`. The
  `failed_at_stage` field records which stage failed for proper retry routing.
- **Review**: Content ambiguity (unidentified media, low-confidence episode
  matching, missing episodes, or no reference matches after successful
  episode-identification reference acquisition). Item is routed to review
  directory. The `needs_review` flag and `review_reason` field describe why.
- An item can have `needs_review=true` and still be in a processing status
  (partial episode resolution continues processing resolved episodes while
  flagging the item for review).

## 9. Store Operations

**Item lifecycle operations:**

| Operation | Purpose |
|-----------|---------|
| `NewDisc(title, fingerprint)` | Insert new item at identification stage (disc fingerprint required) |
| `GetByID(id)` | Fetch single item by primary key |
| `FindByFingerprint(fp)` | Find first item matching a disc fingerprint |
| `Update(item)` | Full item update (mutable columns); applies stop-review override |
| `UpdateProgress(item)` | Update only progress columns (stage, percent, message, bytes, encoding, episode key). High-frequency path. Used for live stage progress. For multi-target stages, callers persist aggregate stage progress and may also force updates at target boundaries so API consumers can advance counts during the active stage. |
| `Remove(id)` | Delete single item |
| `Clear()` | Delete all items |
| `ClearCompleted()` | Delete only completed items |

**Query operations:**

| Operation | Purpose |
|-----------|---------|
| `List(statuses...)` | Items filtered by status set (or all), ordered by creation time |
| `ItemsByStatus(status)` | Items matching a single status, ordered by creation time |
| `NextForStatuses(statuses...)` | Oldest item matching any status (FIFO queue fetch) |
| `ActiveFingerprints()` | Set of all non-empty fingerprints in queue (for orphan cleanup) |
| `HasDiscDependentItem()` | True if any item is in identification or ripping stage with in_progress=1 |
| `InProgressItems()` | All items with in_progress=1, ordered by creation time (for notification context) |
| `Stats()` | Count of items grouped by status |
| `CheckHealth()` | Full diagnostic: existence, table check, column presence, integrity, total count |

**Stop-review override**: When updating an item, `applyStopReviewOverride()`
preserves user-initiated stop state. If the stored item has `stage=failed` and
`review_reason` contains `"Stop requested by user"`, the update is forced to maintain that
state regardless of what the caller sets.

**Transition operations:**

| Operation | Purpose |
|-----------|---------|
| `ResetInProgress()` | Clear `in_progress` on all items (startup recovery) |
| `ResetInProgressOnShutdown()` | Clear `in_progress` on all items (clean shutdown) |
| `RetryFailed(ids...)` | Route failed items to retry point using `failed_at_stage`; falls back to identification |
| `StopItems(ids...)` | User-initiated stop: mark as failed with review flag |

## 10. Metadata Helpers

Queue metadata (`Metadata` struct) provides filesystem path computation for the
organizer and CLI display:

- `MetadataFromJSON(data, fallback)`: Deserialize from `metadata_json` column
  with fallback title inference. Used by display and path computation only
  (see Section 14 for ownership rules).
- `NewBasicMetadata(title, isMovie)`: Construct minimal metadata.
- `NewTVMetadata(show, season, episodes, display)`: Build TV-specific metadata
  with episode filename generation.
- `IsMovie()`: Determine type from `media_type` field (accepts "movie", "film",
  "tv", "tv_show", "television", "series"), falls back to `movie` bool flag.
- `GetLibraryPath(root, moviesDir, tvDir)`: Compute target library folder
  via `SafeJoin` (path traversal guard on TMDB-derived segments). Movies use
  `{root}/{moviesDir}/{baseFilename}`. TV uses
  `{root}/{tvDir}/{show}/Season {NN}`.
- `GetFilename()`: Final output filename. Movies: base filename. TV:
  `{Show} - S{NN}E{NN}` format (range notation for multi-episode).

Filenames are sanitized via `SanitizeDisplayName()`: colons/slashes become
spaces, special characters removed, whitespace collapsed.

## 11. Staging Path Computation

`Item.StagingRoot(base)` computes the per-item working directory:

1. If `DiscFingerprint` is non-empty: use uppercase fingerprint as directory name.
2. Otherwise: use `queue-{ID}` as directory name.
3. Sanitize via `SanitizePathSegment()`.
4. Join to base via `SafeJoin(base, segment)` (path traversal guard).

Result: `{staging_dir}/{fingerprint_or_queue_id}/`

## 12. RipSpec Persistence

`PersistRipSpec(ctx, store, item, env)` encodes a `RipSpecEncoder` (satisfied
by `ripspec.Envelope`) and writes the result to the item's `rip_spec_data`
column via `store.Update()`. Used by stages that modify the envelope
(identification, episode ID, ripping, audio analysis, subtitles).

## 13. Filename Sanitization

Two sanitization functions with distinct purposes:

**`SanitizeDisplayName()`** (human-readable titles for library filenames):
- Replaces `:`, `/`, `\`, newlines, tabs with spaces
- Removes `?`, `"`, `<`, `>`, `|`, `*`
- Collapses consecutive whitespace to single space via `strings.Fields`
- Falls back to `"manual-import"` if result is empty

**`SanitizePathSegment()`** (directory and file path components):
- Replaces `/`, `\`, `:`, `*` with dashes
- Removes `?`, `"`, `<`, `>`, `|`
- Replaces spaces with hyphens
- Trims leading/trailing hyphens and underscores
- Falls back to `"queue"` if result is empty

**`buildEpisodeFilename()`** (metadata helper):
- TV with no episodes: `"{Show} - Season {NN}"` format
- TV single episode: `"{Show} - S{NN}E{NN}"` format
- TV multi-episode range: `"{Show} - S{NN}E{NN}-E{NN}"` format
- Fallback title: `"Manual Import"` when show title is missing (applied by callers)

## 14. Metadata Ownership

TMDB metadata appears in two places: the `metadata_json` queue column and the
`metadata` section of the RipSpec envelope (`rip_spec_data`). These serve
different purposes and have different write rules.

**RipSpec envelope metadata** is authoritative. Stage handlers read metadata
from the envelope and may update it during processing (e.g., episode ID stage
writes episode titles). All stage logic uses envelope metadata exclusively.

**`metadata_json`** is a denormalized projection for display and path
computation. It is written once by the identification stage at the same time
the initial RipSpec envelope is assembled and persisted. After identification,
no stage writes to `metadata_json`.

| Concern | Read from | Written by |
|---------|-----------|------------|
| Stage logic (TMDB ID, media type, season) | `rip_spec_data.metadata` | Identification (initial), later stages as needed |
| CLI display (`queue list`, `queue show`) | `metadata_json` | Identification only |
| Library path computation (organizer) | `metadata_json` via `MetadataFromJSON` | Identification only |
| HTTP API (`GET /api/queue`) | `metadata_json` | Identification only |

**Why not derive everything from the envelope?** Queue list display needs
title, media type, and year for every item. Deserializing the full RipSpec
envelope for each row is unnecessary overhead. The `metadata_json` column
provides a lightweight, query-friendly projection that avoids touching the
envelope for display-only operations.

**Invariant**: `metadata_json` must never be updated after identification.
If metadata changes during later stages (e.g., episode titles resolved),
those changes live only in the envelope. Display code shows the identification-
time snapshot, which is correct for title, year, media type, and library paths.
