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
  TUI that connects to Spindle's HTTP API to display queue status and encoding
  progress via SSE. Flyer is a separate project and not part of Spindle itself, but it
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
| `whisperxSem` | WhisperX GPU/model (shared transcription service) | Episode Identification, Audio Analysis (commentary), Subtitling |

Each semaphore prevents concurrent use of a constrained resource. A stage
acquires its required semaphore before execution and releases it on
completion, cancellation, or failure. Stages that do not require a semaphore
(Organization) run freely.

**Pipeline poll loop** (`run()`):
1. Check `ctx.Done()` for shutdown.
2. Fetch next ready item via `NextReady()` where `in_progress = 0`, ordered
   by stage priority (earlier stages first) then creation time (FIFO within
   same stage).
3. If no item: wait `queue_poll_interval` seconds, loop.
4. If queue fetch error: log, wait `error_retry_interval` seconds, loop.
5. Spawn goroutine: acquire required semaphore(s), run preflight checks for
   the stage, set `in_progress = 1`, process item via `processItem()`,
   advance stage, release semaphore(s).
6. Loop (immediately look for next item; don't wait for spawned goroutine).

**Preflight per item**: Dependency checks run after semaphore acquisition and
before stage execution. This catches missing binaries or unavailable services
before committing to work. Preflight failure marks the item as failed (not
the entire pipeline), allowing other items at different stages to continue.

**Stage priority** ensures disc-dependent stages run first (freeing the drive
for the next disc), while encoding and later stages run concurrently in the
background.

**Per-item logging**: Items past the identification stage get dedicated log
files at `{log_dir}/items/{item_id}/{session_id}.log`.

### 4.4 Stage Configuration Wiring

`ConfigureStages(set StageSet)` registers up to 7 optional stage handlers and
builds the pipeline stage chain.

**StageSet** (7 optional handlers):
```go
type StageSet struct {
    Identifier        stage.Handler
    Ripper            stage.Handler
    EpisodeIdentifier stage.Handler
    Encoder           stage.Handler
    AudioAnalysis     stage.Handler
    Subtitles         stage.Handler
    Organizer         stage.Handler
}
```

**Conditional stage ordering** -- the stage chain is built dynamically:
- If `EpisodeIdentifier` present: encoding follows `episode_identification`;
  absent: encoding follows `ripping` directly.
- If `AudioAnalysis` present: subtitling follows `audio_analysis`;
  absent: subtitling follows `encoding`.
- If `Subtitles` present: organizing follows `subtitling`;
  absent: organizing follows whatever subtitling would have followed.

**pipelineState** -- runtime state:
```go
type pipelineState struct {
    stages       []pipelineStage
    stageOrder   []queue.Stage                // ordered for NextReady()
    stageByStart map[queue.Stage]pipelineStage
    discSem      chan struct{}                // capacity 1, guards optical drive
    encodeSem    chan struct{}                // capacity 1, guards SVT-AV1 encoder
    whisperxSem  chan struct{}                // capacity 1, guards WhisperX GPU
    logger       *slog.Logger
}
```

**pipelineStage** -- single stage descriptor:
```go
type pipelineStage struct {
    name        string
    handler     stage.Handler
    stage       queue.Stage        // stage value this handler processes
    nextStage   queue.Stage        // stage to advance to on success
    needsDisc   bool               // true for identification, ripping
    needsEncode bool               // true for encoding
    needsWhisperX bool             // true for episode_id, audio_analysis, subtitling
}
```

Pipeline finalization (`finalize()`) populates the `stageByStart` map, builds
the `stageOrder` slice (disc-dependent stages first), and initializes the disc
semaphore.

### 4.5 Stage Handler Interface

Every pipeline stage implements:

```go
type Handler interface {
    Prepare(ctx context.Context, item *queue.Item) error
    Execute(ctx context.Context, item *queue.Item) error
}
```

Two methods, no health checks. Preflight dependency checks (binary existence
via `exec.LookPath`) run per-item before stage execution (see Section 4.6
step 4). Deeper connectivity checks (TMDB, Jellyfin, OpenSubtitles) are
standalone functions used by `spindle status` and `/api/status` -- they do
not gate stage execution because transient retry (Section 4.6 failure
handling) handles temporary service unavailability better than a pre-check.

**Per-item logging**: The pipeline manager attaches a per-item `*slog.Logger`
to the context before calling `Prepare` and `Execute`. Handlers retrieve it
via `stage.LoggerFromContext(ctx)`, which falls back to `slog.Default()` if
absent. This replaces mutable `SetLogger()` calls with immutable,
request-scoped context values.

### 4.6 Stage Execution Lifecycle

The manager's `processItem()` drives daemon-mode execution. The standalone
`stageexec.Run()` provides a similar path for CLI one-shot workflows.

**Full lifecycle per item** (14 steps with persistence points):

1. Look up stage handler by `item.Stage` in `pipeline.stageByStart`.
2. Acquire required semaphore(s) for this stage (blocks until available).
3. Create request UUID and stage context (child of daemon context).
   Attach per-item `*slog.Logger` to the context.
4. **Run preflight checks** for this stage's dependencies. On failure:
   mark item as failed with hint, release semaphore, return.
5. **Initialize progress state**: set `ProgressStage` via `deriveStageLabel()`
   (title-cases the stage name), set default message `"{label} started"`,
   reset `ProgressPercent` to 0.
6. **Set `in_progress = 1`**, persist.
7. Call `handler.Prepare(ctx, item)`.
8. **Persist** post-Prepare state changes.
9. If stage is "ripper" and rip hooks registered: call `BeforeRip()`.
10. Call `handler.Execute(ctx, item)` (blocking).
11. If stage is "ripper" and rip hooks registered: call `AfterRip()`.
12. **Handle execution error**: if `context.Canceled`, log DEBUG, set
    `in_progress = 0`, persist, release semaphore, and return. Otherwise call
    `handleStageFailure()`.
13. Advance `item.Stage` to next stage. Set `in_progress = 0`.
    If completed: finalize progress (ensure percent >= 100, non-empty message).
14. **Persist** final state. Release semaphore.

**Failure handling** (`handleStageFailure`):
- Classifies error via `services.Details(err)` which extracts structured
  `ErrorDetails` (Kind, Stage, Operation, Message, Code, Hint, Cause).
- See DESIGN_INFRASTRUCTURE.md Section 5 for the full error taxonomy.
- **Transient** errors are retried (up to 3 times with backoff) before failing.
- **Fatal** errors fail the item immediately.
- **Degraded** errors are logged as warnings; processing continues.
- On failure: sets `item.Stage = failed`, `item.InProgress = 0`.
- Records `failed_at_stage` for retry routing.
- Persists, notifies, and checks queue completion.

**DB write failure during stage execution**: If a persistence step (6, 8, 14)
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
| Episode ID | WhisperX killed. Partial transcripts left (reusable on retry). |
| Encoding | Drapto/FFmpeg killed. Partial output left. Resume skips completed episodes. |
| Audio Analysis | FFmpeg/WhisperX killed. No persistent side effects. |
| Subtitling | WhisperX killed. Partial SRTs left. Resume skips completed episodes. |
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

All secrets can be set via environment variables (overrides config file):

| Env Variable               | Config Field                        |
|----------------------------|-------------------------------------|
| `TMDB_API_KEY`             | `tmdb.api_key`                      |
| `JELLYFIN_API_KEY`         | `jellyfin.api_key`                  |
| `OPENROUTER_API_KEY`       | `llm.api_key`                       |
| `SPINDLE_API_TOKEN`        | `api.token`                         |
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
| `api_key`              | string  | (empty)            | LLM API key (falls back to `[llm].api_key`)  |
| `base_url`             | string  | (empty)            | LLM base URL (falls back to `[llm].base_url`)|
| `model`                | string  | (empty)            | LLM model (falls back to `[llm].model`)      |

**LLM fallback chain**: Each `[commentary]` LLM field is resolved independently.
If `commentary.api_key` is set but `commentary.model` is empty, the API key
comes from `[commentary]` and the model from `[llm]`. All three fields
(`api_key`, `base_url`, `model`) must resolve to non-empty values (from either
section) for commentary LLM classification to be available.

#### `[content_id]`

See `CONTENT_ID_DESIGN.md` for detailed semantics of each field.

| Field                              | Type    | Default | Purpose                                        |
|------------------------------------|---------|---------|------------------------------------------------|
| `min_similarity_score`             | float64 | 0.58    | Minimum cosine similarity to accept a match    |
| `low_confidence_review_threshold`  | float64 | 0.70    | Below this, flag for review                    |
| `llm_verify_threshold`             | float64 | 0.85    | Above this, skip LLM verification              |
| `disc_1_must_start_at_episode_1`   | bool    | true    | Disc 1 contiguous range must start at episode 1|

#### `[workflow]`

| Field                 | Type | Default | Purpose                                    |
|-----------------------|------|---------|--------------------------------------------|
| `queue_poll_interval` | int  | 5       | Seconds between queue polls                |
| `error_retry_interval`| int  | 10      | Seconds to wait after queue fetch error    |
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
  spindle.log           # Main daemon log (symlink to current)
  spindle.lock          # Lock file
  queue.db              # SQLite database
  items/
    {item_id}/
      {session_id}.log  # Per-item log file

$XDG_RUNTIME_DIR/
  spindle.sock          # HTTP API Unix socket
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
