# System Design: Queue Database

SQLite queue database schema, item model, status state machine, and store operations.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. SQLite Setup

- **Location**: `{log_dir}/queue.db`
- **Driver**: modernc.org/sqlite (pure-Go) with WAL mode
- **Transient**: No migration system. On schema changes, bump `schemaVersion`
  constant and users clear the database.
- **Current schema version**: 4

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

## 2. Schema

```sql
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS queue_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_path TEXT,
    disc_title TEXT,
    status TEXT NOT NULL,
    failed_at_status TEXT,
    media_info_json TEXT,
    ripped_file TEXT,
    encoded_file TEXT,
    final_file TEXT,
    error_message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    progress_stage TEXT,
    progress_percent REAL DEFAULT 0.0,
    progress_message TEXT,
    rip_spec_data TEXT,
    disc_fingerprint TEXT,
    metadata_json TEXT,
    last_heartbeat TIMESTAMP,
    needs_review INTEGER NOT NULL DEFAULT 0,
    review_reason TEXT,
    item_log_path TEXT,
    encoding_details_json TEXT,
    active_episode_key TEXT,
    progress_bytes_copied INTEGER DEFAULT 0,
    progress_total_bytes INTEGER DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_items(status);
CREATE INDEX IF NOT EXISTS idx_queue_fingerprint ON queue_items(disc_fingerprint);
```

## 3. Item Model (26 columns)

| Column                | Type      | Purpose                                           |
|-----------------------|-----------|---------------------------------------------------|
| `id`                  | INTEGER   | Auto-increment primary key                        |
| `source_path`         | TEXT      | Original source file path (for file-based input)  |
| `disc_title`          | TEXT      | Disc label / identified title                     |
| `status`              | TEXT      | Current pipeline status                           |
| `failed_at_status`    | TEXT      | Status when failure occurred (for retry routing)  |
| `media_info_json`     | TEXT      | Raw media info JSON blob                          |
| `ripped_file`         | TEXT      | Path to ripped MKV file                           |
| `encoded_file`        | TEXT      | Path to encoded file                              |
| `final_file`          | TEXT      | Path to final organized file                      |
| `error_message`       | TEXT      | Last error message                                |
| `created_at`          | TIMESTAMP | Item creation time                                |
| `updated_at`          | TIMESTAMP | Last update time                                  |
| `progress_stage`      | TEXT      | Current stage display name                        |
| `progress_percent`    | REAL      | Progress percentage (0-100)                       |
| `progress_message`    | TEXT      | Human-readable progress message                   |
| `rip_spec_data`       | TEXT      | JSON-encoded RipSpec envelope                     |
| `disc_fingerprint`    | TEXT      | SHA-256 hash of disc filesystem metadata          |
| `metadata_json`       | TEXT      | JSON-encoded TMDB metadata                        |
| `last_heartbeat`      | TIMESTAMP | Last heartbeat from processing goroutine          |
| `needs_review`        | INTEGER   | 1 if item requires manual review                  |
| `review_reason`       | TEXT      | Why review is needed (semicolon-separated)        |
| `item_log_path`       | TEXT      | Path to per-item log file                         |
| `encoding_details_json`| TEXT     | Drapto encoding snapshot JSON                     |
| `active_episode_key`  | TEXT      | Currently processing episode (e.g., s01e03)       |
| `progress_bytes_copied`| INTEGER  | Bytes copied during organizing                    |
| `progress_total_bytes` | INTEGER  | Total bytes to copy during organizing             |
| `drapto_preset_profile`| TEXT     | Drapto encoding preset profile (health check only)|

Note: `drapto_preset_profile` is listed in the health check expected columns
(`CheckHealth()`) but is not currently in the schema DDL. A reimplementation
should include it in the CREATE TABLE to pass health checks.

## 4. Status State Machine (16 statuses)

```
                              +----------+
                              | pending  |
                              +----+-----+
                                   |
                              +----v---------+
                              | identifying  |
                              +----+---------+
                                   |
                              +----v--------+
                              | identified  |
                              +----+--------+
                                   |
                              +----v-----+
                              | ripping  |
                              +----+-----+
                                   |
                              +----v----+
                              | ripped  |----------------------------------+
                              +----+----+                                  |
                                   |                                       |
                         +---------v--------------+                        |
                         | episode_identifying    | (TV only)              |
                         +---------+--------------+                        |
                                   |                                       |
                         +---------v--------------+                        |
                         | episode_identified     |                        |
                         +---------+--------------+                        |
                                   |                                       |
                              +----v------+                                |
                              | encoding  |<-------------------------------+
                              +----+------+  (movie: ripped -> encoding)
                                   |
                              +----v-----+
                              | encoded  |
                              +----+-----+
                                   |
                         +---------v-----------+
                         | audio_analyzing     |
                         +---------+-----------+
                                   |
                         +---------v----------+
                         | audio_analyzed     |
                         +---------+----------+
                                   |
                              +----v--------+
                              | subtitling  |
                              +----+--------+
                                   |
                              +----v-------+
                              | subtitled  |
                              +----+-------+
                                   |
                              +----v--------+
                              | organizing  |
                              +----+--------+
                                   |
                              +----v-------+
                              | completed  |
                              +------------+

  Any status --> failed (on error)
```

**Processing lane assignment:**
- **Foreground**: pending, identifying, identified, ripping
- **Background**: ripped, audio_analyzing, audio_analyzed, episode_identifying,
  episode_identified, encoding, encoded, subtitling, subtitled, organizing,
  completed
- **Failed**: foreground if no item log path, background otherwise

### Processing Lanes

`ProcessingLane` type determines which workflow lane processes an item:

| Lane | Constant | Statuses |
|------|----------|----------|
| Foreground | `LaneForeground` | pending, identifying, identified, ripping |
| Background | `LaneBackground` | ripped through completed (all intermediate statuses) |

`LaneForItem(item)` assigns lane based on status and metadata:
- Foreground: statuses up through ripping, plus failed items without an item log path
- Background: all later statuses (ripped onward including completed), plus failed items with an item log path
- Nil items default to foreground

## 5. Rollback Transitions

When an item is found stale (heartbeat timeout), it rolls back to the previous
ready state:

| From Status          | Rolls Back To          |
|----------------------|------------------------|
| identifying          | pending                |
| ripping              | identified             |
| episode_identifying  | ripped                 |
| encoding             | episode_identified     |
| audio_analyzing      | encoded                |
| subtitling           | audio_analyzed         |
| organizing           | audio_analyzed         |

## 6. Heartbeat Monitoring

- Each processing item gets a background goroutine that updates `last_heartbeat`
  every `heartbeat_interval` seconds.
- The reclaimer checks for items with `last_heartbeat` older than
  `heartbeat_timeout` seconds and transitions them back per the rollback table.
- Progress is sampled and logged at DEBUG level (suppressed when unchanged).

## 7. Shutdown Behavior

On daemon shutdown, all items in processing statuses (identifying, ripping,
encoding, etc.) are marked as `failed` with error message "Daemon stopped" and
`failed_at_status` capturing where they were. This ensures explicit `retry` is
needed on restart rather than silent auto-resume.

## 8. Review vs Failed Semantics

- **Failed**: Operation error. Item can be retried with `queue retry`. The
  `failed_at_status` field records which stage failed for proper retry routing.
- **Review**: Content ambiguity (unidentified media, low-confidence episode
  matching, missing episodes). Item is routed to review directory. The
  `needs_review` flag and `review_reason` field describe why.
- An item can have `needs_review=true` and still be in a processing status
  (partial episode resolution continues processing resolved episodes while
  flagging the item for review).

## 9. Store Operations

**Item lifecycle operations:**

| Operation | Purpose |
|-----------|---------|
| `NewDisc(title, fingerprint)` | Insert new pending item (fingerprint required) |
| `GetByID(id)` | Fetch single item by primary key |
| `FindByFingerprint(fp)` | Find first item matching a disc fingerprint |
| `Update(item)` | Full item update (22 mutable columns); applies stop-review override |
| `UpdateProgress(item)` | Progress-only update (stage, percent, message, bytes, encoding, episode key) |
| `UpdateHeartbeat(id)` | Touch heartbeat timestamp for liveness |
| `Remove(id)` | Delete single item |
| `Clear()` | Delete all items |
| `ClearCompleted()` | Delete only completed items |
| `ClearFailed()` | Delete failed items (excludes user-stopped items) |

**Query operations:**

| Operation | Purpose |
|-----------|---------|
| `List(statuses...)` | Items filtered by status set (or all), ordered by creation time |
| `ItemsByStatus(status)` | Items matching a single status, ordered by creation time |
| `NextForStatuses(statuses...)` | Oldest item matching any status (FIFO queue fetch) |
| `ActiveFingerprints()` | Set of all non-empty fingerprints in queue (for orphan cleanup) |
| `HasDiscDependentItem()` | True if any item is in identifying or ripping status |
| `Stats()` | Count of items grouped by status |
| `CheckHealth()` | Full diagnostic: existence, table check, column presence, integrity, total count |

**Stop-review override**: When updating an item, `applyStopReviewOverride()`
preserves user-initiated stop state. If the stored item has `status=failed` and
`review_reason="Stop requested by user"`, the update is forced to maintain that
state regardless of what the caller sets.

**Transition operations:**

| Operation | Purpose |
|-----------|---------|
| `ResetStuckProcessing()` | Reset all processing items to stage start (no age check) |
| `ReclaimStaleProcessing(cutoff, statuses...)` | Time-based reclamation with optional status filter |
| `RetryFailed(ids...)` | Route failed items to retry point using `failed_at_status`; falls back to pending |
| `FailActiveOnShutdown()` | Mark all non-terminal items as failed with "Daemon stopped" |
| `StopItems(ids...)` | User-initiated stop: mark as failed with review flag |

## 10. Metadata Helpers

Queue metadata (`Metadata` struct) provides filesystem path computation for the
organizer and CLI display:

- `MetadataFromJSON(data, fallback)`: Deserialize from stored JSON with
  fallback title inference.
- `NewBasicMetadata(title, isMovie)`: Construct minimal metadata.
- `NewTVMetadata(show, season, episodes, display)`: Build TV-specific metadata
  with episode filename generation.
- `IsMovie()`: Determine type from `media_type` field (accepts "movie", "film",
  "tv", "tv_show", "television", "series"), falls back to `movie` bool flag.
- `GetLibraryPath(root, moviesDir, tvDir)`: Compute target library folder.
  Movies use `{root}/{moviesDir}/{baseFilename}`. TV uses
  `{root}/{tvDir}/{show}/Season {NN}`.
- `GetFilename()`: Final output filename. Movies: base + edition suffix. TV:
  `{Show} - S{NN}E{NN}` format (range notation for multi-episode).
- `GetBaseFilename()`: Filename without edition suffix (for shared movie folders).

Filenames are sanitized: colons/hyphens/slashes become spaces, special
characters removed, whitespace collapsed.

## 11. Staging Path Computation

`Item.StagingRoot(base)` computes the per-item working directory:

1. If `DiscFingerprint` is non-empty: use uppercase fingerprint as directory name.
2. Otherwise: use `queue-{ID}` as directory name.
3. Sanitize: replace filesystem-unsafe characters, convert spaces to hyphens,
   trim leading/trailing hyphens and underscores.

Result: `{staging_dir}/{fingerprint_or_queue_id}/`

## 12. RipSpec Persistence

`PersistRipSpec(ctx, store, item, env)` encodes a `RipSpecEncoder` (satisfied
by `ripspec.Envelope`) and writes the result to the item's `rip_spec_data`
column via `store.Update()`. Used by stages that modify the envelope
(identification, episode ID, ripping, audio analysis, subtitles).

## 13. Filename Sanitization

Three sanitization functions ensure filesystem-safe filenames:

**`sanitizeFilename()`** (metadata helper):
- Replaces `:`, `-`, `/`, `\`, newlines, tabs with spaces
- Removes `?`, `"`, `<`, `>`, `|`, `*`
- Collapses consecutive whitespace to single space via `strings.Fields`
- Falls back to `"manual-import"` if result is empty

**`sanitizeSegment()`** (path helper):
- Calls `textutil.SanitizeFileName()` for filesystem-unsafe character replacement
  (slashes/backslashes/colons/asterisks become dashes; `?`, `"`, `<`, `>`, `|` removed)
- Replaces spaces with hyphens
- Trims leading/trailing hyphens and underscores
- Falls back to `"queue"` if result is empty

**`buildEpisodeFilename()`** (metadata helper):
- TV with no episodes: `"{Show} - Season {NN}"` format
- TV single episode: `"{Show} - S{NN}E{NN}"` format
- TV multi-episode range: `"{Show} - S{NN}E{NN}-E{NN}"` format
- Fallback title: `"Manual Import"` when show title is missing (applied by callers)
