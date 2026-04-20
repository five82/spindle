# API Reference: External Service Protocols

Protocols and integration details for all external services used by Spindle.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. MakeMKV CLI

Binary: `makemkvcon`

### Disc Scan

```
makemkvcon --robot --progress=-same info disc:<N>
```

or with device path:

```
makemkvcon --robot --progress=-same info dev:<device>
```

Output: Robot-format lines parsed for disc info, titles, and streams.
Key message types: `TINFO` (title info), `SINFO` (stream info), `CINFO`
(disc info), `MSG` (messages), `PRGV` (progress values).

### Ripping

```
makemkvcon --robot --progress=-same mkv disc:<N> <title_ids> <output_dir>
```

Progress parsed from `PRGV` lines: `current,total,max`.

### MKV File Selection

3-tier fallback to locate the output file after ripping:
1. Match `_t{NN}.mkv` suffix to requested title ID (when single title).
2. Largest file by size.
3. Newest file by modification time.

### MSG Code Handling

MSG codes classify MakeMKV diagnostic output:
- Codes >= 5000: disc-level messages (logged at WARN).
- Codes < 5000: informational (logged at DEBUG).

**Error classification** (from MSG 2003 read errors):
- "TRAY OPEN" -> `tray_open`
- "L-EC UNCORRECTABLE" -> `uncorrectable_read`
- "HARDWARE ERROR" -> `hardware_error`
- Other -> `read_error`

### Settings Configuration

`EnsureSettings()` configures required MakeMKV settings before each rip by
reading and updating `~/.MakeMKV/settings.conf`.

**Required settings:**

| Setting | Value | Purpose |
|---------|-------|---------|
| `app_DefaultSelectionString` | `-sel:all,+sel:video,+sel:audio,+sel:subtitle` | Ensure all video, audio, and subtitle tracks are selected for ripping |
| `app_LibdriveIO` | `true` | Enable libdrive mode for direct disc access (required for UHD discs) |

**Smart-update behavior:** Reads the existing settings file, checks whether all
required settings match, and only writes the file if an update is needed.
Existing user settings not in the required set are preserved. The settings
directory (`~/.MakeMKV/`) is created if absent. Settings write failure is
non-fatal (logged as warning, ripping continues).

### Device Argument Normalization

The device path is normalized before passing to `makemkvcon`:
- Empty string -> `disc:0` (default first disc drive)
- Starts with `/dev/` -> `dev:{path}` prefix (e.g., `/dev/sr0` -> `dev:/dev/sr0`)
- Already prefixed (e.g., `disc:0`) -> passed through unchanged

### Minimum Title Length

When `makemkv.min_title_length` > 0, adds `--minlength={seconds}` flag to scan
and rip commands.

### Post-Rip Cleanup

After selecting and renaming the target MKV output file, all other `.mkv` files
in the output directory are removed to prevent leftover fragments from consuming
disk space.

### Progress Phase Tracking

- `PRGT` lines set the current phase context (e.g., "Analyzing seamless segments").
- `PRGV` progress values are attributed to the current phase.
- Output file size is monitored every 30 seconds during ripping (logged at DEBUG).

---

## 2. TMDB REST API

Base URL: `https://api.themoviedb.org/3` (configurable via `tmdb.base_url`)

Auth: `Authorization: Bearer <tmdb_api_key>`

Language: Configurable (default: `en-US`)

HTTP timeout: **15 seconds** (hardcoded).

### Endpoints Used

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/search/movie?query=<q>&year=<y>&language=<l>` | Search movies |
| GET | `/search/tv?query=<q>&first_air_date_year=<y>&language=<l>` | Search TV shows |
| GET | `/search/multi?query=<q>&language=<l>` | Search movies and TV |
| GET | `/movie/{id}?language=<l>` | Movie details |
| GET | `/tv/{id}?language=<l>` | TV show details |
| GET | `/tv/{id}/season/{season}?language=<l>` | Season details (episode list) |

### Additional Behaviors

- **Runtime filter**: +/- 10 minute tolerance when filtering by runtime.
- **Latency tracking**: Request duration included in error messages for debugging.

---

## 3. OpenSubtitles REST API

Base URL: `https://api.opensubtitles.com/api/v1` (configurable)

Auth headers:
- `Api-Key: <opensubtitles_api_key>`
- `Authorization: Bearer <opensubtitles_user_token>` (optional, for download quota)
- `User-Agent: <opensubtitles_user_agent>`

Rate limiting: Minimum **3 seconds** between API calls (client-enforced).

Fixed-delay retry: **3 retries**, **5 seconds** between attempts.

Retriable conditions: status 429, 502, 503, 504, timeouts, connection errors.

### Endpoints Used

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/subtitles?tmdb_id=<id>&season_number=<s>&episode_number=<e>&languages=<l>` | Search subtitles |
| POST | `/download` | Negotiate subtitle download (body: `{"file_id": <id>, "sub_format": "srt"}`) |
| GET | `/infos/formats` | Health check |

Search also supports: `parent_tmdb_id`, `query`, `type`, `year` parameters.
Multiple search variants are tried per episode (see CONTENT_ID_DESIGN.md Section 5.1).

### Download Flow (2-Step Negotiation)

Subtitle download uses a two-step process:

1. **Negotiate**: POST to `/download` with `file_id` and `sub_format`. Response
   returns a JSON object with a signed `link` URL and `file_name`.
2. **Fetch**: GET the signed URL to retrieve the subtitle data bytes. Only
   `User-Agent` header is sent (no API key).

The signed URL may be on a different host. Redirect handling preserves headers
across redirects (Go's default strips them), with a maximum of **10 redirects**.
HTTP timeout: **45 seconds** (hardcoded `defaultHTTPTimeout`).

---

## 4. WhisperX CLI

Invoked via `uvx` (Python package runner) through an embedded Spindle-owned
Python wrapper rather than relying on bare WhisperX CLI defaults:

```
uvx --from whisperx python -c <embedded_transcription_script> \
  --audio <input_audio> \
  --output-dir <dir> \
  --model large-v3 \
  --language <lang> \
  --vad-method <silero|pyannote> \
  --device <cpu|cuda> \
  --compute-type <int8|float16>
```

GPU acceleration is controlled by `subtitles.whisperx_cuda_enabled`.

The wrapper sets the shared transcription profile explicitly for long-form
media work instead of inheriting WhisperX defaults. In particular it:

- enables VAD-guided speech-region transcription,
- sets explicit VAD merge controls (chunk size / onset / offset),
- sets `condition_on_previous_text = false`,
- runs alignment and preserves word timing plus score/probability metadata when
  available,
- preserves segment-level decode metadata such as average log probability when
  available,
- writes canonical artifacts as `audio.srt` and `audio.json`.

WhisperX output is treated as the **canonical transcript**. Final display SRTs
are produced later by the subtitle stage from canonical JSON/alignment output.

### Audio Extraction (Pre-Processing)

Before invoking WhisperX, audio is extracted from the source MKV via FFmpeg:

```
ffmpeg -i <input> -map 0:<audioIndex> -ac 1 -ar 16000 -c:a pcm_s16le -vn -sn -dn <output.wav>
```

Parameters:
- `-ac 1`: Downmix to mono
- `-ar 16000`: Resample to 16 kHz
- `-c:a pcm_s16le`: PCM 16-bit signed little-endian
- `-vn -sn -dn`: Strip video, subtitle, and data streams
- `-map 0:{audioIndex}`: Select specific audio track by stream index

### Subtitle Formatting (Stable-TS)

Subtitle generation formats viewer-facing SRTs by invoking Stable-TS via `uvx`
against the canonical WhisperX JSON/alignment output:

```
uvx --from stable-ts-whisperless python -c <embedded_formatter_script> \
  <input.json> <output.srt> --language <lang>
```

Behavior:
- Stable-TS regrouping/line breaking is the supported subtitle-formatting path.
- After Stable-TS renders the display SRT, Spindle may apply a final
  display-only readability repair pass that fallback-splits/re-wraps cue text
  and expands short cues into nearby silence gaps when that improves reading
  speed without mutating canonical cached transcript artifacts.
- Subtitle formatting consumes derived working JSON; canonical cached transcript
  artifacts remain unchanged.
- Subtitle filtering and validation use the actual encoded-media duration when
  available; transcript-tail duration is only a fallback.
- If formatting fails, that subtitle job fails explicitly rather than silently
  falling back to the old raw-wrap behavior.

### Environment Handling

- **Torch compatibility**: Sets `TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1` for the
  WhisperX wrapper process to work around Torch 2.6+ loading changes that break
  WhisperX/pyannote.
- **VAD method**: Controlled by `subtitles.whisperx_vad_method`; defaults to
  `silero`, can switch to `pyannote` (requires HF token).
- **HF token**: Passed through to the wrapper when configured so pyannote VAD
  can authenticate.

---

## 5. LLM (OpenRouter)

Base URL: `https://openrouter.ai/api/v1/chat/completions` (configurable via
`llm.base_url`)

Default model: `google/gemini-3-flash-preview` (configurable via `llm.model`)

Auth: `Authorization: Bearer <llm_api_key>`

### Request Format

```json
{
  "model": "<model_id>",
  "messages": [
    {"role": "system", "content": "<system_prompt>"},
    {"role": "user", "content": "<user_prompt>"}
  ],
  "temperature": 0,
  "response_format": {"type": "json_object"}
}
```

Temperature hardcoded to `0` for deterministic output. Response format always
constrained to `{"type": "json_object"}`.

### Use Cases

See DESIGN_LLM_PROMPTS.md for exact prompts, response schemas, and failure
behavior for all three use cases:

1. **Commentary classification**: Determine if an audio track is commentary
   based on transcript analysis (Section 1)
2. **Episode verification**: Compare WhisperX and OpenSubtitles transcripts to
   verify episode matching (Section 2)

### Retry Strategy

- **Attempts**: 5 (with exponential backoff: 1s base, 10s max, 2x per attempt).
- **Retriable status codes**: 408, 429, 5xx.

### JSON Payload Handling

- **Sanitization**: Strips markdown code fence blocks, extracts JSON object/array
  from wrapper text.
- **Content extraction fallback chain**: `message.content` -> `delta.content` ->
  legacy `text` field -> function call arguments -> tool call arguments.

---

## 6. Jellyfin API

Auth: `X-Emby-Token: <jellyfin_api_key>`

### Endpoints Used

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/Library/Refresh` | Trigger full library scan |
| GET | `/Users` | Health check (validate API key) |

### Implementation Note

File organization is handled by `SimpleService` (direct filesystem operations).
`HTTPService` wraps `SimpleService` for organization and adds HTTP-based library
refresh. Cross-filesystem moves use EXDEV fallback (copy + delete). File
collisions resolved with counter suffix `(1)`, `(2)`, etc.

---

## 7. ntfy

Protocol: HTTP POST to topic URL.

```
POST <ntfy_topic_url>
Content-Type: text/plain
Title: <notification_title>
Priority: <1-5>
Tags: <comma_separated_tags>

<notification_body>
```

No authentication (relies on topic URL privacy or server-level auth).

11 event types generate notifications (see DESIGN_INFRASTRUCTURE.md
Section 2.3 for the canonical list):
- `item_queued`, `identification_complete`
- `rip_cache_hit`, `rip_complete`
- `encode_complete`
- `review_required`, `pipeline_complete`
- `queue_started`, `queue_completed`
- `error`, `test`

Behavior notes:
- Notifications are outcome-focused; stage-start events are not pushed.
- Validation and quality issues roll up into the terminal `review_required`
  notification instead of emitting per-issue alerts.
- `queue_started` and `queue_completed` are paired backlog-cycle notifications.
  `queue_started` requires at least 2 active items, and `queue_completed` is
  only emitted if a matching `queue_started` was already sent for that cycle.

---

## 8. FFprobe

```
ffprobe -v quiet -print_format json -show_format -show_streams <file>
```

This is the canonical invocation used everywhere in Spindle.

Used for: media file validation, stream inspection, duration detection,
codec identification.

---

## 9. KeyDB (bd_info)

Binary: `bd_info` (optional)

```
bd_info <device_path>
```

Parses output for Blu-ray disc name, year, studio from the disc's metadata.
Used during identification to improve TMDB search accuracy.

### KeyDB Catalog Management

- **Eager load**: Catalog file loaded at daemon startup.
- **Staleness warning**: When catalog file is older than **7 days**, logs a
  warning suggesting re-download.
- **Hex ID validation**: Strips "0X" prefix, validates exactly **40 hex characters**.
- **Title extraction** (3-step chain, first non-empty result wins):
  1. `extractAlias()`: If title contains `[brackets]`, extract the bracketed
     content as the alias (e.g., `"Foo [Bar]"` -> `"Bar"`).
  2. `stripAlias()`: If no alias found, strip everything from the first `[`
     onward (e.g., `"Foo [extra]"` -> `"Foo"`).
  3. `normalizeDuplicateTitle()`: Unwrap `Title (Title)` patterns where the
     parenthesized suffix exactly matches the prefix
     (e.g., `"Movie (Movie)"` -> `"Movie"`). Uses balanced parenthesis
     matching to handle nested cases.
  4. Fallback: raw payload if all transforms produce empty.

---

## 10. MediaInfo

```
mediainfo --Output=JSON <file>
```

Used for: detailed track metadata inspection during identification and encoding
validation.

---

## 11. mkvmerge

```
mkvmerge -o <output.mkv> <input.mkv> --language 0:<lang> --track-name 0:<name> <subtitle.srt>
```

Used for muxing subtitle tracks into MKV containers when
`subtitles.mux_into_mkv = true`.

---

## 12. Drapto Encoding Library

Drapto is a Go library (not a separate binary) used for SVT-AV1 encoding.

### Client Interface

`Client` wraps the Drapto library, providing:
- `Encode(ctx, inputPath, outputDir, opts) (string, error)`: Run encoding job
- Options include a `Progress` callback for real-time progress updates

### Reporter Adapter

`spindleReporter` bridges Drapto's `Reporter` interface to the flat
`encodingstate.Snapshot`. Maps Drapto event types to snapshot fields:

| Event Type | Snapshot Fields Updated |
|------------|------------------------|
| `initialization` | `input_file`, `resolution`, `dynamic_range` |
| `stage_progress` | `substage`, `percent`, `eta_seconds` |
| `encoding_started` | `total_frames` |
| `encoding_progress` | `percent`, `fps`, `eta_seconds`, `current_frame` |
| `encoding_config` | `preset`, `quality`, `tune`, `encoder`, `audio_codec`, `drapto_preset` |
| `crop_result` | `crop_filter`, `crop_required`, `crop_message` |
| `validation_complete` | `validation` |
| `encoding_complete` | `original_size`, `encoded_size`, `size_reduction_percent`, `average_speed`, `encode_duration_seconds` |
| `warning` | `warning` |
| `error` | `error` |

Remaining Drapto events (`hardware`, `operation_complete`, `batch_started`,
`file_progress`, `batch_complete`) are logged but not persisted to the
snapshot -- they duplicate information available on the parent queue item
or are irrelevant for a single-machine deployment.
