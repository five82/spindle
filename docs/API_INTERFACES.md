# API Reference: Interfaces

CLI commands and HTTP API for the Spindle system.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. CLI Interface

### 1.1 Binary

Single binary: `spindle`

### 1.2 Global Flags

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--socket` | | string | `$XDG_RUNTIME_DIR/spindle.sock` | Path to the daemon HTTP API Unix socket |
| `--config` | `-c` | string | `$XDG_CONFIG_HOME/spindle/config.toml` | Configuration file path |
| `--log-level` | | string | (from config) | Log level: debug, info, warn, error |
| `--verbose` | `-v` | bool | false | Shorthand for `--log-level=debug` |
| `--json` | | bool | false | Output in JSON format |

### 1.3 Daemon Commands

#### `spindle start`

Start the spindle daemon. If no daemon process is running, launches one.

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

Behavior:
- Stop timeout: 5s, start timeout: 10s
- Reports stop details then start result

#### `spindle status`

Show system and queue status.

Output sections:
1. **System Status**: Daemon running state, PID, disc pause, netlink monitoring
2. **Drive Status**: Optical drive readiness (via ioctl when device is `/dev/srN`)
3. **Dependencies**: Per-dependency availability (makemkvcon, ffmpeg, etc.)
4. **Library Paths**: Movie and TV library path accessibility
5. **Queue Status**: Table of status counts

Works without daemon by falling back to direct DB access for queue stats.

### 1.4 Queue Commands

Parent: `spindle queue`

#### `spindle queue list`

List queue items.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--stage` | `-s` | string[] | (all) | Filter by queue stage (repeatable) |

Table columns: ID, Title, Status, Created, Fingerprint.

#### `spindle queue show <id>`

Show detailed information for a single queue item.

Arguments: `<id>` -- queue item ID (required, exactly 1).

Output includes: ID, title, status, timestamps, disc fingerprint,
progress, review status, error, file paths (ripped/encoded/final), metadata,
episode details with per-episode progress and subtitle info, and rip spec
fingerprints.

#### `spindle queue clear [id...]`

Remove queue items.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--all` | bool | false | Remove all items |
| `--completed` | bool | false | Remove only completed items |

Rules:
- Requires either item IDs or exactly one flag
- Cannot combine IDs with flags
- Cannot combine multiple flags

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

### 1.5 Logs Command

#### `spindle logs`

Display daemon logs.

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--follow` | `-f` | bool | false | Follow log output (polls `/api/logs`) |
| `--lines` | `-n` | int | 10 | Number of lines to show (0 for all) |
| `--component` | | string | | Filter by component label |
| `--lane` | | string | | Filter by processing lane |
| `--request` | | string | | Filter by request/correlation ID |
| `--item` | `-i` | int64 | 0 | Filter by queue item ID |
| `--level` | | string | | Minimum log level (debug, info, warn, error) |

When daemon is running, uses `/api/logs` for filtered queries. Falls back to
direct file tailing when daemon is unavailable (no structured filtering).
`--follow` polls `/api/logs` with a sequence cursor.

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

Send a test notification via the configured ntfy topic. Sends an HTTP POST
directly to the ntfy topic URL; does not require a running daemon.

### 1.7 Disc Commands

Parent: `spindle disc`

#### `spindle disc pause`

Pause detection of new disc insertions. Requires daemon.

#### `spindle disc resume`

Resume detection of new disc insertions. Requires daemon.

#### `spindle disc detect`

Trigger disc detection using the configured optical drive. Requires daemon.
If daemon is not running, exits silently (no error).

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

### 1.12 Debug Commands

Parent: `spindle debug`

Standalone diagnostic tools for troubleshooting encoding and audio analysis.
These run against local files without affecting the queue or requiring a daemon.

#### `spindle debug crop <entry|path>`

Run crop detection on a video file.

Arguments: Cache entry number or direct path to a video file.

Output: video resolution, HDR status, crop detection result, crop filter,
sample analysis with candidate distribution.

#### `spindle debug commentary <entry|path>`

Run commentary detection on a video file.

Arguments: Cache entry number or direct path to a video file/directory.

Output: primary audio track, similarity/confidence thresholds, commentary
indices, per-candidate details (language, channels, similarity, downmix
detection, LLM decision), diagnostic transcripts saved to files.

### 1.13 Audit Command

#### `spindle audit-gather <item-id>`

Gather audit artifacts for a queue item as structured JSON.

Arguments: `<item-id>` -- queue item ID (required).

Output: JSON containing queue metadata, parsed log entries, rip cache contents,
ripspec envelope, encoding details, ffprobe output for encoded files. Designed
for consumption by the `itemaudit` skill.

### 1.14 Internal Commands

#### `spindle daemon`

Run the spindle daemon process. Hidden command, not user-facing. Launched by
`spindle start` via `daemonctl`.

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--diagnostic` | bool | false | Enable diagnostic mode |

Skips config loading (loads its own config internally).

---

## 2. HTTP API

### 2.1 Server Configuration

- **Unix socket**: `$XDG_RUNTIME_DIR/spindle.sock` (always created)
- **TCP bind** (optional): `api.bind` (e.g., `127.0.0.1:7487`)
- **Auth token**: `api.token` -- when set, all requests require
  `Authorization: Bearer <token>` header
- **Server timeouts**:
  - ReadHeaderTimeout: 5s
  - ReadTimeout: 15s
  - WriteTimeout: 30s
  - IdleTimeout: 60s

The HTTP API serves all communication: CLI commands, Flyer TUI, and any other
consumers. Both the Unix socket and optional TCP bind serve identical endpoints.

The primary external consumer is **Flyer**, a read-only TUI
([github.com/five82/flyer](https://github.com/five82/flyer)).

**No API versioning**: This is a single-user, single-TUI project. API
endpoints use `/api/` prefix without version numbers. Breaking changes are
coordinated by updating both Spindle and Flyer together. No backwards
compatibility is maintained across versions.

### 2.2 Authentication

When `api.token` is configured, all endpoints require:
```
Authorization: Bearer <token>
```

Missing or invalid token returns `401 Unauthorized`.

**Middleware**: Bearer token validation wraps all HTTP handlers. When
`api.token` is empty, the middleware is a passthrough (no auth required).

### 2.3 Error Response Format

All errors use JSON:
```json
{"error": "error message"}
```

### 2.4 Read Endpoints

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
    "lastItem": null
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
| `stage` | string (repeatable) | Filter by queue stage |

**Response** (200):
```json
{
  "items": [
    {
      "id": 1,
      "discTitle": "MOVIE_TITLE",
      "stage": "completed",
      "inProgress": false,
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

Returns structured log events parsed from the daemon's JSON log file.

**Query parameters**:

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `limit` | int | 200 | Maximum events to return |
| `since` | uint64 | | Return events after this sequence number (cursor for polling) |
| `tail` | string | | `1` or `true` to get the latest events |
| `item` | int64 | | Filter by queue item ID |
| `component` | string | | Filter by component label |
| `lane` | string | | Filter by processing lane |
| `daemon_only` | string | | `1` to show only daemon logs (no item association) |
| `request` | string | | Filter by request/correlation ID |
| `level` | string | | Minimum log level (debug, info, warn, error) |

**Query parameter defaults**: `limit` defaults to 200 if invalid or missing.
String filters (`component`, `level`, etc.) are case-insensitive (compared
via `EqualFold`).

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
      "lane": "ripping",
      "request": "abc123",
      "fields": {"event_type": "stage_complete"},
      "details": [{"label": "Title", "value": "Movie Name"}]
    }
  ],
  "next": 43
}
```

The `next` field is the sequence cursor for polling. Pass it as the `since`
parameter on the next request to get only newer events.

### 2.5 Mutation Endpoints

#### POST /api/daemon/stop

Stop daemon workflow processing.

**Response** (200):
```json
{"stopped": true}
```

#### POST /api/queue/clear

Remove queue items.

**Request body**:
```json
{"scope": "all" | "completed" | "failed"}
```

**Response** (200):
```json
{"removed": 5}
```

#### POST /api/queue/retry

Retry failed items.

**Request body**:
```json
{"ids": [1, 2, 3]}
```

Empty `ids` retries all failed items.

**Response** (200):
```json
{"updated": 3}
```

#### POST /api/queue/retry-episode

Retry a single failed episode within a queue item.

**Request body**:
```json
{"id": 1, "episode_key": "s01e05"}
```

**Response** (200):
```json
{"result": "retried"}
```

Result values: `"retried"`, `"not_found"`, `"not_failed"`, `"episode_not_found"`.

#### POST /api/queue/stop

Stop processing for specific items.

**Request body**:
```json
{"ids": [1, 2]}
```

**Response** (200):
```json
{"updated": 2}
```

#### DELETE /api/queue/{id}

Remove a specific queue item.

**Response** (200):
```json
{"removed": 1}
```

**Response** (404):
```json
{"error": "queue item not found"}
```

#### POST /api/disc/pause

Pause detection of new disc insertions.

**Response** (200):
```json
{"paused": true, "message": "Disc detection paused"}
```

#### POST /api/disc/resume

Resume detection of new disc insertions.

**Response** (200):
```json
{"resumed": true, "message": "Disc detection resumed"}
```

#### POST /api/disc/detect

Trigger disc detection using the configured device.

**Response** (200):
```json
{"handled": true, "message": "Disc detected", "item_id": 42}
```

### 2.6 Queue Access Fallback

Queue read commands (list, show) support direct SQLite access when
the daemon is not running. The CLI detects daemon unavailability and falls back
to `queueaccess.Access` which wraps the SQLite store directly. Mutation
operations require a running daemon.

### 2.7 QueueItem Schema

The `QueueItem` JSON object returned by queue endpoints:

```json
{
  "id":                      int64,
  "discTitle":               string,
  "stage":                   string,
  "inProgress":              bool,
  "progress": {
    "stage":                 string,
    "percent":               float64,
    "message":               string,
    "bytesCopied":           int64,
    "totalBytes":            int64
  },
  "encoding":                Snapshot | null,
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

### 2.8 Log Access

**Daemon running**: `spindle logs` uses `/api/logs` for filtered log queries.
`--follow` polls with a sequence cursor.

**Daemon not running**: `spindle logs` falls back to direct file tailing of
the daemon log file via `logs.Tail()`. No structured filtering in this mode
-- raw line output only.
