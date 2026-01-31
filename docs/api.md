# Spindle HTTP API

Spindle ships with a read-only HTTP API that mirrors the daemon's internal
state. Remote tooling (dashboards, TUIs, scripts) can consume these endpoints to
monitor progress without shelling out to the CLI.

> ⚠️ **Scope**: the API is read-only today. All mutating operations still flow
> through the CLI or IPC socket.

## Getting Started

- Ensure the daemon is running (`spindle start`).
- Default bind address: `http://127.0.0.1:7487`. Override via
  `api_bind = "host:port"` in `config.toml`.
- Responses are JSON with RFC3339 timestamps using millisecond precision.

## Authentication

When `api_token` is set in config (or `SPINDLE_API_TOKEN` environment variable),
all API requests require a bearer token:

```bash
curl -H "Authorization: Bearer <token>" http://localhost:7487/api/status
```

Requests without a valid token receive `401 Unauthorized`. When no token is
configured, authentication is disabled and all requests are allowed.

**Remote access setup:**

1. Set `api_bind = "0.0.0.0:7487"` to listen on all interfaces
2. Generate a token: `openssl rand -hex 32`
3. Set `api_token` in config or export `SPINDLE_API_TOKEN`
4. Restart the daemon

For TLS, consider running behind a reverse proxy (nginx, Caddy) or using
Tailscale for encrypted point-to-point connections.

## Endpoints

### `GET /api/status`

Returns daemon runtime information and workflow diagnostics. With the episode
identification stage enabled, expect an additional `stageHealth` entry named
`episode-identifier` plus new queue counts for `episode_identifying` and
`episode_identified`.

```json
{
  "running": true,
  "pid": 12345,
  "queueDbPath": "/home/user/.local/share/spindle/logs/queue.db",
  "lockFilePath": "/home/user/.local/share/spindle/logs/spindle.lock",
  "workflow": {
    "running": true,
    "queueStats": {
      "pending": 1,
      "encoding": 1,
      "completed": 4
    },
    "lastError": "",
    "lastItem": {
      "id": 8,
      "discTitle": "Example Title",
      "status": "encoding",
      "progress": {
        "stage": "Encoding",
        "percent": 72.5,
        "message": "Pass 2"
      }
    },
    "stageHealth": [
      { "name": "encoder", "ready": true },
      { "name": "organizer", "ready": false, "detail": "jellyfin client unavailable" }
    ]
  },
  "dependencies": [
    { "name": "MakeMKV", "command": "makemkvcon", "available": true },
    { "name": "Drapto", "command": "drapto", "available": false,
      "detail": "binary \"drapto\" not found" }
  ]
}
```

### `GET /api/queue`

Lists queue items. Optional query parameters:

- `status=<value>` — repeatable. Filters by lifecycle status (e.g.
  `pending`, `failed`).

Example: `GET /api/queue?status=episode_identifying&status=failed`

```json
{
  "items": [
    {
      "id": 12,
      "discTitle": "Some Movie",
      "sourcePath": "/media/cdrom",
      "status": "episode_identifying",
      "progress": { "stage": "Episode identification", "percent": 42 },
      "errorMessage": "Unable to read title 1",
      "createdAt": "2025-10-05T14:31:22.123Z",
      "updatedAt": "2025-10-05T14:42:57.812Z",
      "discFingerprint": "abcdef123456",
      "needsReview": false,
      "episodes": [
        {
          "key": "s05e01",
          "season": 5,
          "episode": 1,
          "title": "Pilot",
          "stage": "encoded",
          "encodedPath": "/staging/fingerprint/encoded/Show - S05E01.mkv",
          "subtitleSource": "whisperx_opensubtitles",
          "subtitleLanguage": "en"
        }
      ],
      "episodeTotals": { "planned": 4, "ripped": 4, "encoded": 2, "final": 0 },
      "episodesSynchronized": true
    }
  ]
}
```

### `GET /api/queue/{id}`

Returns metadata for a single queue entry.

- `404` when the item does not exist.
- `400` when `{id}` is not a positive integer.

```json
{
  "item": {
    "id": 15,
    "discTitle": "Unknown Disc",
    "sourcePath": "",
    "status": "review",
    "progress": { "stage": "Manual review", "percent": 100 },
    "errorMessage": "TMDB lookup failed",
    "metadata": {
      "title": "Unknown Disc",
      "movie": true
    },
    "needsReview": true,
    "reviewReason": "Low confidence match"
  }
}
```

### `GET /api/logtail`

Returns raw lines from a queue item's log file (the per-item log stored under
`log_dir/items/`). This is intended for remote dashboards and
TUIs that cannot access the daemon's filesystem.

Query parameters:

- `item=<id>` (required) — queue item id.
- `offset=<bytes>` (optional) — file offset to read from. Use `-1` to tail the
  last `limit` lines (default).
- `limit=<n>` (optional) — maximum number of lines to return (default 200).
- `follow=1` (optional) — wait for new lines when at EOF.
- `wait_ms=<n>` (optional) — max wait duration when `follow=1` (default 5000ms).

```json
{
  "lines": ["..."],
  "offset": 123456
}
```

## Field Reference

| Field | Description |
| --- | --- |
| `queueDbPath` | Full path to the SQLite queue database used by the daemon. |
| `workflow.queueStats` | Map keyed by lifecycle status -> item count. Includes the episode identification states (`episode_identifying`, `episode_identified`) when TV discs are flowing. |
| `workflow.stageHealth` | Stage readiness results from `StageHandler.HealthCheck`, including the `episode-identifier` handler when enabled. For dependency dashboards. |
| `items[].status` | Current lifecycle state from `internal/queue.Status` (`pending → identifying → identified → ripping → ripped → episode_identifying → episode_identified → encoding → encoded → subtitling → subtitled → organizing → completed`, plus `failed`/`review`). |
| `items[].progress` | Stage name, percent 0-100, and last message recorded for the item. Episode identification surfaces as "Episode identification". |
| `items[].draptoPresetProfile` | Drapto preset applied during encoding: `clean`, `grain`, or omitted/`default` when Drapto runs with its stock settings. |
| `items[].metadata` | Raw TMDB/metadata JSON captured during identification. Omitted when empty. |
| `items[].episodes[]` | One entry per planned episode on a TV disc. Includes season/episode numbers once verified, current stage (`planned`, `ripped`, `encoded`, `final`), runtime, artifact paths, and subtitle match info. Empty for movie discs. |
| `items[].episodeTotals` | Aggregate counts (`planned`, `ripped`, `encoded`, `final`) derived from the per-episode map. For progress bars. |
| `items[].episodesSynchronized` | `true` when WhisperX/OpenSubtitles confirmed the episode order and both `MetadataJSON`/rip spec were updated. `false` when Spindle is still relying on heuristic disc ordering. |
| `items[].itemLogPath` | Full path to the per-item log file on disk when available. |

### Episode Identification States

The API does not need special handling for the episode identification stage: it
emits whatever status the queue assigns. Once the stage runs, items advance from
`ripped` to `episode_identifying` while WhisperX/OpenSubtitles align episodes;
on success they flip to `episode_identified` before the encoder picks them up.
API consumers should treat these states like any other processing hop, updating
dashboards or filters if they previously assumed `ripped` flowed straight to
`encoding`.

## Versioning & Compatibility

- Endpoints are additive and currently unversioned. Breaking changes will bump
  in-tree clients (CLI, TUI) simultaneously.
- Keep API consumers defensive: new fields may appear without prior notice.
- If versioned endpoints become necessary, `/api/v1/...` is reserved for that
  future expansion.

## Tips for Tooling

- Poll `/api/status` for high-level dashboards; combine with `/api/queue` to
  embed detailed views as needed.
- Respect the daemon's work cadence—default queue poll interval is 5s. Avoid
  sub-second polling unless you control both ends.
- The API runs inside the daemon process; querying it when the daemon is not
  running will fail with a connection error.
