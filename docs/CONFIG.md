# Spindle Configuration

Status: Active contract / implementation reference.

The generated sample from `spindle config init` is the complete option list and
contains current defaults. This document summarizes the behavior operators need
to rely on.

## Loading order

Most commands load and validate configuration before running. `spindle config init`
skips config loading so it can create the first file. The internal
`spindle daemon` subcommand bypasses root pre-loading and loads config inside the
daemon process.

By default, `spindle config init` writes to
`$XDG_CONFIG_HOME/spindle/config.toml` when `XDG_CONFIG_HOME` is set, otherwise
`~/.config/spindle/config.toml`. Pass `--config` when you want commands to read a
file outside the loader search path.

Configuration is loaded in this order:

1. `--config` / `-c` explicit path.
2. `~/.config/spindle/config.toml`.
3. `./spindle.toml`.
4. Built-in defaults, which still fail validation unless required secrets are
   supplied through environment variables.

Path values support leading `~/` and are normalized to absolute paths.

Environment variables override file values for secrets and tokens:

| Variable | Config field |
|----------|--------------|
| `TMDB_API_KEY` | `tmdb.api_key` |
| `JELLYFIN_API_KEY` | `jellyfin.api_key` |
| `OPENROUTER_API_KEY` | `llm.api_key` |
| `SPINDLE_API_TOKEN` | `api.token` |
| `HUGGING_FACE_HUB_TOKEN` | `subtitles.whisperx_hf_token` |
| `HF_TOKEN` | `subtitles.whisperx_hf_token` fallback |
| `OPENSUBTITLES_API_KEY` | `subtitles.opensubtitles_api_key` |
| `OPENSUBTITLES_USER_TOKEN` | `subtitles.opensubtitles_user_token` |

## Validation

`spindle config validate` validates the loaded config and creates required
runtime directories. Required fields are:

- `tmdb.api_key`
- `paths.staging_dir`
- `paths.state_dir`
- `paths.review_dir`

Conditional requirements:

- `jellyfin.url` and `jellyfin.api_key` are required when `jellyfin.enabled = true`.
- `subtitles.opensubtitles_api_key` is required when
  `subtitles.opensubtitles_enabled = true`.
- `subtitles.whisperx_hf_token` is required when subtitle generation is enabled
  with a non-`silero` VAD method.

Encoding CRF, SVT-AV1 preset, and MakeMKV timeouts must be within valid ranges.
Content-ID thresholds must be between 0 and 1 with
`decisive_auto_accept_threshold` above `low_confidence_review_threshold` and at
or below `clear_confidence_threshold`. Exact ranges and defaults are in the
generated sample config.

## Derived paths

Spindle derives several paths from the configured base directories:

| Derived path | Source |
|--------------|--------|
| Queue DB | `{state_dir}/queue.db` |
| Active daemon log link | `{state_dir}/daemon.log` |
| Timestamped daemon logs | `{state_dir}/spindle-*.log` |
| Unix socket | `$XDG_RUNTIME_DIR/spindle.sock`, or `/tmp/spindle.sock` |
| Daemon lock | `$XDG_RUNTIME_DIR/spindle.lock`, or `/tmp/spindle.lock` |
| Rip cache | XDG cache dir + `/spindle/rips` |
| Disc ID cache | XDG cache dir + `/spindle/discid_cache.json` |
| OpenSubtitles cache directory | XDG cache dir + `/spindle/opensubtitles` |

`library_dir` is optional at validation time and is created best-effort by
`config validate`; final organization still fails if the configured library path
cannot be used.

## Feature gates

Important feature switches:

- `subtitles.enabled` enables the subtitle stage. `subtitles.mux_into_mkv`
  controls whether generated SRTs are muxed into MKV output.
- `subtitles.opensubtitles_enabled` controls explicit OpenSubtitles lookups from
  `spindle gensubtitle` for regular or forced subtitles. TV episode
  identification uses the OpenSubtitles API key directly and is not gated by
  this switch.
- `rip_cache.enabled` enables automatic raw-rip restore/store during normal
  ripping.
- `disc_id_cache.enabled` enables daemon use of the Blu-ray disc ID cache during
  identification. The standalone `identify` and `cache rip` utilities may still
  open the cache directly for diagnostics/one-shot work.
- `commentary.enabled` enables commentary detection, which also needs
  `llm.api_key` and WhisperX support.
- `llm.api_key` enables the OpenRouter-compatible LLM client used only for TV
  episode-pair verification and commentary classification.

Only the `[encoding]` section is re-read automatically: it is reloaded from disk
before each encode so preset/CRF changes can take effect without restarting the
daemon. Other config changes require restarting the daemon or retrying affected
items as appropriate.
