# Spindle Configuration Guide

Complete reference for `~/.config/spindle/config.toml`. See the [README](../README.md) for initial setup.

Changes require restarting the daemon (`spindle stop && spindle start`).

## API Keys and Secrets

**Environment variables always override config file values for API keys.** This is a security best practice that allows you to:

- Keep secrets out of config files that might be accidentally shared or committed
- Use different credentials in development vs production
- Integrate with secret managers and container orchestration

| Config Key | Environment Variable(s) |
| --- | --- |
| `paths.api_token` | `SPINDLE_API_TOKEN` |
| `tmdb.api_key` | `TMDB_API_KEY` |
| `jellyfin.api_key` | `JELLYFIN_API_KEY` |
| `subtitles.opensubtitles_api_key` | `OPENSUBTITLES_API_KEY` |
| `subtitles.opensubtitles_user_token` | `OPENSUBTITLES_USER_TOKEN` |
| `subtitles.whisperx_hf_token` | `HUGGING_FACE_HUB_TOKEN` or `HF_TOKEN` |
| `preset_decider.api_key` | `PRESET_DECIDER_API_KEY`, `OPENROUTER_API_KEY`, or `DEEPSEEK_API_KEY` |

If an environment variable is set, the config file value is ignored for that key.

## Core Paths & Storage

| Key | Purpose | Notes |
| --- | --- | --- |
| `library_dir` | Final Jellyfin-ready library root. | Must exist; Spindle creates `movies/` & `tv/` subdirs when absent. |
| `staging_dir` | Work area for rips, encodes, subtitles, logs. | Prefer fast storage; large temporary files live here. |
| `review_dir` | Destination for items flagged `NeedsReview`. | Defaults to `~/review`; contents are safe to rename manually. |
| `log_dir` | Persistent logs plus the queue DB. | Ensure enough space for SQLite + log rotation. |
| `logging.retention_days` | Days to keep daemon/item logs before pruning. | Default 60; set 0 to disable automatic cleanup. |
| `rip_cache_dir` | Optional cache of MakeMKV output. | Enable with `rip_cache_enabled = true`. |

Spindle enforces a 20% free-space floor on the rip cache. Tune
`rip_cache_max_gib` to cap cache size.

Daemon and item logs are pruned automatically when they exceed
`logging.retention_days` (default 60). Set the value to `0` to retain logs
indefinitely.

## Identification & Metadata

| Key | Description |
| --- | --- |
| `tmdb.api_key` | *(required)* TMDB API key from https://www.themoviedb.org/settings/api. |
| `tmdb.language` | ISO 639-1 code for metadata (default `en`). |
| `tmdb.base_url` | Override TMDB API base URL (rarely needed). |
| `validation.min_vote_count_exact_match` | Minimum TMDB votes for exact title match acceptance. |

Spindle automatically uses `bd_info` when available to enhance disc metadata.
If discs often appear as "UNKNOWN" or "INDEX_BDMV", install `bd_info` from
libbluray utilities (`libbluray-bin`, `libbluray-utils`, etc.) and ensure
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
  Use `0.0.0.0:7487` to allow remote connections.
- `api_token` — Bearer token for API authentication. When set, all API requests
  must include `Authorization: Bearer <token>`. Generate with `openssl rand -hex 32`.
  Can also be set via `SPINDLE_API_TOKEN` environment variable.
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
alignment artifacts inside each queue item's staging folder for debugging.

## Audio, Encoding, and Dependencies

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

## Commentary Detection

| Key | Description |
| --- | --- |
| `commentary.enabled` | Enable commentary track detection during audio analysis stage. |
| `commentary.whisperx_model` | Model for transcription (default: `large-v3-turbo`). |
| `commentary.similarity_threshold` | Cosine similarity above which a track is considered a stereo downmix (default: 0.92). |
| `commentary.confidence_threshold` | LLM confidence required to classify as commentary (default: 0.80). |
| `commentary.api_key` | OpenRouter API key (falls back to `preset_decider.api_key`). |
| `commentary.base_url` | API endpoint (falls back to `preset_decider.base_url`). |
| `commentary.model` | LLM model for classification (falls back to `preset_decider.model`). |

Commentary detection uses WhisperX to transcribe audio tracks and an LLM to
classify whether the content is commentary. Detected commentary tracks are
excluded from the final output.

## Rip Cache

| Key | Details |
| --- | --- |
| `rip_cache.enabled` | Enables reuse of MakeMKV output. |
| `rip_cache.dir` | Cache directory path. |
| `rip_cache.max_gib` | Absolute size cap for the cache. |

## Workflow Timing

| Key | Details |
| --- | --- |
| `workflow.queue_poll_interval` | Seconds between queue polls (default 5). |
| `workflow.error_retry_interval` | Seconds before retrying after transient errors. |
| `workflow.heartbeat_interval` | Seconds between heartbeat updates for in-progress items. |
| `workflow.heartbeat_timeout` | Seconds before an item is considered stuck. |
| `workflow.disc_monitor_timeout` | Seconds to wait when polling for disc presence. |

## Logging & Diagnostics

| Key | Details |
| --- | --- |
| `logging.level` | Log level: `debug`, `info` (default), `warn`, `error`. |
| `logging.format` | Output format: `console` or `json`. |
| `logging.retention_days` | Days to keep logs before pruning (default 60; 0 disables). |
| `logging.stage_overrides` | Per-stage log level overrides (e.g., `{encoding = "debug"}`). |

## Validation

| Key | Details |
| --- | --- |
| `validation.enforce_drapto_validation` | Fail encoding if Drapto validation fails. |
| `validation.min_vote_count_exact_match` | Minimum TMDB votes for exact title match acceptance. |

## Tips

- After changing configuration, run `spindle config validate` to catch missing
  directories or malformed TOML before restarting the daemon.
- Keep `staging_dir` and `rip_cache_dir` on SSD/NVMe storage; encoding churns on
  these paths heavily.
- Prefer setting API keys via environment variables rather than config file.
  This keeps secrets out of files and works better with containers and secret managers.
- If you must use config file for keys, ensure `chmod 600 ~/.config/spindle/config.toml`.
- Back up `~/.config/spindle/` (config + tokens) alongside the queue database in
  `~/.local/share/spindle/` if you move hosts.

Need more? See `docs/workflow.md` for the lifecycle, `docs/cli.md` for command
syntax, and `docs/api.md` for the HTTP endpoints.
