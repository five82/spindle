# System Design: Go Package Layout

Module path: `github.com/five82/spindle`

Package structure, dependency rules, and module boundaries for the Go codebase.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Package Tree

```
cmd/
  spindle/              main package; CLI entry point (cobra commands)

internal/
  config/               TOML config loading, normalization, validation
  queue/                SQLite store, Item model, Stage types, metadata helpers
  ripspec/              RipSpec envelope data model, parse/encode, asset tracking
  stage/                Handler interface, context-scoped logger, ParseRipSpec helper
  services/             Error types (ErrTransient, ErrFatal, ErrDegraded, ErrorDetails)

  daemon/               Daemon lifecycle, lock file, disc pause/resume
  daemonrun/            Daemon runtime entry point (Run function)
  daemonctl/            CLI-facing daemon control (start/stop/restart/status)
  workflow/             Pipeline manager, semaphores, stage wiring, processItem loop

  stageexec/            One-shot stage execution for CLI commands

  identify/             Stage: identification (MakeMKV scan, TMDB, KeyDB, edition)
  ripper/               Stage: ripping (MakeMKV rip, cache, progress)
  contentid/            Stage: episode identification (matching, Hungarian, anchors)
  encoder/              Stage: encoding (Drapto integration, job planning)
  audioanalysis/        Stage: audio analysis (refinement, commentary detection)
  subtitle/             Stage: subtitle generation (WhisperX, forced subs, SRT)
  organizer/            Stage: organization (library copy, review routing, Jellyfin)

  transcription/        Shared WhisperX transcription service with caching

  makemkv/              MakeMKV CLI wrapper (scan, rip, robot format parser)
  tmdb/                 TMDB REST API client
  opensubtitles/        OpenSubtitles REST API client
  llm/                  LLM (OpenRouter) client
  jellyfin/             Jellyfin API client (library refresh)
  notify/               ntfy notification service
  keydb/                KeyDB catalog management (download, lookup, parsing)

  fingerprint/          Disc fingerprinting (Blu-ray, DVD, fallback)
  discmonitor/          Disc detection (netlink/udev, lsblk, tray status, ejection)

  media/
    ffprobe/            FFprobe JSON wrapper
    audio/              Audio track selection algorithm

  ripcache/             Rip cache management (store, prune, restore, metadata)
  discidcache/          Disc ID -> TMDB ID JSON cache

  textutil/             Text processing (TF-IDF, tokenization, cosine, sanitization)
  fileutil/             File operations (copy, verified copy)
  language/             Language code normalization (ISO 639-1/2/3)
  encodingstate/        Encoding snapshot state (Drapto telemetry)
  staging/              Staging directory management (list, clean stale/orphaned)
  deps/                 Dependency checking (binary resolution, requirements)

  httpapi/              HTTP API server, route registration, middleware
  queueaccess/          Queue access abstraction (HTTP client + direct store)
  logs/                 StreamHub, EventArchive, log tailing, StreamClient
  logstream/            Log access with automatic HTTP/file fallback
  auditgather/          Audit artifact collection and analysis
```

---

## 2. Dependency Rules

Dependency flow is strictly top-down. Packages in lower layers must never
import packages in higher layers.

### Layer 1: Foundation (no internal imports)

`services`, `textutil`, `fileutil`, `language`, `encodingstate`, `deps`

### Layer 2: Data Models (depend on Layer 1 only)

`config`, `queue`, `ripspec`, `stage`

### Layer 3: External Clients (depend on Layers 1-2)

`tmdb`, `opensubtitles`, `llm`, `jellyfin`, `notify`, `keydb`,
`makemkv`, `fingerprint`, `discmonitor`, `media/ffprobe`, `media/audio`,
`ripcache`, `discidcache`, `staging`, `transcription`

### Layer 4: Stage Handlers (depend on Layers 1-3)

`identify`, `ripper`, `contentid`, `encoder`, `audioanalysis`,
`subtitle`, `organizer`

### Layer 5: Orchestration (depend on Layers 1-4)

`workflow`, `stageexec`, `httpapi`, `queueaccess`, `logs`, `logstream`,
`auditgather`

### Layer 6: Daemon (depend on Layers 1-5)

`daemon`, `daemonrun`, `daemonctl`

### Layer 7: Entry Point

`cmd/spindle` (imports all layers)

### Prohibited Dependencies

- Stage handlers must not import each other.
- `queue` must not import `ripspec` (the envelope is opaque TEXT to the store).
- `config` must not import any client packages.
- No package may import `cmd/spindle`.

---

## 3. Key Interfaces and Boundaries

### 3.1 Stage Handler Boundary

All stage handlers implement `stage.Handler` (Prepare + Execute). The
`workflow` package dispatches via this interface and never imports concrete
stage packages directly -- they are wired in `daemonrun` via
`ConfigureStages()`.

### 3.2 Queue Store Boundary

The `queue.Store` interface is the sole access point for SQLite operations.
Stage handlers receive `*queue.Item` values and call `store.Update()` /
`store.UpdateProgress()` to persist changes. Direct SQL is confined to the
`queue` package.

### 3.3 External Client Boundary

Each external service has its own client package with a concrete struct.
Stage handlers accept these clients as constructor dependencies (struct
fields, not interfaces) since there is only one implementation per client.
Test doubles use interfaces defined at the consumer site (see
DESIGN_TESTING.md).

### 3.4 Transcription Boundary

The `transcription.Service` is shared by `contentid`, `audioanalysis`, and
`subtitle`. Each stage receives the same `*transcription.Service` instance
via constructor injection. The service is stateless; concurrency is managed
by the `whisperxSem` semaphore at the workflow level.

---

## 4. Build Tags

No build tags required. The pure-Go SQLite driver (`modernc.org/sqlite`)
eliminates CGo. Platform-specific code (netlink, ioctl) is confined to
`discmonitor` and uses `//go:build linux` constraints.

---

## 5. Module Dependencies

### 5.1 Direct Dependencies

| Module | Purpose |
|--------|---------|
| `github.com/five82/drapto` | SVT-AV1 encoding library |
| `modernc.org/sqlite` | Pure-Go SQLite driver |
| `github.com/pelletier/go-toml/v2` | TOML config parsing |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/gofrs/flock` | File locking |

### 5.2 Drapto Local Development

Uses `go.work` (gitignored) to reference `../drapto` locally. CI uses
the version pinned in `go.mod`. See AGENTS.md for the update workflow.
