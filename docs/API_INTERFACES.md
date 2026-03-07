# API Reference: Interfaces

CLI commands, IPC protocol, and HTTP API for the Spindle system.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

> **Rewrite improvement note**: Unify IPC + HTTP into a single HTTP API served
> over a Unix socket (with optional TCP bind). This eliminates the parallel
> JSON-RPC layer and reduces the interface surface area. The CLI would use the
> same HTTP endpoints as external consumers.

---

## 1. CLI Interface

### 1.1 Binary

Single binary: `spindle`

### 1.2 Global Flags

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--socket` | | string | `$XDG_RUNTIME_DIR/spindle.sock` | Path to the daemon Unix socket |
| `--config` | `-c` | string | `$XDG_CONFIG_HOME/spindle/config.toml` | Configuration file path |
| `--log-level` | | string | (from config) | Log level: debug, info, warn, error |
| `--verbose` | `-v` | bool | false | Shorthand for `--log-level=debug` |
| `--json` | | bool | false | Output in JSON format |

### 1.3 Daemon Commands

#### `spindle start`

Start the spindle daemon. If no daemon process is running, launches one.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--diagnostic` | bool | false | Enable diagnostic mode with separate DEBUG logs |

Behavior:
- Resolves own executable path for daemon launch
- Calls `daemonctl.EnsureStarted()` with 10s timeout
- Reports: "Daemon started", "Daemon already running", or start message

#### `spindle stop`

Stop the spindle daemon (completely terminates the process).

Behavior:
- Calls `daemonctl.StopAndTerminate()` with 5s timeout
- If daemon not running, prints "Daemon is not running" (no error)
- Reports stop acknowledgment and optional forced kill with PID

#### `spindle restart`

Restart the spindle daemon (stop then start).

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--diagnostic` | bool | false | Enable diagnostic mode with separate DEBUG logs |

Behavior:
- Stop timeout: 5s, start timeout: 10s
- Reports stop details then start result

#### `spindle status`

Show system and queue status.

Output sections:
1. **System Status**: Daemon running state, PID, disc pause, netlink monitoring
2. **Dependencies**: Per-dependency availability (makemkvcon, ffmpeg, etc.)
3. **Library Paths**: Movie and TV library path accessibility
4. **Queue Status**: Table of status counts

Works without daemon by falling back to direct DB access for queue stats.

### 1.4 Queue Commands

Parent: `spindle queue`

#### `spindle queue status`

Show queue status summary as a table of status counts.

#### `spindle queue list`

List queue items.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--status` | `-s` | string[] | (all) | Filter by queue status (repeatable) |

Table columns: ID, Title, Status, Created, Fingerprint.

#### `spindle queue show <id>`

Show detailed information for a single queue item.

Arguments: `<id>` -- queue item ID (required, exactly 1).

Output includes: ID, title, status, timestamps, source path, disc fingerprint,
progress, review status, error, file paths (ripped/encoded/final), metadata,
episode details with per-episode progress and subtitle info, and rip spec
fingerprints.

#### `spindle queue clear [id...]`

Remove queue items.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--all` | bool | false | Remove all items |
| `--completed` | bool | false | Remove only completed items |
| `--failed` | bool | false | Remove only failed items |

Rules:
- Requires either item IDs or exactly one flag
- Cannot combine IDs with flags
- Cannot combine multiple flags

#### `spindle queue reset-stuck`

Return in-flight items (stuck in processing states) to pending.

#### `spindle queue retry [itemID...]`

Retry failed queue items.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--episode` | `-e` | string | | Retry only a specific episode (e.g., `s01e05`) |

Behavior:
- No IDs, no `--episode`: retry all failed items
- IDs without `--episode`: retry specified items from the beginning
- ID with `--episode`: clear only the specified episode's failed status
  (requires exactly one ID)

#### `spindle queue stop <id...>`

Stop processing for specific queue items.

Arguments: `<id...>` -- one or more queue item IDs (minimum 1 required).

#### `spindle queue health`

Check queue database health (schema, integrity, columns).

Requires daemon. Output: database path, existence, readability, schema version,
table presence, columns, missing columns, integrity check, total items, errors.

### 1.5 Show Command

#### `spindle show`

Display daemon logs.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--follow` | `-f` | bool | false | Follow log output |
| `--lines` | `-n` | int | 10 | Number of lines to show (0 for all) |
| `--component` | | string | | Filter by component label |
| `--lane` | | string | | Filter by workflow lane (foreground/background) |
| `--request` | | string | | Filter by request/correlation ID |
| `--item` | `-i` | int64 | 0 | Filter by queue item ID |
| `--level` | | string | | Minimum log level (debug, info, warn, error) |
| `--alert` | | string | | Filter by alert flag |
| `--decision-type` | | string | | Filter by decision type |
| `--search` | | string | | Search logs by substring |

Transport priority: HTTP API (preferred) -> IPC fallback.
Filters require API access; IPC only supports raw line tailing.

### 1.6 Workflow Commands

#### `spindle identify [device]`

Identify a disc and show TMDB matching details.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--device` | `-d` | string | (configured optical_drive) | Optical device path |

Arguments: Optional `[device]` positional argument (overrides `--device`).

Runs identification stage without affecting the queue. Shows disc label, TMDB
results, metadata, library filename, review status, and rip spec fingerprints.

#### `spindle gensubtitle <encoded-file>`

Create subtitles for an encoded media file using WhisperX.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--output` | `-o` | string | (alongside source) | Output directory |
| `--work-dir` | | string | (temp under staging_dir) | Working directory |
| `--fetch-forced` | | bool | false | Also fetch forced subs from OpenSubtitles |
| `--external` | | bool | false | Create external SRT sidecar instead of muxing |

Arguments: `<encoded-file>` -- path to the encoded media file (required).

#### `spindle test-notify`

Send a test notification via the configured ntfy topic.

Requires daemon connection.

### 1.7 Disc Commands

Parent: `spindle disc`

#### `spindle disc pause`

Pause detection of new disc insertions. Requires daemon.

#### `spindle disc resume`

Resume detection of new disc insertions. Requires daemon.

#### `spindle disc detect`

Trigger disc detection using the configured optical drive. Requires daemon.
If daemon is not running, exits silently (no error).

#### `spindle disc status`

Check optical drive readiness via ioctl. No daemon required.
Requires a raw device path (`/dev/srN`) in config; `disc:N` format not supported.

Reports: device path, drive status string, ready boolean.

### 1.8 Cache Commands

Parent: `spindle cache`

Manages the rip cache (MakeMKV output between ripping and encoding).

#### `spindle cache rip [device]`

Rip a disc into the rip cache without proceeding to encoding.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--device` | `-d` | string | (configured optical_drive) | Optical device path |

Refuses to run when daemon is running. Runs identification + ripping stages.

#### `spindle cache stats`

Show all cached entries with sizes and ages.

Output: entry count, total/max size, free disk space, numbered list of entries
with primary file name, video count, size, and last update time.

#### `spindle cache process <number>`

Queue a cached rip for post-rip processing.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--allow-duplicate` | bool | false | Allow multiple queue items with same disc fingerprint |

Arguments: `<number>` -- entry number from `cache stats` (required).

#### `spindle cache remove <number>`

Remove a specific cache entry by number.

Arguments: `<number>` -- entry number from `cache stats` (required).

#### `spindle cache clear`

Remove all cache entries.

#### `spindle cache crop <entry|path>`

Run crop detection on a cached rip file (troubleshooting).

Arguments: Cache entry number or direct path to a video file.

Output: video resolution, HDR status, crop detection result, crop filter,
sample analysis with candidate distribution.

#### `spindle cache commentary <entry|path>`

Run commentary detection on a cached rip file (troubleshooting).

Arguments: Cache entry number or direct path to a video file/directory.

Output: primary audio track, similarity/confidence thresholds, commentary
indices, per-candidate details (language, channels, similarity, downmix
detection, LLM decision), diagnostic transcripts saved to files.

### 1.9 Config Commands

Parent: `spindle config`

#### `spindle config init`

Create a sample configuration file.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--path` | `-p` | string | (default config path) | Destination file path |
| `--overwrite` | | bool | false | Overwrite existing file |

Skips config loading (annotation: `skipConfigLoad`).

#### `spindle config validate`

Validate configuration file. Loads config, ensures directories, reports path
and validity.

### 1.10 Staging Commands

Parent: `spindle staging`

#### `spindle staging list`

List staging directories. Shows fingerprint (truncated to 12 chars), age, size,
and total summary.

#### `spindle staging clean`

Remove orphaned staging directories.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--all` | bool | false | Remove all staging directories (including active) |

Default: removes only directories not associated with any current queue item.

### 1.11 Disc ID Commands

Parent: `spindle discid`

Manages the disc ID cache (Blu-ray disc ID to TMDB ID mappings).

#### `spindle discid list`

List all cached disc ID mappings. Numbered, sorted by most recently cached.
Shows title, TMDB ID, media type, season, disc ID (truncated), cached date.

#### `spindle discid remove <number>`

Remove a specific cache entry by number from `discid list`.

#### `spindle discid clear`

Remove all disc ID cache entries.

### 1.12 Audit Command

#### `spindle audit-gather <item-id>`

Gather audit artifacts for a queue item as structured JSON.

Arguments: `<item-id>` -- queue item ID (required).

Output: JSON containing queue metadata, parsed log entries, rip cache contents,
ripspec envelope, encoding details, ffprobe output for encoded files. Designed
for consumption by the `itemaudit` skill.

### 1.13 Internal Commands

#### `spindle daemon`

Run the spindle daemon process. Hidden command, not user-facing. Launched by
`spindle start` via `daemonctl`.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--diagnostic` | bool | false | Enable diagnostic mode |

Skips config loading (loads its own config internally).

---

## 2. IPC Protocol

### 2.1 Transport

- Unix domain socket (path: `$XDG_RUNTIME_DIR/spindle.sock` or `--socket`)
- Protocol: JSON-RPC 1.0 via `net/rpc/jsonrpc`
- Service name: `Spindle`
- Client dial timeout: 2 seconds

### 2.2 Method Catalog

All methods use the form `Spindle.<Method>`.

#### Spindle.Start

Start daemon workflow processing.

```
Request:  {} (empty)
Response: { "started": bool, "message": string }
```

#### Spindle.Stop

Stop daemon workflow processing.

```
Request:  {} (empty)
Response: { "stopped": bool }
```

#### Spindle.Status

Retrieve combined daemon/workflow status.

```
Request:  {} (empty)
Response: {
  "running":              bool,
  "disc_paused":          bool,
  "netlink_monitoring":   bool,
  "queue_stats":          map[string]int,
  "system_checks":        [{ "label": string, "severity": string, "detail": string }],
  "library_paths":        [{ "label": string, "severity": string, "detail": string }],
  "dependency_summary":   { "total": int, "available": int, "missingRequired": int, "missingOptional": int, "severity": string, "detail": string },
  "last_error":           string,
  "last_item":            QueueItem | null,
  "lock_path":            string,
  "queue_db_path":        string,
  "stage_health":         [{ "name": string, "ready": bool, "detail": string }],
  "dependencies":         [{ "name": string, "command": string, "description": string, "optional": bool, "available": bool, "detail": string }],
  "pid":                  int
}
```

#### Spindle.QueueList

List queue items, optionally filtered by status.

```
Request:  { "statuses": []string }
Response: { "items": []QueueItem }
```

#### Spindle.QueueDescribe

Get details for a single queue item.

```
Request:  { "id": int64 }
Response: { "found": bool, "item": QueueItem }
```

#### Spindle.QueueClear

Remove all items from the queue.

```
Request:  {} (empty)
Response: { "removed": int64 }
```

#### Spindle.QueueClearCompleted

Remove only completed items.

```
Request:  {} (empty)
Response: { "removed": int64 }
```

#### Spindle.QueueClearFailed

Remove only failed items.

```
Request:  {} (empty)
Response: { "removed": int64 }
```

#### Spindle.QueueReset

Reset in-flight (stuck) items to pending.

```
Request:  {} (empty)
Response: { "updated": int64 }
```

#### Spindle.QueueRetry

Retry failed items. Empty IDs means all failed items.

```
Request:  { "ids": []int64 }
Response: { "updated": int64 }
```

#### Spindle.QueueRetryEpisode

Retry a single failed episode within a queue item.

```
Request:  { "id": int64, "episode_key": string }
Response: { "result": RetryItemResult }
```

`RetryItemResult` outcome values: `"retried"`, `"not_found"`, `"not_failed"`,
`"episode_not_found"`.

#### Spindle.QueueStop

Stop processing for specific items.

```
Request:  { "ids": []int64 }  (at least one required)
Response: { "updated": int64 }
```

#### Spindle.QueueRemove

Remove specific items by ID.

```
Request:  { "ids": []int64 }  (at least one required)
Response: { "removed": int64 }
```

#### Spindle.LogTail

Fetch log lines from the daemon log file.

```
Request:  {
  "offset":      int64,   // file byte offset (0 = start)
  "limit":       int,     // max lines to return
  "follow":      bool,    // wait for new lines
  "wait_millis": int      // max follow wait (default 1s when follow=true)
}
Response: {
  "lines":  []string,
  "offset": int64         // next offset for continuation
}
```

#### Spindle.DatabaseHealth

Retrieve detailed database diagnostics.

```
Request:  {} (empty)
Response: {
  "db_path":           string,
  "database_exists":   bool,
  "database_readable": bool,
  "schema_version":    string,
  "table_exists":      bool,
  "columns_present":   []string,
  "missing_columns":   []string,
  "integrity_check":   bool,
  "total_items":       int,
  "error":             string
}
```

#### Spindle.TestNotification

Trigger a notification test via the daemon.

```
Request:  {} (empty)
Response: { "sent": bool, "message": string }
```

#### Spindle.DiscPause

Pause detection of new disc insertions.

```
Request:  {} (empty)
Response: { "paused": bool, "message": string }
```

#### Spindle.DiscResume

Resume detection of new disc insertions.

```
Request:  {} (empty)
Response: { "resumed": bool, "message": string }
```

#### Spindle.DiscDetect

Trigger disc detection using the configured device.

```
Request:  {} (empty)
Response: { "handled": bool, "message": string, "item_id": int64 }
```

### 2.3 Queue Access Fallback

Queue commands (list, describe, clear, retry, stop, etc.) support direct SQLite
access when the daemon is not running. The CLI detects daemon unavailability and
falls back to `queueaccess.Access` which wraps the SQLite store directly. This
means queue inspection works without a running daemon.

---

## 3. HTTP API

### 3.1 Server Configuration

- **Bind address**: `paths.api_bind` (e.g., `127.0.0.1:8484`)
- **Auth token**: `paths.api_token` -- when set, all requests require
  `Authorization: Bearer <token>` header
- **Server timeouts**:
  - ReadHeaderTimeout: 5s
  - ReadTimeout: 15s
  - WriteTimeout: 30s
  - IdleTimeout: 60s

The API server starts only when `api_bind` is configured. It runs alongside
the daemon and shuts down with 5s grace period.

The primary external consumer of this API is **Flyer**, a read-only TUI
([github.com/five82/flyer](https://github.com/five82/flyer)).

### 3.2 Authentication

When `api_token` is configured, all endpoints require:
```
Authorization: Bearer <token>
```

Missing or invalid token returns `401 Unauthorized`.

**Middleware**: Bearer token validation wraps all HTTP handlers. When
`api_token` is empty, the middleware is a passthrough (no auth required).

All endpoints enforce `GET` only; other methods return `405 Method Not Allowed`.

### 3.3 Error Response Format

All errors use JSON:
```json
{"error": "error message"}
```

### 3.4 Endpoints

#### GET /api/status

Returns daemon status.

**Response** (200):
```json
{
  "running": true,
  "pid": 12345,
  "queueDbPath": "/path/to/queue.db",
  "lockFilePath": "/path/to/spindle.lock",
  "workflow": {
    "running": true,
    "queueStats": {"pending": 2, "completed": 5},
    "lastError": "",
    "lastItem": null,
    "stageHealth": [{"name": "identifier", "ready": true, "detail": ""}]
  },
  "dependencies": [
    {"name": "makemkvcon", "command": "makemkvcon", "description": "...", "optional": false, "available": true}
  ]
}
```

#### GET /api/queue

Returns queue items, optionally filtered.

**Query parameters**:

| Param | Type | Description |
|-------|------|-------------|
| `status` | string (repeatable) | Filter by queue status |

**Response** (200):
```json
{
  "items": [
    {
      "id": 1,
      "discTitle": "MOVIE_TITLE",
      "status": "completed",
      "processingLane": "background",
      "progress": {"stage": "organizing", "percent": 100, "message": "done"},
      ...
    }
  ]
}
```

#### GET /api/queue/{id}

Returns a single queue item by ID.

**Response** (200):
```json
{
  "item": { ... QueueItem ... }
}
```

**Response** (404):
```json
{"error": "queue item not found"}
```

#### GET /api/logs

Returns structured log events from the in-memory log stream.

**Query parameters**:

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `since` | uint64 | 0 | Sequence number to fetch from |
| `limit` | int | 200 | Maximum events to return |
| `follow` | string | | `1` or `true` to wait for new events |
| `tail` | string | | `1` or `true` to get the latest events (ignores `since`) |
| `item` | int64 | | Filter by queue item ID |
| `component` | string | | Filter by component label |
| `lane` | string | | Filter by lane (`foreground`, `background`, `*` or `all` for both) |
| `daemon_only` | string | | `1` to show only daemon logs (no item association) |
| `correlation_id` | string | | Filter by correlation/request ID |
| `request` | string | | Alias for `correlation_id` |
| `level` | string | | Minimum log level (debug, info, warn, error) |
| `alert` | string | | Filter by alert flag value |
| `decision_type` | string | | Filter by decision type value |
| `search` | string | | Substring search across message, component, stage, lane, correlation ID, fields, details |

**Query parameter defaults**: `limit` defaults to 200 if invalid or missing.
`offset` defaults to -1 (end of file) if invalid or missing. Invalid `status`
values are silently skipped. String filters (`lane`, `component`, `level`,
etc.) are case-insensitive (compared via `EqualFold`).

**Implicit lane filtering**: When no `item`, `lane`, `daemon_only` filters are
set, background lane logs are excluded by default. Use `lane=*` or `lane=all`
to see all lanes.

**Archive fallback**: When `since` points to a sequence older than the in-memory
stream's first sequence, the event archive on disk is consulted first.

**Response** (200):
```json
{
  "events": [
    {
      "seq": 42,
      "ts": "2025-01-15T10:30:00.000Z",
      "level": "INFO",
      "msg": "disc identification complete",
      "component": "identifier",
      "stage": "identification",
      "item_id": 5,
      "lane": "foreground",
      "correlation_id": "abc123",
      "fields": {"event_type": "stage_complete"},
      "details": [{"label": "Title", "value": "Movie Name"}]
    }
  ],
  "next": 43
}
```

#### GET /api/logtail

Returns raw log lines from a queue item's log file.

**Query parameters**:

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `item` | int64 | (required) | Queue item ID |
| `offset` | int64 | -1 | File byte offset (-1 for end) |
| `limit` | int | 200 | Maximum lines to return |
| `follow` | string | | `1` or `true` to wait for new lines |
| `wait_ms` | int | 5000 | Follow wait timeout in milliseconds |

**Response** (200):
```json
{
  "lines": ["2025-01-15 10:30:00 INFO ...", ...],
  "offset": 4096
}
```

### 3.5 QueueItem Schema

The `QueueItem` JSON object appears in both IPC and HTTP responses:

```json
{
  "id":                      int64,
  "discTitle":               string,
  "sourcePath":              string,
  "status":                  string,
  "processingLane":          string,
  "progress": {
    "stage":                 string,
    "percent":               float64,
    "message":               string,
    "bytesCopied":           int64,
    "totalBytes":            int64
  },
  "encoding":                EncodingStatus | null,
  "errorMessage":            string,
  "createdAt":               string (RFC3339),
  "updatedAt":               string (RFC3339),
  "discFingerprint":         string,
  "rippedFile":              string,
  "encodedFile":             string,
  "finalFile":               string,
  "itemLogPath":             string,
  "needsReview":             bool,
  "reviewReason":            string,
  "metadata":                object (raw JSON),
  "ripSpec":                 object (raw JSON),
  "episodes": [{
    "key":                       string,
    "season":                    int,
    "episode":                   int,
    "title":                     string,
    "stage":                     string,
    "status":                    string,
    "errorMessage":              string,
    "active":                    bool,
    "progress":                  QueueProgress | null,
    "runtimeSeconds":            int,
    "sourceTitleId":             int,
    "sourceTitle":               string,
    "outputBasename":            string,
    "rippedPath":                string,
    "encodedPath":               string,
    "subtitledPath":             string,
    "finalPath":                 string,
    "subtitleSource":            string,
    "subtitleLanguage":          string,
    "generatedSubtitleSource":   string,
    "generatedSubtitleLanguage": string,
    "generatedSubtitleDecision": string,
    "matchScore":                float64,
    "matchedEpisode":            int
  }],
  "episodeTotals": {
    "planned":               int,
    "ripped":                int,
    "encoded":               int,
    "final":                 int
  },
  "episodeIdentifiedCount":  int,
  "episodesSynchronized":    bool,
  "subtitleGeneration": {
    "opensubtitles":           int,
    "whisperx":                int,
    "expectedOpenSubtitles":   bool,
    "fallbackUsed":            bool
  },
  "primaryAudioDescription": string,
  "commentaryCount":         int
}
```

### 3.6 Log Access Transport Layer

The `logstream` package provides a unified log access interface with automatic
transport fallback:

1. **Primary**: HTTP API via `StreamClient.Fetch()` -- structured log events
   with full filtering.
2. **Fallback**: IPC `LogTail` -- raw line-based tailing (no structured
   filtering).
3. **Error**: `ErrFiltersRequireAPI` when filters need API features but API
   is unavailable.

The `logs` package provides:

- `Tail(ctx, path, opts)`: Direct file tailing with follow support. Used for
  per-item log files.
- `StreamClient`: HTTP client for `/api/logs`. Supports all 12 filter
  parameters from Section 3.4.
- `ErrAPIUnavailable` / `IsAPIUnavailable(err)`: Transport error detection.

**Legacy IPC polling**: When falling back to IPC `LogTail`, the stream
function polls at a fixed 1000ms (1 second) interval. Follow mode uses
context cancellation for shutdown (no explicit follow timeout).
