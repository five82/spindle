# System Design: Infrastructure

Logging, notifications, preflight checks, shared utility libraries, log access,
audit gathering, and configuration validation.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Logging System

### 1.1 Handler Architecture

Two log handler modes:
- **Console**: Human-readable format with colored level indicators and bullet-point
  details. Used for CLI and daemon console output.
- **JSON**: Structured JSON lines. Used for machine-readable logging.

**Handler composition stack** (innermost to outermost):
1. `newJSONHandler` or `newPrettyHandler` (selected by config format)
2. `newStreamHandler` (wraps base, feeds StreamHub; skipped when no StreamHub)
3. `newLevelOverrideHandler` (per-group level overrides; skipped when override empty)
4. `newSessionIDHandler` (adds `session_id` to every record; skipped when ID empty)

Fanout handler tees records to multiple downstream handlers when needed.

### 1.2 Log Streaming: StreamHub / EventArchive

**StreamHub**: In-memory bounded ring buffer (default capacity: 512 events).
The daemon overrides this to 4096 (`daemonrun/run.go`).
- `Publish(event)`: Append event, broadcast to subscribers.
- `Fetch(since, limit, wait)`: Retrieve events with optional blocking for follow mode.
- `Tail(limit)`: Get most recent events without blocking.
- `AddSink(sink)`: Register external sink for persistence.

**EventArchive**: On-disk persistence of log events.
- Flushes events from StreamHub to JSONL file.
- Supports `ReadSince(sequence, limit)` for historical queries.
- When StreamHub buffer is exhausted, API falls back to archive.

### 1.3 Per-Item Logs

Background lane items get dedicated log files:
- Path: `{log_dir}/items/{item_id}/{session_id}.log`
- Contains all log output from that item's processing stages.
- Retention: `retention_days` (default 60 days).

### 1.4 Log Event Structure

```json
{
  "seq": 1234,
  "ts": "2024-01-15T10:30:00.000Z",
  "level": "INFO",
  "msg": "encoding stage started",
  "component": "encoder",
  "stage": "encoding",
  "item_id": 42,
  "lane": "background",
  "correlation_id": "req-abc123",
  "fields": {"event_type": "stage_start", "decision_type": "..."},
  "details": [{"label": "Input", "value": "/path/to/file.mkv"}]
}
```

### 1.5 Level Semantics

| Level | Use For                                                                    |
|-------|----------------------------------------------------------------------------|
| INFO  | All decisions affecting output: stage start/complete, track selection,     |
|       | preset choice, skip decisions, fallback logic, cache hits                  |
| DEBUG | Raw data dumps, metrics, internal state (not decisions)                    |
| WARN  | Degraded behavior (include `event_type`, `error_hint`, `impact`)          |
| ERROR | Operation failed (include `event_type`, `error_hint`, `error`)            |

### 1.6 Decision Logging

All decisions use structured attributes:
- `decision_type`: Category (e.g., `title_source`, `media_type_detection`)
- `decision_result`: Outcome (e.g., `updated`, `skipped`, `detected`)
- `decision_reason`: Why this outcome was chosen

### 1.7 Progress Format

`"Phase N/M - Action (context)"` -- e.g., `"Phase 2/3 - Ripping selected titles (5 of 12)"`

### 1.8 Stage Overrides

Per-stage log levels via `logging.stage_overrides` map:
```toml
[logging.stage_overrides]
encoder = "debug"
contentid = "debug"
```

### 1.9 Progress Sampling

Bucket-based progress suppression prevents log spam. State machine
(`ProgressSampler`):

**State:** `lastStage string`, `lastBucket int` (initialized to -1).

**Bucket calculation:** `bucket = int(percent / bucketSize)` with default
`bucketSize` of 5 (so buckets at 0%, 5%, 10%, ...). When `percent >= 100`,
the bucket is forced to `int(100 / bucketSize)` to guarantee a final emit.

**Emit rules** -- `ShouldLog` returns true when either condition is met:
1. Stage change: `stage != lastStage` (also resets `lastBucket` to -1).
2. Bucket crossing: `bucket > lastBucket`.

Negative percent values (unknown progress) skip bucket evaluation entirely.
`Reset()` clears both `lastStage` and `lastBucket` for new jobs.

### 1.10 Log Stream Filters

The log streaming API supports 3 additional filter types beyond those in
API_INTERFACES.md Section 3.4:
- `alert`: Filter by alert flag value.
- `decision_type`: Filter by decision type attribute.
- `search`: Substring search across message, component, stage, lane, correlation
  ID, fields, and details.

### 1.11 Retention

Log files older than `retention_days` are cleaned up. Per-item log directories
are deleted when all files within them exceed retention.

---

## 2. Notification System

### 2.1 ntfy Protocol

- HTTP POST to configured `ntfy_topic` URL.
- Headers: `Title`, `Priority`, `Tags`, `User-Agent: Spindle-Go/0.1.0`.
- Body: plain text message content.
- Timeout: `request_timeout` seconds (default **10 seconds**; fallback when <= 0).

**Config gate** -- 8 boolean flags control which event types generate
notifications: `identification`, `rip`, `encoding`, `validation`,
`organization`, `queue`, `review`, `errors`.

### 2.2 Event Types (13)

| Event                      | Config Gate           | Priority | Tags            | Suppression Rules                  |
|----------------------------|-----------------------|----------|-----------------|------------------------------------|
| `disc_detected`            | `identification`      | default  | -               | -                                  |
| `identification_completed` | `identification`      | default  | identify        | Skip if display title empty        |
| `rip_started`              | (never sent)          | -        | -               | Always suppressed                  |
| `rip_completed`            | `rip`                 | default  | rip             | Cache hit + duration < min_rip_seconds|
| `encoding_completed`       | `encoding`            | default  | encode          | Skip placeholder (no client)       |
| `validation_failed`        | `validation`          | high     | validation,warning | -                               |
| `processing_completed`     | (internal, always sent)| -       | -               | -                                  |
| `organization_completed`   | `organization`        | default  | organize        | -                                  |
| `queue_started`            | `queue`               | default  | queue           | count < queue_min_items            |
| `queue_completed`          | `queue`               | default  | queue           | processed+failed < queue_min_items |
| `error`                    | `errors`              | high     | error           | -                                  |
| `unidentified_media`       | `review`              | default  | review          | -                                  |
| `test`                     | (always)              | low      | test            | -                                  |

### 2.3 Deduplication

- Key: event type + first label field (discTitle/mediaTitle/title/filename/context).
- Window: `dedup_window_seconds` (default 600 = 10 minutes).
- Same key within window: suppressed.

### 2.4 Suppression Rules

- `rip_completed` with cache hit: suppress if rip duration < `min_rip_seconds`.
- `queue_started`/`queue_completed`: suppress if item count < `queue_min_items`.
- `encoding_completed`: suppress if placeholder (no Drapto client).

---

## 3. Preflight Checks

### 3.1 System Dependencies

Checked at daemon startup and via `spindle status`:

| Dependency | Command       | Optional | Condition                     |
|------------|---------------|----------|-------------------------------|
| MakeMKV    | `makemkvcon`  | No       | Always required               |
| FFmpeg     | `ffmpeg`      | No       | Always required               |
| FFprobe    | `ffprobe`     | No       | Always required               |
| MediaInfo  | `mediainfo`   | No       | Always required               |
| bd_info    | `bd_info`     | Yes      | Always optional               |
| uvx        | `uvx`         | No*      | When subtitles.enabled        |
| mkvmerge   | `mkvmerge`    | No*      | When subtitles.mux_into_mkv   |

*Conditionally required based on config.

### 3.2 Runtime Checks (via `spindle status`)

- **LLM connectivity**: Health check against configured LLM API (30s timeout).
- **Jellyfin connectivity**: Auth check via `/Users` endpoint.
- **OpenSubtitles connectivity**: Format info check via `/infos/formats`.
- **Directory access**: Verify read/write/execute on staging, library, review dirs.

### 3.3 Disc Probing

`ProbeDisc()` detects loaded discs via `lsblk` for the CLI `status` command:
- Default device: `/dev/sr0`.
- Timeout: **2 seconds**.
- Classifies disc type from FSTYPE: "udf" -> "Blu-ray", "iso9660" -> "DVD",
  else -> "Unknown".

---

## 4. Shared Utility Libraries

These packages provide foundational building blocks used across multiple stages.
A clean room rewrite must replicate their behavior.

### 4.1 Text Processing (`textutil`)

**TF-IDF fingerprinting** for content similarity (used by content ID matching,
commentary detection):

- `Tokenize(text)`: Split to lowercase tokens, filter tokens < 3 characters,
  split on `[^a-z0-9]+` regex.
- `NewFingerprint(text)`: Create term-frequency vector with L2 norm. Returns
  nil if no valid tokens.
- `Fingerprint.WithIDF(idf)`: Apply TF-IDF weights, returning a new
  fingerprint. Terms absent from IDF map retain original weight. Zero-weight
  terms are dropped. Returns nil if all terms are zeroed.
- `Corpus`: Tracks document frequency across fingerprints.
  - `Add(fp)`: Register unique terms (increments doc count).
  - `IDF()`: Compute weights as `log((N+1)/(1+df))` for each term.
- `CosineSimilarity(a, b)`: Dot product divided by product of L2 norms.
  Returns 0 if either fingerprint is nil or has zero norm.

**Filename sanitization:**

- `SanitizeFileName(name)`: Replace `/\:*` with dashes, remove `?"<>|`, trim.
- `SanitizeToken(value)`: Lowercase, keep `[a-z0-9_-]`, everything else becomes
  underscore. Returns "unknown" for empty input.

**Generic helper:**

- `Ternary[T](cond, a, b)`: Conditional expression (returns `a` if true, `b`
  if false).

### 4.2 File Operations (`fileutil`)

- `CopyFile(src, dst)`: Stream copy with `0o644` permissions.
- `CopyFileMode(src, dst, mode)`: Stream copy with custom permissions.
- `CopyFileVerified(src, dst)`: Stream copy with simultaneous SHA-256 hashing
  of both source and destination streams, plus size verification. Removes
  destination on mismatch. Used for media file integrity during organization.

### 4.3 Language Normalization (`language`)

Supports 18 languages: English, Spanish, French, German, Italian, Portuguese,
Japanese, Korean, Chinese, Russian, Arabic, Hindi, Dutch, Polish, Swedish,
Danish, Norwegian, Finnish.

Lookup indexes are built at init from code tables supporting ISO 639-1
(2-letter), ISO 639-2 primary (3-letter), alternate 3-letter codes (e.g.,
"fre"/"fra"), and full word forms (e.g., "english").

- `ToISO2(code)`: Convert any recognized code/word to 2-letter. Unknown
  2-letter codes pass through; everything else returns empty.
- `ToISO3(code)`: Convert to 3-letter. Unknown 2-letter codes return "und";
  unknown 3-letter codes pass through.
- `DisplayName(code)`: Human-readable name. Returns uppercased code if
  unrecognized.
- `ExtractFromTags(tags)`: Extract language from stream metadata tags. Checks
  keys in order: `language`, `LANGUAGE`, `Language`, `language_ietf`, `lang`,
  `LANG`. Strips null bytes.
- `NormalizeList(languages)`: Deduplicate, normalize to ISO 639-1. Codes > 2
  chars are converted via `ToISO2()`.

### 4.4 Media Inspection (`media/ffprobe`)

Wraps `ffprobe -v error -hide_banner -show_format -show_streams -of json`.

**Types:**

- `Result`: Parsed ffprobe output containing `Streams []Stream` and
  `Format Format`. Preserves raw JSON (`RawJSON()`).
- `Stream`: 16 fields -- `Index`, `CodecName`, `CodecType`, `CodecTag`,
  `CodecLong`, `Duration`, `BitRate`, `Width`, `Height`, `SampleRate`,
  `Channels`, `ChannelLayout`, `Profile`, `Tags map[string]string`,
  `Disposition map[string]int`.
- `Format`: 6 fields -- `Filename`, `NBStreams`, `Duration`, `Size`,
  `BitRate`, `FormatName`.

**Convenience methods on `Result`:**

| Method | Returns |
|--------|---------|
| `VideoStreamCount()` | Count of video streams |
| `AudioStreamCount()` | Count of audio streams |
| `DurationSeconds()` | Container duration as float64 |
| `SizeBytes()` | Container size as int64 |
| `BitRate()` | Container bitrate as int64 |

- `Inspect(ctx, binary, path)`: Execute ffprobe and parse JSON response.
  Defaults binary to "ffprobe" if empty.

### 4.5 Audio Track Selection (`media/audio`)

Selects the single primary English audio track for ripping. Used to configure
MakeMKV audio extraction before rip.

**Algorithm:**

1. Build candidate list from audio streams (extract language, channels, spatial
   audio, lossless codec, default flag).
2. Filter to English candidates (language starts with "en").
3. If no English found, fall back to first available stream.
4. Score candidates: channel count (8ch=1000, 6ch=800, 4ch=600, 2ch=400) +
   lossless bonus (100) + default flag (5) - stream order tiebreaker (0.1 per
   position).
5. Select highest-scoring candidate as primary.
6. All other audio streams are marked for removal.

**Types:**

- `Selection`: Primary stream, PrimaryIndex, KeepIndices, RemovedIndices.
- `Selection.PrimaryLabel()`: Human-readable summary (language | codec |
  channels | title).
- `Selection.Changed(totalAudio)`: True if any streams are removed.

**Detection helpers:**

- **Spatial audio**: Checks codec long name, profile, codec name, and title
  for: "atmos", "dts:x", "dtsx", "dts-x", "auro-3d", "imax enhanced".
- **Lossless codecs**: truehd, flac, mlp, alac, pcm_s16le, pcm_s24le,
  pcm_s32le, pcm_bluray, pcm_s24be, pcm_s16be, plus "lossless" or
  "master audio" or "dts-hd" in long name.
- **Channel count**: Prefers `stream.Channels` field; falls back to parsing
  `ChannelLayout` string (e.g., "7.1" -> 8, "5.1(side)" -> 6).

### 4.6 Encoding State (`encodingstate`)

Captures Drapto encoding telemetry for queue persistence and display.

**Snapshot** (33 JSON fields): `JobLabel`, `EpisodeKey`, `EpisodeIndex`,
`EpisodeCount`, `Stage`, `Message`, `Percent`, `ETASeconds`, `Speed`, `FPS`,
`Bitrate`, `TotalFrames`, `CurrentFrame`, `CurrentOutputBytes`,
`EstimatedTotalBytes`, `Hardware`, `Video`, `Crop`, `Config`, `Validation`,
`Warning`, `Error`, `Result`.

**Nested types:**

| Type | Fields |
|------|--------|
| `Hardware` | Hostname |
| `Video` | InputFile, OutputFile, Duration, Resolution, Category, DynamicRange, AudioDescription |
| `Crop` | Message, Crop, Required, Disabled |
| `Config` | Encoder, Preset, Tune, Quality, PixelFormat, MatrixCoefficients, AudioCodec, AudioDescription, DraptoPreset, PresetSettings, SVTParams |
| `PresetSetting` | Key, Value |
| `Validation` | Passed, Steps []ValidationStep |
| `ValidationStep` | Name, Passed, Details |
| `Issue` | Title, Message, Context, Suggestion |
| `Result` | InputFile, OutputFile, OutputPath, OriginalSize, EncodedSize, VideoStream, AudioStream, AverageSpeed, DurationSeconds, SizeReductionPercent |

**Serialization:**

- `Snapshot.IsZero()`: True when all fields are zero/empty/nil.
- `Snapshot.Marshal()`: JSON string (empty string for zero snapshot).
- `Unmarshal(raw)`: Parse from JSON string.

**Crop analysis:**

- `ParseCropFilter(filter)`: Extract width and height from "crop=W:H:X:Y" or
  "W:H:X:Y" format.
- `MatchStandardRatio(ratio)`: Match to standard aspect ratio within 2%
  tolerance. Standards: 4:3, 16:9, 1.85:1, 2.00:1, 2.20:1, 2.35:1, 2.39:1,
  2.40:1. Returns numeric label (e.g., "1.78:1") if no match.

### 4.7 Stage Handler Interface (`stage`)

Contract that all pipeline stages must implement:

```go
type Handler interface {
    Prepare(ctx context.Context, item *queue.Item) error
    Execute(ctx context.Context, item *queue.Item) error
    HealthCheck(ctx context.Context) Health
}
```

Optional interface for stages that accept a per-item logger:

```go
type LoggerAware interface {
    SetLogger(logger *slog.Logger)
}
```

**Health reporting:**

- `Health` struct: `Name string`, `Ready bool`, `Detail string`.
- `Healthy(name)`: Construct ready status.
- `Unhealthy(name, detail)`: Construct unhealthy status.

**Helper:**

- `ParseRipSpec(raw)`: Parse rip spec string into `ripspec.Envelope`, returning
  `services.ErrValidation` on failure (standard error for stage Execute methods).

### 4.8 Dependency Resolution (`deps`)

- `Requirement` struct: Name, Command, Description, Optional.
- `Status` struct: extends Requirement with Available and Detail.
- `CheckBinaries(requirements)`: Check each binary via `exec.LookPath`.

**Binary path resolution** (with environment variable overrides):

| Function | Env Variables | Fallback |
|----------|---------------|----------|
| `ResolveFFmpegPath()` | `SPINDLE_FFMPEG_PATH`, `FFMPEG_PATH` | PATH lookup, then "ffmpeg" |
| `ResolveFFprobePath(default)` | `SPINDLE_FFPROBE_PATH`, `FFPROBE_PATH` | PATH lookup of default, then "ffprobe" |

Environment variables are checked in order; the first non-empty value wins.
The resolved path is verified as an executable file (stat + permission check).

### 4.9 Staging Directory Management (`staging`)

- `ListDirectories(stagingDir)`: List all directories with metadata (`DirInfo`:
  Name, Path, ModTime, SizeBytes).
- `CleanStale(ctx, stagingDir, maxAge, logger)`: Remove directories older than
  maxAge. Returns `CleanStaleResult` with removed count and errors.
- `CleanOrphaned(ctx, stagingDir, activeFingerprints, logger)`: Remove
  directories whose names don't match any active fingerprint or `queue-*`
  format.

Used by: identification stage (stale staging cleanup), CLI `staging clean`
command.

---

## 5. Log Access Layer

### 5.1 File Tailing (`logs`)

- `Tail(ctx, path, opts)`: Read lines from a log file.
  - `TailOptions`: Offset (byte position), Limit (max lines), Follow (wait for
    new data), WaitDuration.
  - `TailResult`: Lines, Offset (next byte position for continuation).

### 5.2 HTTP Log Stream Client (`logs`)

- `StreamClient`: HTTP client for the `/api/logs` endpoint.
- `NewStreamClient(bind)`: Create client targeting `http://{bind}`.
- `StreamClient.Fetch(ctx, query)`: Fetch structured log events.
- `StreamQuery`: 12 filter parameters -- Since, Limit, Follow, Tail,
  Component, Lane, CorrelationID, ItemID, Level, Alert, DecisionType, Search.

**Error handling:**

- `ErrAPIUnavailable`: Sentinel error when API server is unreachable.
- `IsAPIUnavailable(err)`: Classification helper.

### 5.3 Log Stream Abstraction (`logstream`)

`Stream()` provides dual-mode log access with automatic fallback:

1. Try HTTP API via `StreamClient.Fetch()`.
2. If API unavailable and filters don't require API: fall back to IPC
   `LogTail` (raw line-based tailing).
3. If filters require API features (structured queries, lane filtering, etc.):
   return `ErrFiltersRequireAPI`.

---

## 6. Audit Gathering

The `auditgather` package provides comprehensive queue item analysis for
debugging. Used by the CLI `spindle audit-gather` command and the `/itemaudit`
skill.

### 6.1 Gather Pipeline

`Gather(ctx, cfg, item)` collects all artifacts for a queue item:

1. **Item summary**: ID, title, status, timestamps, file paths.
2. **Stage gate**: Furthest stage reached, media type, disc source, edition,
   boolean phase flags determining which phases run.
3. **Log analysis**: Parse per-item log for decisions, warnings, errors,
   stage events. Extract disc source inference.
4. **Rip cache**: Check for cached rip data and metadata.
5. **Envelope**: Parse RipSpec for fingerprint, content key, titles, episodes,
   assets, attributes.
6. **Encoding**: Extract encoding snapshot (preset settings stripped for
   compactness).
7. **Media probes**: FFprobe each encoded/final file. TV: probe each episode
   asset, falling back to final path if staging cleaned up.

**Stage gating** (`computeStageGate()`):

Each pipeline status maps to a numeric ordinal. `reachedAtLeast(item, target)`
checks whether the item's effective status >= the target ordinal. For failed
items, `FailedAtStatus` is used instead of `Status` (so a failure during
encoding still enables the rip cache phase).

| Phase Flag | Enabled When |
|------------|-------------|
| `PhaseLogs` | Always |
| `PhaseRipCache` | Reached `ripped` |
| `PhaseEpisodeID` | TV + reached `episode_identified` |
| `PhaseEncoded` | Reached `encoded` |
| `PhaseCrop` | Reached `encoded` |
| `PhaseEdition` | Movie + reached `identified` |
| `PhaseSubtitles` | Reached `subtitled` |
| `PhaseCommentary` | Reached `audio_analyzed` |
| `PhaseExternalValidation` | Reached `encoded` AND disc source != `dvd` |

Disc source (`DiscSource`) is initially `"unknown"` and refined from log
analysis after Phase 3. Values: `4k_bluray`, `bluray`, `dvd`, `unknown`.
Inferred by scanning raw JSON log lines for `is_blu_ray`, UHD indicators,
or `VIDEO_TS` patterns.

### 6.2 Analysis

Pre-computed summaries derived from gathered data:

- **Decision groups**: Aggregate identical decisions (type, result, reason)
  with count.
- **Episode consistency**: Majority profile (video codec, resolution, audio
  streams, subtitle streams) with per-episode deviations.
- **Crop analysis**: Filter dimensions, aspect ratio, standard ratio match.
- **Episode stats**: Matched/unresolved counts, confidence min/max/mean.
- **Media stats**: Duration and size ranges across all files.
- **Asset health**: Counts per stage (ripped, encoded, subtitled, final) with
  ok/failed/muxed breakdown.
- **Anomalies**: Red flags detected automatically (severity + category +
  message).

### 6.3 Key Types

| Type | Purpose |
|------|---------|
| `Report` | Top-level container (25 fields) |
| `Analysis` | Pre-computed summaries |
| `ItemSummary` | ID, title, status, timestamps |
| `StageGate` | Pipeline progress flags |
| `LogAnalysis` | Parsed log file results |
| `LogDecision` | Individual logged decision |
| `LogEntry` | Individual log line |
| `StageEvent` | Stage start/complete/fail events |
| `EnvelopeReport` | RipSpec analysis |
| `MediaFileProbe` | FFprobe result per file |
| `ProfileSummary` | Episode profile (video, audio, subtitles) |
| `Anomaly` | Detected issue (severity, category, message) |

---

## 7. Configuration Validation

### 7.1 Loading and Normalization

Config loading (see DESIGN_OVERVIEW.md Section 5.2) is followed by normalization
and validation:

1. **Normalize**: Expand `~` in all path fields, resolve to absolute paths,
   apply environment variable overrides (DESIGN_OVERVIEW.md Section 5.4), set
   defaults for empty fields.
2. **Validate**: Check required fields, value ranges, path accessibility,
   and cross-field consistency.

### 7.2 Validation Rules

Key validation constraints enforced by `validate.go`:

- `tmdb.api_key`: Required (non-empty).
- `paths.staging_dir`, `paths.log_dir`, `paths.review_dir`: Required.
- `encoding.svt_av1_preset`: Must be 0-13.
- `makemkv.rip_timeout`: Must be > 0.
- `makemkv.min_title_length`: Must be >= 0.
- `workflow.queue_poll_interval`: Must be > 0.
- `workflow.heartbeat_interval`: Must be > 0.
- `workflow.heartbeat_timeout`: Must be > heartbeat_interval.
- `jellyfin.url` and `jellyfin.api_key`: Required when `jellyfin.enabled`.
- `subtitles.whisperx_hf_token`: Required when subtitles enabled with
  pyannote VAD.
- `opensubtitles.api_key`: Required when OpenSubtitles enabled.

### 7.3 Config Commands

CLI config commands don't require a running daemon:

- `spindle config init`: Writes sample config to config file path.
  `skipConfigLoad` annotated -- does not load/validate existing config.
- `spindle config validate`: Load, normalize, and validate config. Reports
  all validation errors.

### 7.4 Commands Without Config

Some CLI commands are annotated with `skipConfigLoad` and work without any
config file. These include: `config init`, `queue health`, and help commands.
