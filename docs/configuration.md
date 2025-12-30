# Spindle Configuration Guide

This guide expands on the `spindle config init` output and explains every key in
`~/.config/spindle/config.toml`. Use it when tuning the daemon or when new
features land.

## Getting Started

1. Install the CLI (`go install github.com/five82/spindle/cmd/spindle@latest`).
2. Generate a config skeleton:

   ```bash
   spindle config init
   ```

3. Edit `~/.config/spindle/config.toml` with your preferred editor. Sample:

   ```toml
   [paths]
   library_dir = "~/Media/Library"
   staging_dir = "~/Media/Staging"

   [tmdb]
   api_key = "tmdb-key-here"

   [jellyfin]
   enabled = true
   url = "https://jellyfin.example.com"
   api_key = "jellyfin-api-key"

   [notifications]
   ntfy_topic = "spindle"
   ```

4. Validate and authorize:

   ```bash
   spindle config validate
   ```

Spindle reads this configuration on startup. Changes require restarting the
daemon (`spindle stop && spindle start`).

## Core Paths & Storage

| Key | Purpose | Notes |
| --- | --- | --- |
| `library_dir` | Final Jellyfin-ready library root. | Must exist; Spindle creates `movies/` & `tv/` subdirs when absent. |
| `staging_dir` | Work area for rips, encodes, subtitles, logs. | Prefer fast storage; large temporary files live here. |
| `review_dir` | Destination for items flagged `NeedsReview`. | Defaults to `~/review`; contents are safe to rename manually. |
| `log_dir` | Persistent logs plus the queue DB. | Ensure enough space for SQLite + log rotation. |
| `log_retention_days` | Days to keep daemon/item logs before pruning. | Default 60; set 0 to disable automatic cleanup. |
| `rip_cache_dir` | Optional cache of MakeMKV output. | Enable with `rip_cache_enabled = true`. |

Spindle enforces a 20% free-space floor on the rip cache. Tune
`rip_cache_max_gib` to cap cache size.

Daemon and item logs are pruned automatically when they exceed
`log_retention_days` (default 60). Set the value to `0` to retain logs
indefinitely.

## Identification & Metadata

- `tmdb_api_key` *(required)* — Grab from https://www.themoviedb.org/settings/api.
- `tmdb_language` — ISO 639‑1 code for metadata (default `en`).
- `tmdb_confidence_threshold` — Float 0‑1; lower values accept fuzzier matches.
- `identification_overrides_path` — JSON file for manual disc→title overrides
  (defaults to `~/.config/spindle/overrides/identification.json`).
- `bd_info_enabled` — When true, Spindle shells out to `bd_info` for additional
  playlist metadata. Requires `libbluray` utilities (`libbluray-bin`,
  `libbluray-utils`, etc.).

If discs often appear as “UNKNOWN” or “INDEX_BDMV”, install `bd_info` and ensure
mount points `/media/cdrom` or `/media/cdrom0` stay accessible.

## Jellyfin & Library Integration

| Key | Description |
| --- | --- |
| `jellyfin.url` | Base URL of your Jellyfin server (e.g. `https://jellyfin.example.com`). |
| `jellyfin.api_key` | Jellyfin API key used to trigger library refreshes. |
| `jellyfin.enabled` | When true, Spindle triggers Jellyfin library refreshes after organizing. |

If credentials are missing, the organizer skips Jellyfin refreshes but still
files media correctly.

## Notification & API Settings

- `ntfy_topic` — ntfy.sh topic for workflow notifications (disc inserted,
  rip/encode complete, failures).
- `ntfy_base_url` — Override when self-hosting ntfy.
- `notify_identification` / `notify_rip` / `notify_encoding` /
  `notify_organization` — Per-stage toggles.
- `notify_queue` — Send queue start/finish only when `count >= notify_queue_min_items`
  (default 2) to avoid noise.
- `notify_review` — Notify when an item is diverted to `review_dir`.
- `notify_errors` — Always send errors when true.
- `notify_min_rip_seconds` — Skip rip-complete notifications for cache hits faster
  than this many seconds (default 120).
- `notify_dedup_window_seconds` — Suppress identical notifications within this
  window (default 600s).
- `api_bind` — Bind address for the read-only HTTP API (default `127.0.0.1:7487`).
- `api_tls_cert` / `api_tls_key` — Optional TLS assets when exposing the API on
  your LAN.

## Subtitle & WhisperX Pipeline

| Key | Role |
| --- | --- |
| `subtitles_enabled` | Global toggle; subtitles run only when true. |
| `opensubtitles_enabled` | Enables OpenSubtitles download/clean/align pass. |
| `opensubtitles_api_key` | Required when OpenSubtitles is enabled. |
| `opensubtitles_user_agent` | Your registered UA string; mandated by OpenSubtitles. |
| `opensubtitles_user_token` | Optional JWT for higher daily limits. |
| `opensubtitles_languages` | Preferred ISO 639‑1 codes (e.g. `['en','es']`). |
| `whisperx_cuda_enabled` | Use CUDA 12.8+/cuDNN 9.1 for GPU inference. |
| `whisperx_vad_method` | `silero` (default, offline) or `pyannote` (needs HF token). |
| `whisperx_hf_token` | Required when `whisperx_vad_method = "pyannote"`. |

Set `SPD_DEBUG_SUBTITLES_KEEP=1` before launching the daemon to retain raw
alignment artifacts inside each queue item’s staging folder for debugging.

## Commentary Detection

Commentary detection keeps the primary audio plus qualifying commentary tracks
(English + stereo only). It is conservative by design and drops ambiguous
candidates.

| Key | Role |
| --- | --- |
| `commentary_detection.enabled` | Enable commentary track detection. |
| `commentary_detection.languages` | Allowed languages (ISO 639-1/2 prefixes). |
| `commentary_detection.channels` | Channel count required for candidates (default 2). |
| `commentary_detection.sample_windows` | Number of audio windows sampled across the program. |
| `commentary_detection.window_seconds` | Seconds per analysis window. |
| `commentary_detection.fingerprint_similarity_duplicate` | Similarity threshold used to drop duplicate/downmix tracks. |
| `commentary_detection.speech_ratio_min_commentary` | Minimum speech ratio required for commentary inclusion. |
| `commentary_detection.speech_ratio_max_music` | Maximum speech ratio for music-only exclusion. |
| `commentary_detection.speech_overlap_primary_min` | Minimum overlap with primary speech for mixed commentary. |
| `commentary_detection.speech_in_silence_max` | Maximum speech in primary silence before flagging AD. |
| `commentary_detection.duration_tolerance_seconds` | Absolute duration tolerance vs primary (seconds). |
| `commentary_detection.duration_tolerance_ratio` | Relative duration tolerance vs primary (ratio). |

Set `SPD_DEBUG_COMMENTARY_KEEP=1` to retain commentary analysis artifacts in the
staging directory for inspection.
Set `SPD_DEBUG_COMMENTARY_VERBOSE=1` to emit per-candidate commentary debug logs.

## Audio, Encoding, and Dependencies

- `drapto_path` — Override when Drapto is not on `PATH`.
- `makemkv_path` — Custom path to `makemkvcon` if needed.
- `keydb_path`, `keydb_download_url`, `keydb_download_timeout` — controls for
  refreshing `KEYDB.cfg` (AACS keys). Drop a manual file at
  `~/.config/spindle/keydb/KEYDB.cfg` to seed the cache.
- `keydb_auto_refresh` — When true, Spindle fetches updates automatically.
- `preset_decider_enabled`, `preset_decider_model`, `preset_decider_base_url`,
  `preset_decider_api_key`, `preset_decider_referer`, `preset_decider_title`,
  `preset_decider_timeout_seconds` —
  configure the OpenRouter-powered preset selector. Defaults point at
  `google/gemini-3-flash-preview`; supply an API key via config or
  `OPENROUTER_API_KEY`.
- Custom FFmpeg/FFprobe builds are picked up automatically when they are in
  `PATH`. Use `SPINDLE_FFMPEG_PATH`/`FFMPEG_PATH` or
  `SPINDLE_FFPROBE_PATH`/`FFPROBE_PATH` only when you need to override PATH
  resolution (systemd services, CI, or multiple installs).

## Queue & Workflow Toggles

| Key | Details |
| --- | --- |
| `rip_cache_enabled` | Enables reuse of MakeMKV output. Combine with `rip_cache_max_gib`. |
| `rip_cache_max_gib` | Absolute size cap for the cache. |
| `max_parallel_encodes` | Limits concurrent Drapto jobs (default 1). |
| `max_parallel_rips` | Non-zero enables overlapping MakeMKV jobs when hardware allows. |
| `danger_allow_multiple_daemons` | Debug-only; bypasses single-instance lock. Do not set in production. |

## Diagnostics & Advanced Flags

- `log_level` — `info` (default), `debug`, or `trace`.
- `diagnostic_dump_dir` — When set, intermediate artifacts are copied here for
  long-term inspection.
- `enable_profiler` / `profiler_bind` — Exposes Go pprof endpoints; intended for
  development only.

## Tips

- After changing configuration, run `spindle config validate` to catch missing
  directories or malformed TOML before restarting the daemon.
- Keep `staging_dir` and `rip_cache_dir` on SSD/NVMe storage; encoding churns on
  these paths heavily.
- Store TMDB and OpenSubtitles credentials in a password manager; the daemon
  reads the config in plain text.
- Back up `~/.config/spindle/` (config + tokens) alongside the queue database in
  `~/.local/share/spindle/` if you move hosts.

Need more? See `docs/workflow.md` for the lifecycle, `docs/cli.md` for command
syntax, and `docs/api.md` for the HTTP endpoints.
