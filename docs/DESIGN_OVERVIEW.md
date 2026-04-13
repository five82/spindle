# System Design: Overview

Introduction, external dependencies, services, architecture, configuration, and
filesystem layout for the Spindle system.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Introduction

### 1.1 Purpose

Spindle automates the complete workflow from optical disc to Jellyfin media library:
disc detection, ripping (MakeMKV), encoding (Drapto/SVT-AV1), metadata lookup (TMDB),
subtitle generation (canonical transcription + Stable-TS display formatting + OpenSubtitles forced subs), commentary detection,
episode identification, and library organization with Jellyfin refresh.

### 1.2 Scope

Single-developer, single-machine deployment. One optical drive, one processing
pipeline. Designed for a home media server operator who inserts discs and expects
them to appear in Jellyfin without manual intervention.

### 1.3 Deployment Model

- **Single binary**: `spindle` -- daemon mode and CLI commands in one executable.
- **Daemon**: Long-running background process that monitors for disc insertions and
  processes the work queue.
- **CLI**: Commands for daemon control, queue management, cache management, and
  standalone workflow operations (identify, generate subtitles, etc.).
- **Queue commands work without daemon**: Queue inspection and management fall back to
  direct SQLite access when the daemon is not running.

### 1.4 Known Consumers

- **Flyer** ([github.com/five82/flyer](https://github.com/five82/flyer)): A read-only
  TUI that connects to Spindle's HTTP API to display queue status and encoding
  progress. Flyer is a separate project and not part of Spindle itself, but it
  is the primary external consumer of the API.

---

## 2. External Dependencies

### 2.1 Required Binaries

| Binary       | Purpose                                    |
|--------------|--------------------------------------------|
| `makemkvcon` | Disc scanning and MKV ripping              |
| `ffmpeg`     | Audio/video encoding (via Drapto library)  |
| `ffprobe`    | Media file inspection and validation       |
| `mediainfo`  | Metadata inspection for disc titles        |

### 2.2 Optional Binaries

| Binary     | Purpose                                            | When Required                  |
|------------|----------------------------------------------------|--------------------------------|
| `bd_info`  | Enhanced Blu-ray metadata (disc name, year, studio)| Always optional; improves ID   |
| `uv`       | Managed Python runtime bootstrap for Parakeet/NeMo | When subtitles.enabled = true  |
| `uvx`      | Stable-TS subtitle formatting package runner       | When subtitles.enabled = true  |
| `mkvmerge` | Muxing subtitles into MKV containers               | When subtitles.mux_into_mkv = true |

### 2.3 Library Dependencies

| Library | Purpose                              |
|---------|--------------------------------------|
| Drapto  | Go library for SVT-AV1 encoding     |
| SQLite  | Queue database (via modernc.org/sqlite, pure-Go) |

---

## 3. External Services

### 3.1 TMDB (The Movie Database)

- **Purpose**: Identify disc content (movie or TV show), get metadata.
- **Base URL**: `https://api.themoviedb.org/3` (configurable)
- **Auth**: Bearer token via `tmdb.api_key`
- **Endpoints used**: `/search/movie`, `/search/tv`, `/movie/{id}`, `/tv/{id}`,
  `/tv/{id}/season/{season}`, `/search/multi`
- **Language**: Configurable (default: `en-US`)

### 3.2 OpenSubtitles

- **Purpose**: Download reference subtitles for episode identification; download
  forced (foreign-parts-only) subtitles.
- **Base URL**: `https://api.opensubtitles.com/api/v1` (default)
- **Auth**: API key via `Api-Key` header; optional user token for download quota.
- **Rate limiting**: Client-side delay between API calls (3 second minimum).
- **Endpoints used**: `/subtitles` (search), `/download` (download), `/infos/formats` (health check)

### 3.3 Jellyfin

- **Purpose**: Trigger library refresh after organizing files.
- **Auth**: API key via `X-Emby-Token` header.
- **Endpoints used**: `/Library/Refresh`, `/Users` (health check)

### 3.4 ntfy

- **Purpose**: Push notifications for pipeline events.
- **Protocol**: HTTP POST to topic URL with headers for title, priority, tags.
- **Auth**: None (public topics) or configured via topic URL.

### 3.5 LLM (via OpenRouter)

- **Purpose**: Commentary classification, episode verification.
- **Base URL**: `https://openrouter.ai/api/v1/chat/completions` (configurable)
- **Default model**: `google/gemini-3-flash-preview`
- **Auth**: API key via `Authorization: Bearer` header.
- **Headers**: `HTTP-Referer`, `X-Title` for OpenRouter routing.
- **Usage**: Commentary detection and episode verification use `[llm]` settings.

### 3.6 KeyDB

- **Purpose**: Disc ID to title mapping database for Blu-ray identification.
- **Format**: Zip file containing `KEYDB.cfg` with disc ID entries.
- **Download URL**: `http://fvonline-db.bplaced.net/export/keydb_eng.zip` (configurable)
- **Local path**: `~/.config/spindle/keydb/KEYDB.cfg`

---

## 4. Architecture Overview

### 4.1 Binary Structure

Single binary `spindle` with two execution modes:

1. **Daemon mode** (`spindle daemon`): Starts the full daemon with disc
   monitoring, workflow manager, and HTTP API server.
2. **CLI mode**: All other commands. Some require a running daemon (via HTTP
   API), others work standalone (direct DB access or local operations).

### 4.2 Communication Layer

```
                    +-----------+
                    | CLI User  |
                    +-----+-----+
                          |
                    +-----v------+
                    | HTTP API   |
                    | (REST)     |
                    +-----+------+
                          |
              +-----------+-----------+
              |                       |
     +--------v--------+    +--------v--------+
     | Unix Socket      |    | TCP (optional)  |
     | (CLI + local)    |    | (Flyer, remote) |
     +--------+--------+    +--------+--------+
              |                       |
              +-----------+-----------+
                          |
                   +------v------+
                   |   Daemon    |
                   +------+------+
                          |
              +-----------+-----------+
              |                       |
     +--------v--------+    +--------v--------+
     | Workflow Manager |    | Disc Monitor    |
     | (Queue + Stages) |    | (Netlink/Udev)  |
     +-----------------+    +-----------------+
```

**Single HTTP API**: All communication uses HTTP REST endpoints. The server
listens on:
- **Unix socket**: `$XDG_RUNTIME_DIR/spindle.sock` (primary, used by CLI)
- **TCP** (optional): `api.bind` address (e.g., `127.0.0.1:7487`) for remote
  consumers like Flyer

Both listeners serve the same endpoints and share the same auth model. Token
auth via `api.token` applies to both.

**Queue access fallback**: When daemon is unavailable, CLI queue commands open
the SQLite database directly for read-only operations.

### 4.3 Concurrency Model: Single Pipeline with Resource Semaphores

The workflow manager runs a single processing pipeline. Multiple items can be
in-flight concurrently (one encoding while another is being identified), but
several resources require exclusive access, each guarded by a semaphore.

**Stage sequence** (per item):
1. **Identification** (pending -> identification)
2. **Ripping** (identification -> ripping)
3. **Episode Identification** (ripping -> episode_identification) [TV only]
4. **Encoding** (episode_identification/ripping -> encoding)
5. **Audio Analysis** (encoding -> audio_analysis)
6. **Subtitle Generation** (audio_analysis -> subtitling)
7. **Organization** (subtitling -> organizing -> completed)

Each stage transitions the item's `stage` field. The `in_progress` flag
tracks whether work is actively executing (see DESIGN_QUEUE.md Section 4).

**Resource semaphores** (all capacity 1):

| Semaphore | Guards | Stages |
|-----------|--------|--------|
| `discSem` | Optical drive | Identification, Ripping |
| `encodeSem` | SVT-AV1 encoder (CPU-bound) | Encoding |
| `transcriptionSem` | shared transcription runtime (GPU/model) | Episode Identification, Audio Analysis (commentary), Subtitling |

Each semaphore prevents concurrent use of a constrained resource. A stage
acquires its required semaphore before execution and releases it on
completion, cancellation, or failure. Stages that do not require a semaphore
(Organization) run freely.

**Pipeline poll loop** (`run()`):
1. Check `ctx.Done()` for shutdown.
2. Fetch next ready item via `NextReady()` where `in_progress = 0`, ordered
   by stage priority (earlier stages first) then creation time (FIFO within
   same stage).
3. If no item: wait 5 seconds, loop.
4. If queue fetch error: log, wait 10 seconds, loop.
5. Spawn goroutine: acquire required semaphore(s), set `in_progress = 1`,
   process item via `processItem()`, advance stage, release semaphore(s).
6. Loop (immediately look for next item; don't wait for spawned goroutine).

**Stage priority** ensures disc-dependent stages run first (freeing the drive
for the next disc), while encoding and later stages run concurrently in the
background.

### 4.4 Stage Configuration Wiring

`ConfigureStages(stages []pipelineStage)` registers an ordered slice of stage
handlers and builds the pipeline chain. Absent stages are simply omitted from
the slice -- no nil checks or conditional ordering needed.

**pipelineStage** -- single stage descriptor:
```go
type Semaphore int

const (
    SemNone     Semaphore = iota
    SemDisc                        // guards optical drive
    SemEncode                      // guards SVT-AV1 encoder
    SemTranscription                    // guards transcription runtime
)

type pipelineStage struct {
    name      string
    handler   stage.Handler
    stage     queue.Stage      // stage value this handler processes
    semaphore Semaphore        // which resource semaphore to acquire
}
```

The `nextStage` for each entry is derived from the next element in the slice
(last entry advances to `completed`). Stage ordering is implicit in slice
position.

**pipelineState** -- runtime state:
```go
type pipelineState struct {
    stages     []pipelineStage
    stageOrder []queue.Stage             // ordered for NextReady()
    stageMap   map[queue.Stage]int       // stage -> index in stages slice
    sems       [3]chan struct{}          // disc, encode, transcription (capacity 1 each)
    logger     *slog.Logger
}
```

Pipeline finalization builds `stageMap` from the stages slice, derives
`stageOrder` (disc-semaphore stages first), and initializes semaphore channels.

### 4.5 Stage Handler Interface

Every pipeline stage implements:

```go
type Handler interface {
    Run(ctx context.Context, item *queue.Item) error
}
```

Single method, no health checks. Preflight dependency checks (binary existence
via `exec.LookPath`) run once at daemon startup (see DESIGN_INFRASTRUCTURE.md
Section 3). Deeper connectivity checks (TMDB, Jellyfin, OpenSubtitles) are
standalone functions used by `spindle status` and `/api/status` -- they do
not gate stage execution because temporary service unavailability is
better handled by failing the item and retrying via `spindle queue retry`
than by a pre-check that may itself be stale.

**Per-item logging**: The pipeline manager attaches an `item_id` attribute to
the logger before calling `Run`. Handlers retrieve it via
`stage.LoggerFromContext(ctx)`, which falls back to `slog.Default()` if
absent. All log lines for an item share the same `item_id` field, enabling
filtering from the single daemon log.

### 4.6 Stage Execution Lifecycle

The manager's `processItem()` drives daemon-mode execution. The standalone
`stageexec.Run()` provides a similar path for CLI one-shot workflows.

**Lifecycle per item** (5 steps, 2 persistence points):

1. Look up stage handler by `item.Stage` in `pipeline.stageMap`.
   Acquire required semaphore for this stage (blocks until available).
   Create request UUID and stage context (child of daemon context).
   Attach per-item `*slog.Logger` to the context.
2. Initialize progress state (`ProgressStage`, default message, percent 0).
   **Set `in_progress = 1`**, persist.
3. Call `handler.Run(ctx, item)`.
4. **Handle error**: if `context.Canceled`, set `in_progress = 0`, persist,
   release semaphore, return. Otherwise call `handleStageFailure()`.
5. Advance `item.Stage` to next stage. Set `in_progress = 0`.
   If completed: finalize progress (ensure percent >= 100, non-empty message).
   **Persist** final state. Release semaphore.

Stage-specific lifecycle concerns (e.g., the ripping handler pausing disc
monitoring) are owned by the handler, not the generic lifecycle. See
DESIGN_STAGES.md Section 2.6.

**Failure handling** (`handleStageFailure`):
- Checks error type: `services.ErrDegraded` is logged as a warning and
  processing continues. All other errors fail the item.
- See DESIGN_INFRASTRUCTURE.md Section 5 for the error taxonomy.
- On failure: sets `item.Stage = failed`, `item.InProgress = 0`.
- Records `failed_at_stage` (the stage where failure occurred) for retry routing.
- Persists, notifies, and checks queue completion.
- Retry is workflow-level: `spindle queue retry <id>` re-runs from the
  failed stage.

**DB write failure during stage execution**: If a persistence step (2, 5)
fails, the item is marked as failed with the DB error. The in-memory state
may diverge from the persisted state, but startup recovery (Section 4.7)
handles this by resetting all `in_progress` flags. The item will be
re-executed from the beginning of its current stage on next startup.

### 4.6.1 Stage Cancellation Contract

When the daemon shuts down or an item is stopped, the stage context is
cancelled. All stage handlers must observe `ctx.Done()` and comply with
these rules:

**General rules (all stages):**
1. External processes must be started with `exec.CommandContext(ctx, ...)`
   so they are killed on cancellation.
2. Handlers should return promptly after observing cancellation. Return
   `ctx.Err()` (which will be `context.Canceled`).
3. Partial work in the staging directory is left as-is. The stage will
   re-execute from the beginning on retry; resume-capable stages
   (encoding, subtitles) skip already-completed episodes.
4. Resource semaphores are released by the manager, not the handler.

**Per-stage cancellation behavior:**

| Stage | On Cancellation |
|-------|-----------------|
| Identification | MakeMKV scan killed. No cleanup needed. |
| Ripping | MakeMKV rip killed. Partial files left in staging (overwritten on retry). |
| Episode ID | transcription runtime killed. Partial transcripts left (reusable on retry). |
| Encoding | Drapto/FFmpeg killed. Partial output left. Resume skips completed episodes. |
| Audio Analysis | FFmpeg/transcription runtime killed. No persistent side effects. |
| Subtitling | transcription runtime killed. Partial SRTs left. Resume skips completed episodes. |
| Organization | **Must clean up partial copies.** Incomplete files in the library are dangerous. On cancellation, remove any partially-written target file before returning. |

### 4.7 Crash Recovery

Since Spindle is a single binary, a process crash kills all goroutines. On
startup, the daemon scans for items with `in_progress = 1` and resets them
to `in_progress = 0`. These items are then picked up by the normal pipeline
poll loop and re-executed from the beginning of their current stage.

See DESIGN_QUEUE.md Section 5 for details.

---

## 5. Configuration

### 5.1 Format

TOML format. Parsed by `go-toml/v2`.

### 5.2 Load Order

1. If `--config` flag specified: use that path.
2. Otherwise, check `~/.config/spindle/config.toml`.
3. Otherwise, check `./spindle.toml` (project-local).
4. If no file exists: use all defaults (no error).

After parsing: normalize (expand paths, apply env vars, set defaults), then validate.

### 5.3 Path Expansion

All path fields support `~` expansion to user home directory. Paths are cleaned
and converted to absolute paths.

### 5.4 Environment Variable Overrides

**Secrets** (override config file values):

| Env Variable               | Config Field                        |
|----------------------------|-------------------------------------|
| `TMDB_API_KEY`             | `tmdb.api_key`                      |
| `JELLYFIN_API_KEY`         | `jellyfin.api_key`                  |
| `OPENROUTER_API_KEY`       | `llm.api_key`                       |
| `SPINDLE_API_TOKEN`        | `api.token`                         |
| `OPENSUBTITLES_API_KEY`    | `subtitles.opensubtitles_api_key`   |
| `OPENSUBTITLES_USER_TOKEN` | `subtitles.opensubtitles_user_token`|

**Binary path overrides** (checked in order; first non-empty wins):

| Env Variable             | Fallback Env     | Default            |
|--------------------------|------------------|--------------------|
| `SPINDLE_FFMPEG_PATH`    | `FFMPEG_PATH`    | PATH lookup, then `ffmpeg`  |
| `SPINDLE_FFPROBE_PATH`   | `FFPROBE_PATH`   | PATH lookup, then `ffprobe` |

See Section 4.8 of DESIGN_INFRASTRUCTURE.md for the full resolution logic.

### 5.5 Directory Provisioning

On config load, `EnsureDirectories` creates:
- `staging_dir` (required, fail on error)
- `state_dir` (required, fail on error)
- `review_dir` (required, fail on error)
- `library_dir` (best-effort, don't fail -- storage may be offline)
- Auto-derived cache directories as needed (rip cache, OpenSubtitles cache,
  transcription cache)

### 5.6 Configuration Sections

#### `[paths]`

| Field                    | Type   | Default                                      | Purpose                                    |
|--------------------------|--------|----------------------------------------------|--------------------------------------------|
| `staging_dir`            | string | `~/.local/share/spindle/staging`             | Working directory for in-progress items (under `$XDG_DATA_HOME`, not cache, because staging files are large and long-lived; tmpwatch-style cleanup would be destructive) |
| `library_dir`            | string | `~/library`                                  | Root of Jellyfin media library             |
| `state_dir`              | string | `~/.local/state/spindle`                     | Daemon logs and queue DB                   |
| `review_dir`             | string | `~/review`                                   | Unidentified files routed for manual review|

**Auto-derived state paths** (not configurable):
- Queue database: `{state_dir}/queue.db` via `QueueDBPath()`
- Daemon log: `{state_dir}/daemon.log` via `DaemonLogPath()`

**Auto-derived cache directories** (not configurable, all under `$XDG_CACHE_HOME/spindle/`):
- OpenSubtitles cache: `$XDG_CACHE_HOME/spindle/opensubtitles`
- transcription cache: `$XDG_CACHE_HOME/spindle/transcription`
- Rip cache: `$XDG_CACHE_HOME/spindle/rips` (when `rip_cache.enabled`)
- Disc ID cache: `$XDG_CACHE_HOME/spindle/discid_cache.json` (when `disc_id_cache.enabled`)

#### `[api]`

| Field                    | Type   | Default                                      | Purpose                                    |
|--------------------------|--------|----------------------------------------------|--------------------------------------------|
| `bind`                   | string | (empty)                                      | Optional TCP listen address for HTTP API   |
| `token`                  | string | (empty)                                      | Bearer token for HTTP API auth             |

#### `[tmdb]`

| Field      | Type   | Default                          | Purpose                  |
|------------|--------|----------------------------------|--------------------------|
| `api_key`  | string | (required)                       | TMDB API bearer token    |
| `base_url` | string | `https://api.themoviedb.org/3`   | TMDB API base URL        |
| `language` | string | `en-US`                          | TMDB query language      |

#### `[jellyfin]`

| Field     | Type   | Default | Purpose                             |
|-----------|--------|---------|-------------------------------------|
| `enabled` | bool   | false   | Enable Jellyfin library refresh     |
| `url`     | string | (empty) | Jellyfin server URL                 |
| `api_key` | string | (empty) | Jellyfin API key                    |

#### `[library]`

| Field               | Type   | Default  | Purpose                            |
|---------------------|--------|----------|------------------------------------|
| `movies_dir`        | string | `movies` | Subdirectory under library_dir     |
| `tv_dir`            | string | `tv`     | Subdirectory under library_dir     |
| `overwrite_existing`| bool   | false    | Overwrite files already in library |

#### `[notifications]`

| Field                 | Type   | Default | Purpose                                     |
|-----------------------|--------|---------|---------------------------------------------|
| `ntfy_topic`          | string | (empty) | ntfy topic URL (empty disables all notifications) |
| `request_timeout`     | int    | 10      | HTTP timeout in seconds                     |

When `ntfy_topic` is set, Spindle sends a compact set of milestone and outcome
notifications: item queued, identification complete, rip cache hit, rip
complete, encode complete, final clean success, final review-required outcome,
queue backlog start/finish, fatal errors, and test sends. Queue start/finish
notifications are backlog-cycle events: they only fire for meaningful queue
activity and are sent as a matched pair.

#### `[subtitles]`

| Field                      | Type     | Default                 | Purpose                                |
|----------------------------|----------|-------------------------|----------------------------------------|
| `enabled`                  | bool     | false                            | Enable subtitle generation pipeline    |
| `mux_into_mkv`             | bool     | true                             | Embed subtitles in MKV container       |
| `transcription_model`      | string   | `nvidia/parakeet-tdt-0.6b-v2`    | Parakeet model name                    |
| `transcription_device`     | string   | `auto`                           | Runtime device: `auto`, `cuda`, `cpu`  |
| `transcription_precision`  | string   | `bf16`                           | Runtime precision: `bf16` is usually faster; `fp32` still uses the GPU but favors reliability over speed |
| `opensubtitles_enabled`    | bool     | false                            | Enable OpenSubtitles integration       |
| `opensubtitles_api_key`    | string   | (empty)                          | OpenSubtitles API key                  |
| `opensubtitles_user_agent` | string   | `Spindle/dev v0.1.0`             | User-Agent for OpenSubtitles requests  |
| `opensubtitles_user_token` | string   | (empty)                          | OpenSubtitles user token for downloads |
| `opensubtitles_languages`  | []string | `["en"]`                         | Preferred subtitle languages           |

#### `[rip_cache]`

| Field     | Type   | Default | Purpose                        |
|-----------|--------|---------|--------------------------------|
| `enabled` | bool   | false   | Enable rip cache               |
| `max_gib` | int    | 150     | Maximum cache size in GiB      |

Directory auto-derived: `$XDG_CACHE_HOME/spindle/rips` (see `[paths]`).

#### `[disc_id_cache]`

| Field     | Type   | Default | Purpose                              |
|-----------|--------|---------|--------------------------------------|
| `enabled` | bool   | false   | Enable disc ID -> TMDB ID cache      |

Path auto-derived: `$XDG_CACHE_HOME/spindle/discid_cache.json` (see `[paths]`).

#### `[makemkv]`

| Field                  | Type   | Default                                               | Purpose                                  |
|------------------------|--------|-------------------------------------------------------|------------------------------------------|
| `optical_drive`        | string | `/dev/sr0`                                            | Optical drive device path                |
| `rip_timeout`          | int    | 14400                                                 | Rip timeout in seconds (4 hours)         |
| `info_timeout`         | int    | 600                                                   | Disc info scan timeout (10 minutes)      |
| `disc_settle_delay`    | int    | 10                                                    | Seconds between disc access commands     |
| `min_title_length`     | int    | 120                                                   | Skip titles shorter than this (seconds)  |
| `keydb_path`           | string | `~/.config/spindle/keydb/KEYDB.cfg`                   | Local KeyDB file path                    |
| `keydb_download_url`   | string | `http://fvonline-db.bplaced.net/export/keydb_eng.zip` | KeyDB download URL                       |
| `keydb_download_timeout`| int   | 300                                                   | Download timeout in seconds              |

#### `[encoding]`

| Field           | Type | Default      | Purpose                                       |
|-----------------|------|--------------|-----------------------------------------------|
| `svt_av1_preset`| int  | 6            | SVT-AV1 preset (0-13)                         |
| `crf_sd`        | int  | (drapto: 24) | CRF for SD (<1920 width); 0 = drapto default  |
| `crf_hd`        | int  | (drapto: 26) | CRF for HD (>=1920, <3840); 0 = drapto default|
| `crf_uhd`       | int  | (drapto: 26) | CRF for UHD (>=3840); 0 = drapto default      |

Encoding parameters are re-read from disk before each encode, so changes
take effect without restarting the daemon. If the reload fails (file
deleted, parse error, invalid values), the existing config is used with a
warning log.

#### `[llm]`

| Field            | Type   | Default                                         | Purpose                       |
|------------------|--------|-------------------------------------------------|-------------------------------|
| `api_key`        | string | (empty)                                         | OpenRouter API key            |
| `base_url`       | string | `https://openrouter.ai/api/v1/chat/completions`| Chat completions endpoint     |
| `model`          | string | `google/gemini-3-flash-preview`                 | LLM model identifier          |
| `referer`        | string | `https://github.com/five82/spindle`             | HTTP-Referer header           |
| `title`          | string | `Spindle`                                       | X-Title header                |
| `timeout_seconds`| int    | 60                                              | Request timeout               |

#### `[commentary]`

| Field                  | Type    | Default            | Purpose                                      |
|------------------------|---------|--------------------|----------------------------------------------|
| `enabled`              | bool    | false          | Enable commentary track detection                     |
| `transcription_model`  | string  | (empty)        | Optional transcription model override                 |
| `similarity_threshold` | float64 | 0.92           | Cosine similarity for stereo downmix check            |
| `confidence_threshold` | float64 | 0.80           | LLM confidence required for classification            |

Commentary LLM classification uses the `[llm]` settings directly. All three
`[llm]` fields (`api_key`, `base_url`, `model`) must be set for commentary
classification to be available.

#### `[logging]`

| Field             | Type              | Default   | Purpose                               |
|-------------------|-------------------|-----------|---------------------------------------|
| `retention_days`  | int               | 60        | Days to retain daemon log files       |

### 5.7 Hardcoded Constants (Not Configurable)

The following values are code constants, not config fields. They were previously
exposed as config but never changed from defaults in practice.

**Content ID thresholds** (see `CONTENT_ID_DESIGN.md`):
- `minSimilarityScore = 0.58` -- minimum cosine similarity to accept a match
- `lowConfidenceReviewThreshold = 0.70` -- below this, flag for review
- `llmVerifyThreshold = 0.85` -- above this, skip LLM verification
- `disc1MustStartAtEpisode1 = true`

**Workflow timing:**
- Queue poll interval: 5 seconds
- Error retry interval: 10 seconds
- Disc monitor timeout: 5 seconds

**Validation:**
- Drapto validation enforcement: always on (fail encoding on validation failure)
- TMDB exact match minimum vote count: 5

---

## 6. File System Layout

### 6.1 Staging Directory

```
{staging_dir}/
  {fingerprint}/
    ripped/           # MakeMKV output
      title_00.mkv
    encoded/          # Drapto output + subtitle sidecars
      title_00.mkv    # or episode files
      title_00.en.srt
      title_00.en.forced.srt   # only when forced subs found
    contentid/        # transcription artifacts for episode ID
      s01_001/
        s01_001-contentid.srt
```

### 6.2 Library Directory

```
{library_dir}/
  {movies_dir}/
    Movie Title (2024)/
      Movie Title (2024).mkv
      Movie Title (2024).en.srt
  {tv_dir}/
    Show Name/
      Season 01/
        Show Name - S01E01 - Episode Title.mkv
        Show Name - S01E01 - Episode Title.en.srt
```

### 6.3 Cache Directories

All cache paths are auto-derived (see Section 5.6 `[paths]`).

```
$XDG_CACHE_HOME/spindle/
  rips/                   # Rip cache (when enabled)
    {fingerprint}/
      title_00.mkv
      ...
  discid_cache.json       # Disc ID cache (when enabled)
  opensubtitles/          # OpenSubtitles download cache
    {tmdb_id}/
      {season}/
        {episode}_{language}_{file_id}.srt
  transcription/          # Canonical transcription cache + runtime state
    {cache_key}/
      audio.srt
      audio.json
    runtime/
      parakeet/
        .venv/
        parakeet_transcribe.py
  huggingface/            # Model/download cache used by transcription runtime
    ...
```

### 6.4 State Directory

```
{state_dir}/
  spindle.log           # Main daemon log (symlink to current)
  queue.db              # SQLite database

$XDG_RUNTIME_DIR/
  spindle.sock          # HTTP API Unix socket
  spindle.lock          # Daemon lock file
```

### 6.5 Review Directory

```
{review_dir}/
  {sanitized_review_reason}_{8-char_fingerprint_hex}/
    encoded_file.mkv
```

### 6.6 Configuration

```
~/.config/spindle/
  config.toml           # Main configuration
  keydb/
    KEYDB.cfg           # KeyDB catalog
```
