# System Design: Overview

Introduction, external dependencies, services, architecture, configuration, and
filesystem layout for the Spindle system.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Introduction

### 1.1 Purpose

Spindle automates the complete workflow from optical disc to Jellyfin media library:
disc detection, ripping (MakeMKV), encoding (Drapto/SVT-AV1), metadata lookup (TMDB),
subtitle generation (WhisperX + OpenSubtitles forced subs), commentary detection,
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
  TUI that connects to Spindle's HTTP API to display queue status, encoding progress,
  and log streams. Flyer is a separate project and not part of Spindle itself, but it
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
| `uvx`      | WhisperX-driven transcription (Python runner)      | When subtitles.enabled = true  |
| `mkvmerge` | Muxing subtitles into MKV containers               | When subtitles.mux_into_mkv = true |

### 2.3 Library Dependencies

| Library | Purpose                              |
|---------|--------------------------------------|
| Drapto  | Go library for SVT-AV1 encoding     |
| SQLite  | Queue database (via go-sqlite3)      |

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

- **Purpose**: Edition detection, commentary classification, episode verification.
- **Base URL**: `https://openrouter.ai/api/v1/chat/completions` (configurable)
- **Default model**: `google/gemini-3-flash-preview`
- **Auth**: API key via `Authorization: Bearer` header.
- **Headers**: `HTTP-Referer`, `X-Title` for OpenRouter routing.
- **Fallback**: Commentary detection falls back to `[llm]` settings when
  `[commentary]` section doesn't specify its own API key/model.

### 3.6 KeyDB

- **Purpose**: Disc ID to title mapping database for Blu-ray identification.
- **Format**: Zip file containing `KEYDB.cfg` with disc ID entries.
- **Download URL**: `http://fvonline-db.bplaced.net/export/keydb_eng.zip` (configurable)
- **Local path**: `~/.config/spindle/keydb/KEYDB.cfg`

---

## 4. Architecture Overview

### 4.1 Binary Structure

Single binary `spindle` with two execution modes:

1. **Daemon mode** (`spindle daemon-run`): Starts the full daemon with disc
   monitoring, workflow manager, IPC server, and HTTP API server.
2. **CLI mode**: All other commands. Some require a running daemon (via IPC),
   others work standalone (direct DB access or local operations).

### 4.2 Communication Layers

```
                    +-----------+
                    | CLI User  |
                    +-----+-----+
                          |
              +-----------+-----------+
              |                       |
     +--------v--------+    +--------v--------+
     | IPC (Unix sock) |    | HTTP API (TCP)  |
     | JSON-RPC        |    | REST            |
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

**IPC**: Unix domain socket at `{log_dir}/spindle.sock`, JSON-RPC protocol,
service name "Spindle". Used by CLI for daemon control and queue management.

**HTTP API**: TCP listener at `api_bind` address (default `127.0.0.1:7487`).
REST endpoints for status, queue, and log streaming. Token auth via `api_token`.

**Queue access fallback**: When daemon is unavailable, CLI queue commands open
the SQLite database directly for read operations.

> **Rewrite improvement**: Unify IPC + HTTP into a single HTTP API served over
> Unix socket (with optional TCP bind). The CLI would use the same HTTP endpoints
> as external consumers. See `API_INTERFACES.md` for details.

### 4.3 Concurrency Model: Foreground/Background Lanes

The workflow manager runs two parallel processing lanes:

**Foreground lane** (frees the disc drive quickly):
1. **Identification** (pending -> identifying -> identified)
2. **Ripping** (identified -> ripping -> ripped)

**Background lane** (runs while next disc can start):
3. **Episode Identification** (ripped -> episode_identifying -> episode_identified) [TV only]
4. **Encoding** (episode_identified/ripped -> encoding -> encoded)
5. **Audio Analysis** (encoded -> audio_analyzing -> audio_analyzed)
6. **Subtitle Generation** (audio_analyzed -> subtitling -> subtitled)
7. **Organization** (subtitled -> organizing -> completed)

Each lane runs independently. A disc can be ejected and a new one inserted as soon
as ripping completes; the background lane continues processing the first disc's
encoded files while the foreground lane identifies and rips the new disc.

**Lane poll loop** (`runLane()`):
1. Check `ctx.Done()` for shutdown.
2. Run reclaimer (if lane has processing statuses).
3. Fetch next item via `NextForStatuses()` (FIFO by creation time).
4. If no item: wait `queue_poll_interval` seconds, loop.
5. If queue fetch error: log, wait `error_retry_interval` seconds, loop.
6. Run preflight dependency checks. **Failure halts the lane** (requires daemon
   restart) to prevent repeated failures against missing binaries.
7. Process item via `processItem()`.
8. Loop.

Foreground lane enables notifications; background lane does not.

### 4.4 Stage Configuration Wiring

`ConfigureStages(set StageSet)` registers up to 7 optional stage handlers and
builds the two processing lanes.

**StageSet** (7 optional handlers):
```go
type StageSet struct {
    Identifier        stage.Handler
    Ripper            stage.Handler
    AudioAnalysis     stage.Handler
    EpisodeIdentifier stage.Handler
    Encoder           stage.Handler
    Subtitles         stage.Handler
    Organizer         stage.Handler
}
```

**Conditional stage ordering** -- background lane start statuses chain dynamically:
- If `EpisodeIdentifier` present: encoder starts at `episode_identified`;
  absent: encoder starts at `ripped`.
- If `AudioAnalysis` present: subtitles starts at `audio_analyzed`;
  absent: subtitles starts at `encoded`.
- If `Subtitles` present: organizer starts at `subtitled`;
  absent: organizer starts at whatever subtitles would have started at.

**laneState** -- per-lane runtime state:
```go
type laneState struct {
    kind                 laneKind                     // "foreground" or "background"
    name                 string
    stages               []pipelineStage
    statusOrder          []queue.Status               // ordered for NextForStatuses()
    stageByStart         map[queue.Status]pipelineStage
    processingStatuses   []queue.Status               // statuses needing heartbeat
    logger               *slog.Logger
    notificationsEnabled bool                         // foreground=true, background=false
    runReclaimer         bool                         // true if any processing statuses
}
```

**pipelineStage** -- single stage descriptor:
```go
type pipelineStage struct {
    name             string
    handler          stage.Handler
    startStatus      queue.Status
    processingStatus queue.Status
    doneStatus       queue.Status
}
```

Lane finalization (`finalize()`) populates the `stageByStart` map, builds the
`statusOrder` slice, deduplicates `processingStatuses`, and sets
`runReclaimer = true` if the lane has any processing statuses.

### 4.5 Stage Handler Interface

Every pipeline stage implements:

```go
type Handler interface {
    Prepare(ctx context.Context, item *queue.Item) error
    Execute(ctx context.Context, item *queue.Item) error
    HealthCheck(ctx context.Context) Health
}
```

Optional interface for per-item logging:

```go
type LoggerAware interface {
    SetLogger(logger *slog.Logger)
}
```

### 4.6 Stage Execution Lifecycle

The manager's `processItem()` drives daemon-mode execution. The standalone
`stageexec.Run()` provides a similar path for CLI one-shot workflows.

**Full lifecycle per item** (15 steps with persistence points):

1. Look up stage by `item.Status` in `lane.stageByStart`.
2. Create request UUID and stage context.
3. If handler implements `LoggerAware`, call `SetLogger()`.
4. **Initialize progress state**: set `ProgressStage` via `deriveStageLabel()`
   (splits status on underscores, title-cases each word), set default message
   `"{label} started"`, reset `ProgressPercent` to 0, set `LastHeartbeat` to now.
5. **Persist** transition to processing status.
6. Call `handler.Prepare(ctx, item)`.
7. **Persist** post-Prepare state changes.
8. If stage is "ripper" and rip hooks registered: call `BeforeRip()`.
9. **Execute with heartbeat**: spawn heartbeat goroutine via
   `executeWithHeartbeat()`, then call `handler.Execute(ctx, item)` (blocking).
10. Cancel heartbeat context; wait for heartbeat goroutine to finish.
11. If stage is "ripper" and rip hooks registered: call `AfterRip()`.
12. **Handle execution error**: if `context.Canceled`, log DEBUG and return.
    Otherwise call `handleStageFailure()`.
13. Advance `item.Status` to `stage.doneStatus`. Clear `LastHeartbeat`.
14. If completed: finalize progress (ensure percent >= 100, non-empty message).
15. **Persist** final state.

**Failure handling** (`handleStageFailure`):
- Classifies error via `services.Details(err)` which extracts structured
  `ErrorDetails` (Kind, Stage, Operation, Message, Code, Hint, Cause).
- ErrorKind values: `external`, `validation`, `configuration`, `not_found`,
  `timeout`, `transient`.
- Sets `item.Status = failed` with extracted message.
- Persists, notifies, and checks queue completion.

### 4.7 Heartbeat Concurrency

`executeWithHeartbeat()` creates a child context, spawns a heartbeat goroutine,
runs the handler's `Execute()`, then cancels the heartbeat context and waits for
the goroutine to finish.

- Heartbeat updates `last_heartbeat` every `heartbeat_interval` seconds.
- Missed heartbeat updates do not abort the stage -- they only make the item
  eligible for reclamation if the stage crashes.
- Progress is sampled during heartbeat ticks (suppressed when unchanged).

**Stale item reclamation** (`ReclaimStaleItems`):
- Runs at the start of each lane iteration, before fetching the next item.
- Cutoff: `time.Now().Add(-heartbeatTimeout)`.
- Items with `last_heartbeat` older than cutoff are reset to their start status
  per the rollback table (see DESIGN_QUEUE.md Section 1.5).
- Only runs for lanes with processing statuses (`runReclaimer = true`).

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

All secrets can be set via environment variables (overrides config file):

| Env Variable               | Config Field                        |
|----------------------------|-------------------------------------|
| `TMDB_API_KEY`             | `tmdb.api_key`                      |
| `JELLYFIN_API_KEY`         | `jellyfin.api_key`                  |
| `OPENROUTER_API_KEY`       | `llm.api_key`                       |
| `SPINDLE_API_TOKEN`        | `paths.api_token`                   |
| `HUGGING_FACE_HUB_TOKEN`  | `subtitles.whisperx_hf_token`       |
| `HF_TOKEN`                 | `subtitles.whisperx_hf_token` (alternative) |
| `OPENSUBTITLES_API_KEY`    | `subtitles.opensubtitles_api_key`   |
| `OPENSUBTITLES_USER_TOKEN` | `subtitles.opensubtitles_user_token`|

### 5.5 Directory Provisioning

On config load, `EnsureDirectories` creates:
- `staging_dir` (required, fail on error)
- `log_dir` (required, fail on error)
- `review_dir` (required, fail on error)
- `library_dir` (best-effort, don't fail -- storage may be offline)
- `rip_cache.dir` (if cache enabled, fail on error)

### 5.6 Configuration Sections

#### `[paths]`

| Field                    | Type   | Default                                      | Purpose                                    |
|--------------------------|--------|----------------------------------------------|--------------------------------------------|
| `staging_dir`            | string | `~/.local/share/spindle/staging`             | Working directory for in-progress items    |
| `library_dir`            | string | `~/library`                                  | Root of Jellyfin media library             |
| `log_dir`                | string | `~/.local/share/spindle/logs`                | Daemon logs, queue DB, lock file, socket   |
| `review_dir`             | string | `~/review`                                   | Unidentified files routed for manual review|
| `opensubtitles_cache_dir`| string | `~/.local/share/spindle/cache/opensubtitles` | OpenSubtitles download cache               |
| `whisperx_cache_dir`     | string | `~/.local/share/spindle/cache/whisperx`      | WhisperX transcription cache               |
| `api_bind`               | string | `127.0.0.1:7487`                             | HTTP API listen address                    |
| `api_token`              | string | (empty)                                      | Bearer token for HTTP API auth             |

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
| `ntfy_topic`          | string | (empty) | ntfy topic URL (empty disables)             |
| `request_timeout`     | int    | 10      | HTTP timeout in seconds                     |
| `identification`      | bool   | true    | Notify on disc detection + identification   |
| `rip`                 | bool   | true    | Notify on rip completion                    |
| `encoding`            | bool   | true    | Notify on encoding completion               |
| `validation`          | bool   | true    | Notify on validation failure                |
| `organization`        | bool   | true    | Notify on library organization              |
| `queue`               | bool   | true    | Notify on queue start/complete              |
| `review`              | bool   | true    | Notify on items needing review              |
| `errors`              | bool   | true    | Notify on pipeline errors                   |
| `min_rip_seconds`     | int    | 120     | Suppress rip notifications shorter than this|
| `queue_min_items`     | int    | 2       | Suppress queue notifications below count    |
| `dedup_window_seconds`| int    | 600     | Deduplication window (10 minutes)           |

#### `[subtitles]`

| Field                      | Type     | Default                 | Purpose                                |
|----------------------------|----------|-------------------------|----------------------------------------|
| `enabled`                  | bool     | false                   | Enable subtitle generation pipeline    |
| `mux_into_mkv`             | bool     | true                    | Embed subtitles in MKV container       |
| `whisperx_model`           | string   | `large-v3`              | WhisperX model name                    |
| `whisperx_cuda_enabled`    | bool     | false                   | Enable CUDA acceleration               |
| `whisperx_vad_method`      | string   | `silero`                | Voice activity detection method        |
| `whisperx_hf_token`        | string   | (empty)                 | HuggingFace access token               |
| `opensubtitles_enabled`    | bool     | false                   | Enable OpenSubtitles integration       |
| `opensubtitles_api_key`    | string   | (empty)                 | OpenSubtitles API key                  |
| `opensubtitles_user_agent` | string   | `Spindle/dev`           | User-Agent for OpenSubtitles requests  |
| `opensubtitles_user_token` | string   | (empty)                 | OpenSubtitles user token for downloads |
| `opensubtitles_languages`  | []string | `["en"]`                | Preferred subtitle languages           |

#### `[rip_cache]`

| Field     | Type   | Default                         | Purpose                        |
|-----------|--------|---------------------------------|--------------------------------|
| `enabled` | bool   | false                           | Enable rip cache               |
| `dir`     | string | `$XDG_CACHE_HOME/spindle/rips`  | Cache directory path           |
| `max_gib` | int    | 150                             | Maximum cache size in GiB      |

#### `[disc_id_cache]`

| Field     | Type   | Default                              | Purpose                              |
|-----------|--------|--------------------------------------|--------------------------------------|
| `enabled` | bool   | false                                | Enable disc ID -> TMDB ID cache      |
| `path`    | string | `~/.cache/spindle/discid_cache.json` | JSON cache file path                 |

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

| Field           | Type | Default | Purpose                       |
|-----------------|------|---------|-------------------------------|
| `svt_av1_preset`| int  | 6       | SVT-AV1 preset (0-13)        |

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
| `enabled`              | bool    | false              | Enable commentary track detection            |
| `whisperx_model`       | string  | `large-v3-turbo`   | WhisperX model (falls back to subtitles model)|
| `similarity_threshold` | float64 | 0.92               | Cosine similarity for stereo downmix check   |
| `confidence_threshold` | float64 | 0.80               | LLM confidence required for classification   |
| `api_key`              | string  | (empty)            | LLM API key (falls back to [llm])            |
| `base_url`             | string  | (empty)            | LLM base URL (falls back to [llm])           |
| `model`                | string  | (empty)            | LLM model (falls back to [llm])              |

#### `[content_id]`

See `CONTENT_ID_DESIGN.md` for detailed semantics of each field.

| Field                              | Type    | Default | Purpose                                        |
|------------------------------------|---------|---------|------------------------------------------------|
| `min_similarity_score`             | float64 | 0.58    | Minimum cosine similarity to accept a match    |
| `low_confidence_review_threshold`  | float64 | 0.70    | Below this, flag for review                    |
| `llm_verify_threshold`             | float64 | 0.85    | Above this, skip LLM verification              |
| `anchor_min_score`                 | float64 | 0.63    | Minimum score for anchor episode               |
| `anchor_min_score_margin`          | float64 | 0.03    | Minimum gap between best and second-best       |
| `block_high_confidence_delta`      | float64 | 0.05    | High-confidence threshold offset from max      |
| `block_high_confidence_top_ratio`  | float64 | 0.70    | Top percentage threshold for high-confidence   |
| `disc_block_padding_min`           | int     | 2       | Minimum padding for disc block strategy        |
| `disc_block_padding_divisor`       | int     | 4       | Padding divisor for disc block strategy        |
| `disc1_must_start_at_episode1`     | bool    | true    | Disc 1 always starts at episode 1             |
| `disc2_plus_min_start_episode`     | int     | 2       | Disc 2+ cannot start before this episode       |

#### `[workflow]`

| Field                 | Type | Default | Purpose                                    |
|-----------------------|------|---------|--------------------------------------------|
| `queue_poll_interval` | int  | 5       | Seconds between queue polls                |
| `error_retry_interval`| int  | 10      | Seconds to wait after queue fetch error    |
| `heartbeat_interval`  | int  | 15      | Seconds between heartbeat updates          |
| `heartbeat_timeout`   | int  | 120     | Seconds before item is considered stale    |
| `disc_monitor_timeout`| int  | 5       | Disc detection command timeout (seconds)   |

#### `[logging]`

| Field             | Type              | Default   | Purpose                               |
|-------------------|-------------------|-----------|---------------------------------------|
| `format`          | string            | `console` | Log format: "console" or "json"       |
| `level`           | string            | `info`    | Default log level                     |
| `retention_days`  | int               | 60        | Days to retain per-item log files     |
| `stage_overrides` | map[string]string | (empty)   | Per-stage log level overrides         |

#### `[validation]`

| Field                       | Type | Default | Purpose                                   |
|-----------------------------|------|---------|-------------------------------------------|
| `enforce_drapto_validation` | bool | true    | Fail encoding if Drapto validation fails  |
| `min_vote_count_exact_match`| int  | 5       | TMDB vote threshold for exact title match |

---

## 6. File System Layout

### 6.1 Staging Directory

```
{staging_dir}/
  queue-{id}/
    ripped/           # MakeMKV output
      title_00.mkv
    encoded/          # Drapto output
      title_00.mkv    # or episode files
    contentid/        # WhisperX transcripts for episode ID
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

```
{rip_cache_dir}/
  {fingerprint}/
    title_00.mkv
    ...

{opensubtitles_cache_dir}/
  {tmdb_id}/
    {season}/
      {episode}_{language}_{file_id}.srt

{whisperx_cache_dir}/
  ...

{disc_id_cache_path}   # Single JSON file
```

### 6.4 Log Directory

```
{log_dir}/
  spindle.log           # Main daemon log
  spindle.lock          # Lock file
  spindle.sock          # IPC Unix socket
  queue.db              # SQLite database
  items/
    {item_id}/
      {session_id}.log  # Per-item log file
```

### 6.5 Review Directory

```
{review_dir}/
  {disc_title}/
    encoded_file.mkv
```

### 6.6 Configuration

```
~/.config/spindle/
  config.toml           # Main configuration
  keydb/
    KEYDB.cfg           # KeyDB catalog
```
