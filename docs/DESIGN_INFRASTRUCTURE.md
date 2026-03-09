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

The daemon writes structured JSON log lines to a timestamped log file.

### 1.2 Log Event Structure

```json
{
  "seq": 1234,
  "ts": "2024-01-15T10:30:00.000Z",
  "level": "INFO",
  "msg": "encoding stage started",
  "component": "encoder",
  "stage": "encoding",
  "item_id": 42,
  "lane": "ripping",
  "request": "req-abc123",
  "fields": {"event_type": "stage_start", "decision_type": "..."},
  "details": [{"label": "Input", "value": "/path/to/file.mkv"}]
}
```

### 1.3 Level Semantics

| Level | Use For                                                                    |
|-------|----------------------------------------------------------------------------|
| INFO  | All decisions affecting output: stage start/complete, track selection,     |
|       | preset choice, skip decisions, fallback logic, cache hits                  |
| DEBUG | Raw data dumps, metrics, internal state (not decisions)                    |
| WARN  | Degraded behavior (include `event_type`, `error_hint`, `impact`)          |
| ERROR | Operation failed (include `event_type`, `error_hint`, `error`)            |

### 1.4 Decision Logging

All decisions use structured attributes:
- `decision_type`: Category (e.g., `title_source`, `media_type_detection`)
- `decision_result`: Outcome (e.g., `updated`, `skipped`, `detected`)
- `decision_reason`: Why this outcome was chosen

### 1.5 Progress Format

`"Phase N/M - Action (context)"` -- e.g., `"Phase 2/3 - Ripping selected titles (5 of 12)"`

### 1.6 Progress Sampling

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

### 1.7 Log Filtering

The daemon writes a single DEBUG-level JSON log file. The `/api/logs` endpoint
filters to INFO+ by default, providing clean output for Flyer and CLI consumers.
Additional server-side filters: `item` (item ID), `component`, `lane`,
`request`, and `daemon_only`.

### 1.8 Retention

Daemon log files older than `retention_days` are cleaned up on startup.

---

## 2. Notification System

### 2.1 ntfy Protocol

- HTTP POST to configured `ntfy_topic` URL.
- Headers: `Title`, `Priority`, `Tags`, `User-Agent: Spindle-Go/0.1.0`.
- Body: plain text message content.
- Timeout: `request_timeout` seconds (default **10 seconds**; fallback when <= 0).

Notifications are enabled when `ntfy_topic` is non-empty. All event types
are sent unconditionally (no per-event config gates).

### 2.2 Event Types

| Event                      | Priority | Tags               |
|----------------------------|----------|--------------------|
| `disc_detected`            | default  | -                  |
| `identification_complete`  | default  | identify           |
| `rip_complete`             | default  | rip                |
| `encode_complete`          | default  | encode             |
| `validation_failed`        | high     | validation,warning |
| `pipeline_complete`        | default  | -                  |
| `organize_complete`        | default  | organize           |
| `queue_started`            | default  | queue              |
| `queue_completed`          | default  | queue              |
| `error`                    | high     | error              |
| `unidentified_media`       | default  | review             |
| `test`                     | low      | test               |

---

## 3. Preflight Checks

### 3.1 System Dependencies

Checked at daemon startup (fatal if required dependencies missing) and
via `spindle status`. No per-stage rechecking — binaries do not disappear
mid-run.

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

Wraps `ffprobe -v quiet -print_format json -show_format -show_streams`.

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

Captures Drapto encoding telemetry for queue persistence and TUI display.
The snapshot is served directly as the `encoding` field in the queue API
response -- no transformation layer between DB and API.

**Snapshot** (28 fields, 3 nested types):

| Field | Type | Set When | Display Use |
|-------|------|----------|-------------|
| `percent` | float64 | Live | Progress bar |
| `eta_seconds` | float64 | Live | ETA display |
| `fps` | float64 | Live | Throughput indicator |
| `current_frame` | int64 | Live | Frame-based progress |
| `total_frames` | int64 | Live | Frame-based progress |
| `current_output_bytes` | int64 | Live | Bytes written during encode |
| `estimated_total_bytes` | int64 | Live | Estimated final size (>= 10% progress) |
| `substage` | string | Live | Substage label: "crop_analysis", "encoding", "validation" |
| `input_file` | string | Start | Episode matching (which rip is encoding) |
| `resolution` | string | Start | "1080p" / "2160p" |
| `dynamic_range` | string | Start | "SDR" / "HDR10" / "Dolby Vision" |
| `encoder` | string | Start | Encoder name (e.g., "libsvtav1") |
| `preset` | string | Start | SVT-AV1 preset number |
| `quality` | string | Start | CRF value |
| `tune` | string | Start | SVT-AV1 tune parameter |
| `audio_codec` | string | Start | Audio codec (e.g., "libopus") |
| `drapto_preset` | string | Start | Drapto preset name |
| `crop_filter` | string | Start | FFmpeg crop filter (empty = no crop) |
| `crop_required` | bool | Start | Whether cropping was needed |
| `crop_message` | string | Start | Crop detection summary |
| `original_size` | int64 | End | Size comparison |
| `encoded_size` | int64 | End | Size comparison |
| `size_reduction_percent` | float64 | End | "42% reduction" |
| `average_speed` | float64 | End | "1.2x avg" |
| `encode_duration_seconds` | float64 | End | Wall-clock encode time |
| `warning` | string | Any | Warning message |
| `error` | *Issue | Any | Structured error detail |
| `validation` | *Validation | End | Pass/fail with per-step detail |

**Nested types:**

| Type | Fields |
|------|--------|
| `Issue` | Title, Message, Context, Suggestion |
| `Validation` | Passed, Steps []ValidationStep |
| `ValidationStep` | Name, Passed, Details |

**Serialization:**

- `Snapshot.IsZero()`: True when all fields are zero/empty/nil.
- `Snapshot.Marshal()`: JSON string (empty string for zero snapshot).
- `Unmarshal(raw)`: Parse from JSON string.

**Crop analysis helpers:**

- `ParseCropFilter(filter)`: Extract width and height from "crop=W:H:X:Y" or
  "W:H:X:Y" format.
- `MatchStandardRatio(ratio)`: Match to standard aspect ratio within 2%
  tolerance. Standards: 4:3, 16:9, 1.85:1, 2.00:1, 2.20:1, 2.35:1, 2.39:1,
  2.40:1. Returns numeric label (e.g., "1.78:1") if no match.

### 4.7 Stage Handler Interface (`stage`)

Contract that all pipeline stages must implement:

```go
type Handler interface {
    Run(ctx context.Context, item *queue.Item) error
}
```

**Per-item logging**: The pipeline manager attaches an `item_id` attribute to
the logger before calling `Run`. Handlers retrieve it via:

```go
func LoggerFromContext(ctx context.Context) *slog.Logger
```

Returns `slog.Default()` if no logger is attached. All log lines for an item
share the same `item_id` field, enabling filtering from the single daemon log.

**Helper:**

- `ParseRipSpec(raw)`: Parse rip spec string into `ripspec.Envelope`, returning
  `services.ErrValidation` on failure (standard error for stage `Run` methods).

### 4.7.1 Connectivity Checks (standalone)

Deeper service health checks are standalone functions, not part of the
`Handler` interface. Used by `spindle status` and `GET /api/status`:

- `CheckLLM(ctx, cfg)`: Health check against configured LLM API (30s timeout).
- `CheckJellyfin(ctx, cfg)`: Auth check via `/Users` endpoint.
- `CheckOpenSubtitles(ctx, cfg)`: Format info check via `/infos/formats`.
- `CheckDirectoryAccess(paths)`: Verify read/write/execute on staging,
  library, review dirs.

These do not gate stage execution. Transient service failures fail the item;
retry is workflow-level via `spindle queue retry`.

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
- `CleanStale(ctx, stagingDir, maxAge, activeFingerprints, logger)`: Remove
  directories older than maxAge, skipping directories whose names match an
  active fingerprint or `queue-*` pattern. The `activeFingerprints` set is
  obtained from `store.ActiveFingerprints()` before calling. Returns
  `CleanStaleResult` with removed count and errors.
- `CleanOrphaned(ctx, stagingDir, activeFingerprints, logger)`: Remove
  directories whose names don't match any active fingerprint or `queue-*`
  format.

Used by: identification stage (stale staging cleanup), CLI `staging clean`
command.

---

## 5. Error Taxonomy

Stage handlers return errors via the `services` package. Two outcomes:
fail the item or warn and continue.

### 5.1 Error Types

| Type | Error | Behavior |
|------|-------|----------|
| **Fail** | Any non-degraded error | Fail the item, record `failed_at_stage` |
| **Warn** | `services.ErrDegraded` | Log warning, continue processing |

No in-process retry. Retry is workflow-level: `spindle queue retry <id>`
re-runs from the failed stage.

### 5.2 Degraded Errors (warn and continue)

Degraded errors are for optional functionality whose failure should not
block the pipeline:

- bd_info unavailable (proceed without enhanced metadata)
- TMDB returns no results (use fallback metadata, flag for review)
- Rip cache write failure (rip succeeded, cache is optional)
- No episode ID reference matches (keep placeholder keys, flag for review)
- Commentary detection failure (continue without commentary labels)
- OpenSubtitles download failure (skip forced subs)
- SRT validation issues (flag for review, don't fail)
- Jellyfin refresh failure (log warning)
- Subtitle sidecar move failure (log warning)

Everything else (MakeMKV failures, encoding errors, file copy errors,
WhisperX failures, network errors) fails the item.

### 5.3 Error Wrapping

Standard `fmt.Errorf` with `%w` wrapping. Operation context and actionable
hints are included in the error message string (e.g.,
`fmt.Errorf("makemkv rip: timed out; consider increasing rip_timeout: %w", err)`).

`ErrDegraded` is detected via `errors.As`. All other errors fail the item.

---

## 6. Log Access Layer

### 6.1 File Tailing (`logs`)

- `Tail(ctx, path, opts)`: Read lines from a log file.
  - `TailOptions`: Offset (byte position), Limit (max lines), Follow (wait for
    new data), WaitDuration.
  - `TailResult`: Lines, Offset (next byte position for continuation).

Used by the CLI `spindle logs` command for direct file access when the daemon
is not running. When the daemon is running, the CLI uses `/api/logs` (which
reads and filters the same file server-side).

---

## 7. Audit Gathering

The `auditgather` package provides comprehensive queue item analysis for
debugging. Its sole consumer is the `/itemaudit` agent skill (the CLI
`spindle audit-gather` command exists as that skill's entry point).

**Why this complexity is intentional:** The structured JSON output --
pre-computed analysis, anomaly flags, aggregated decisions -- exists so
the LLM skill can audit a queue item in a single context window without
running 10+ sequential shell commands (`sqlite3`, `jq`, `ffprobe`, log
parsing). Front-loading this work in Go keeps the skill's token budget
focused on reasoning about the results rather than gathering them. The
type count reflects the breadth of the audit surface (7 pipeline stages,
4 asset phases, per-episode media probes), not unnecessary abstraction.

### 7.1 Gather Pipeline

`Gather(ctx, cfg, item)` collects all artifacts for a queue item:

1. **Item summary**: ID, title, status, timestamps, file paths.
2. **Log analysis**: Filter daemon log by item ID for decisions, warnings,
   errors, stage events.
3. **Rip cache**: Check for cached rip data and metadata.
4. **Envelope**: Parse RipSpec for fingerprint, content key, titles, episodes,
   assets, attributes. Reads `disc_source` from envelope metadata.
5. **Encoding**: Extract encoding snapshot.
6. **Media probes**: FFprobe each encoded/final file. TV: probe each episode
   asset, falling back to final path if staging cleaned up.

**Phase applicability**: The report includes boolean phase flags computed
from `furthest_stage` + `media_type` + `disc_source` so the skill knows
which sections of the report are meaningful. These are computed inline
during gathering, not via a separate type.

| Phase Flag | Enabled When |
|------------|-------------|
| `phase_logs` | Always |
| `phase_rip_cache` | Past `ripping` stage |
| `phase_episode_id` | TV + past `episode_identification` stage |
| `phase_encoded` | Past `encoding` stage |
| `phase_crop` | Past `encoding` stage |
| `phase_edition` | Movie + past `identification` stage |
| `phase_subtitles` | Past `subtitling` stage |
| `phase_commentary` | Past `audio_analysis` stage |
| `phase_external_validation` | Past `encoding` stage AND disc source != `dvd` |

For failed items, `failed_at_stage` is used instead of `stage` when
computing the furthest stage reached (so a failure during encoding still
enables the rip cache phase).

### 7.2 Analysis

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

### 7.3 Key Types

#### Report (top-level)

| Field | Type | Description |
|-------|------|-------------|
| `item` | ItemSummary | Queue item summary |
| `furthest_stage` | string | Highest pipeline stage reached |
| `media_type` | string | movie or tv |
| `disc_source` | string | From envelope `metadata.disc_source` |
| `edition` | string? | Detected edition label |
| `phase_*` | bool | Phase applicability flags (see Section 7.1) |
| `logs` | LogAnalysis? | Log entries filtered by item ID |
| `rip_cache` | RipCacheReport? | Cached rip data and metadata |
| `envelope` | EnvelopeReport? | Parsed RipSpec envelope |
| `encoding` | EncodingReport? | Encoding snapshot |
| `media` | []MediaFileProbe | FFprobe results per file |
| `media_omitted` | int | Files skipped (too many) |
| `analysis` | Analysis? | Pre-computed summaries |
| `errors` | []string | Non-fatal gather errors |

#### ItemSummary

| Field | Type |
|-------|------|
| `id` | int64 |
| `disc_title` | string |
| `stage` | string |
| `failed_at_stage` | string? |
| `error_message` | string? |
| `needs_review` | bool |
| `review_reason` | string? |
| `disc_fingerprint` | string? |
| `created_at` | string (RFC3339) |
| `updated_at` | string (RFC3339) |
| `progress_stage` | string? |
| `progress_percent` | float64? |
| `progress_message` | string? |

#### LogAnalysis

| Field | Type |
|-------|------|
| `path` | string |
| `total_lines` | int |
| `decisions` | []LogDecision |
| `warnings` | []LogEntry |
| `errors` | []LogEntry |
| `stages` | []StageEvent |

#### LogDecision

`ts`, `decision_type`, `decision_result`, `decision_reason`, `message` (all strings).

#### LogEntry

`ts`, `level`, `message`, `event_type`, `error_hint` (strings) + `extras` (map).

#### StageEvent

`ts`, `event_type`, `stage`, `message` (strings) + `duration_seconds` (float64).

#### RipCacheReport

`path` (string), `found` (bool), `metadata` (ripcache.EntryMetadata?).

#### EnvelopeReport

`fingerprint`, `content_key` (strings), `metadata` (EnvelopeMetadata),
`titles` ([]Title), `episodes` ([]Episode), `assets` (Assets),
`attributes` (EnvelopeAttributes). Types from `ripspec` package.

#### EncodingReport

`snapshot` (encodingstate.Snapshot) -- see Section 4.6.

#### MediaFileProbe

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | File path |
| `role` | string | encoded, final, etc. |
| `episode_key` | string? | Episode key for TV |
| `representative` | bool? | True if selected as representative |
| `probe` | ffprobe.Result | Full ffprobe output |
| `size_bytes` | int64 | File size |
| `duration_seconds` | float64 | Duration |
| `error` | string? | Probe error if any |

#### Analysis

The analysis fields use inline struct definitions in Go (not named exported
types) since each is constructed in exactly one place and consumed only as
JSON by the skill.

| Field | JSON Shape | Description |
|-------|-----------|-------------|
| `decision_groups` | `[{decision_type, decision_result, decision_reason, count, entries}]` | Aggregated decisions |
| `episode_consistency` | `{majority_profile, majority_count, total_episodes, deviations}` | Majority media profile + per-episode deviations |
| `crop_analysis` | `{filter, output_width, output_height, aspect_ratio, standard_ratio}` | Crop detection results |
| `episode_stats` | `{count, matched, unresolved, confidence_min/max/mean, sequence_contiguous, episode_range}` | Episode ID summary |
| `media_stats` | `{file_count, duration_min_sec, duration_max_sec, size_min_bytes, size_max_bytes}` | Duration and size ranges |
| `asset_health` | `{ripped, encoded, subtitled, final}` each `{total, ok, failed, muxed}` | Per-stage asset counts |
| `anomalies` | `[{severity, category, message}]` | Auto-detected red flags |

---

## 8. Configuration Validation

### 8.1 Loading and Normalization

Config loading (see DESIGN_OVERVIEW.md Section 5.2) is followed by normalization
and validation:

1. **Normalize**: Expand `~` in all path fields, resolve to absolute paths,
   apply environment variable overrides (DESIGN_OVERVIEW.md Section 5.4), set
   defaults for empty fields.
2. **Validate**: Check required fields, value ranges, path accessibility,
   and cross-field consistency.

### 8.2 Validation Rules

Key validation constraints enforced by `validate.go`:

- `tmdb.api_key`: Required (non-empty).
- `paths.staging_dir`, `paths.log_dir`, `paths.review_dir`: Required.
- `encoding.svt_av1_preset`: Must be 0-13.
- `makemkv.rip_timeout`: Must be > 0.
- `makemkv.min_title_length`: Must be >= 0.
- `jellyfin.url` and `jellyfin.api_key`: Required when `jellyfin.enabled`.
- `subtitles.whisperx_hf_token`: Required when subtitles enabled with
  pyannote VAD.
- `opensubtitles.api_key`: Required when OpenSubtitles enabled.

### 8.3 Config Commands

CLI config commands don't require a running daemon:

- `spindle config init`: Writes sample config to config file path.
  `skipConfigLoad` annotated -- does not load/validate existing config.
- `spindle config validate`: Load, normalize, and validate config. Reports
  all validation errors.

### 8.4 Commands Without Config

Some CLI commands are annotated with `skipConfigLoad` and work without any
config file. These include: `config init` and help commands.

---

## 9. Shared Transcription Service

### 9.1 Purpose

WhisperX transcription is used by three subsystems: episode identification,
commentary detection, and subtitle generation. Rather than each subsystem
invoking WhisperX independently with separate caching, a shared
`transcription` package provides a unified interface.

### 9.2 Interface

```go
type Service struct {
    // Configuration: model, CUDA, VAD method, cache dir, etc.
}

func (s *Service) Transcribe(ctx context.Context, req TranscribeRequest) (*TranscribeResult, error)
```

**TranscribeRequest:**

| Field | Type | Purpose |
|-------|------|---------|
| `InputPath` | string | Path to media file |
| `AudioIndex` | int | Audio stream index to extract |
| `Language` | string | Target language code |
| `OutputDir` | string | Directory for output files |
| `Model` | string | WhisperX model override (empty = default) |

**TranscribeResult:**

| Field | Type | Purpose |
|-------|------|---------|
| `SRTPath` | string | Path to generated SRT file |
| `Duration` | float64 | Detected audio duration in seconds |
| `Segments` | int | Number of SRT cues |
| `Cached` | bool | True if result came from cache |

### 9.3 Cache Strategy

The cache is keyed by a composite of:
- SHA-256 of the source file path and audio stream index
- WhisperX model name
- Language

Cache key computation:

```
key = hex(SHA-256( path + "\x00" + streamIndex + "\x00" + model + "\x00" + language ))
```

Where `path` is the absolute file path, `streamIndex` is the decimal audio
stream index, `model` is the WhisperX model name, and `language` is the
ISO 639-1 code. All components are UTF-8 strings separated by NUL bytes.

Cache directory: `~/.local/share/spindle/cache/whisperx/{cache_key}/`

**Cache operations:**
- `Lookup(key)`: Check for existing transcription. Validates SRT exists and
  has > 0 cues.
- `Store(key, result)`: Copy SRT to cache directory.

The cache is shared across all three consumers. Episode ID transcripts are
automatically available for subtitle generation via cache hits, eliminating
redundant GPU work.

### 9.4 Concurrency

All WhisperX invocations are serialized by the `whisperxSem` semaphore
(capacity 1) at the pipeline level. The transcription service itself is
stateless and safe for concurrent use -- the semaphore is held by the
calling stage, not the service.

### 9.5 Audio Extraction

Audio extraction (FFmpeg WAV conversion) is performed by the service before
invoking WhisperX:

```
ffmpeg -i <input> -map 0:<audioIndex> -ac 1 -ar 16000 -c:a pcm_s16le -vn -sn -dn <output.wav>
```

### 9.6 Post-Processing Pipeline

After WhisperX completes:

1. **Hallucination filtering**: Remove WhisperX artifacts and repetitive
   segments (see DESIGN_STAGES.md Section 6.2).
2. **Validation**: Zero-segment output fails the stage.
