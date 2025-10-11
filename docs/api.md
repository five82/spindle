# Spindle HTTP API

Spindle ships with a lightweight HTTP API that mirrors the daemon's internal
state. Remote tooling (dashboards, TUIs, scripts) can consume these endpoints to
monitor progress without shelling out to the CLI.

> ⚠️ **Scope**: the API is read-only today. All mutating operations still flow
> through the CLI or future RPC endpoints. Authentication is not implemented; run
> Spindle on trusted networks only.

## Getting Started

- Ensure the daemon is running (`spindle start`).
- Default bind address: `http://127.0.0.1:7487`. Override via
  `api_bind = "host:port"` in `config.toml`.
- Responses are JSON with RFC3339 timestamps using millisecond precision.

## Endpoints

### `GET /api/status`

Returns daemon runtime information and workflow diagnostics.

```json
{
  "running": true,
  "pid": 12345,
  "queueDbPath": "/home/user/.local/share/spindle/logs/queue.db",
  "lockFilePath": "/home/user/.local/share/spindle/logs/spindle.lock",
  "draptoLogPath": "/home/user/.local/share/spindle/logs/drapto.log",
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
      { "name": "organizer", "ready": false, "detail": "missing plex url" }
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

Example: `GET /api/queue?status=failed&status=review`

```json
{
  "items": [
    {
      "id": 12,
      "discTitle": "Some Movie",
      "sourcePath": "/media/cdrom",
      "status": "failed",
      "progress": { "stage": "Ripping", "percent": 10 },
      "errorMessage": "Unable to read title 1",
      "createdAt": "2025-10-05T14:31:22.123Z",
      "updatedAt": "2025-10-05T14:42:57.812Z",
      "discFingerprint": "abcdef123456",
      "needsReview": false
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

## Field Reference

| Field | Description |
| --- | --- |
| `queueDbPath` | Full path to the SQLite queue database used by the daemon. |
| `draptoLogPath` | Pointer to the latest Drapto encoder log (symlink or copy). |
| `workflow.queueStats` | Map keyed by lifecycle status -> item count. Matches `internal/queue.Status` values. |
| `workflow.stageHealth` | Stage readiness results from `StageHandler.HealthCheck`. Useful for dependency dashboards. |
| `items[].progress` | Stage name, percent 0-100, and last message recorded for the item. |
| `items[].metadata` | Raw TMDB/metadata JSON captured during identification. Omitted when empty. |

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
