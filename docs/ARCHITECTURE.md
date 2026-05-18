# Spindle Architecture

Status: Active contract.

Spindle automates the path from optical disc to a Jellyfin-ready media library:
disc detection, identification, ripping, encoding, optional audio/subtitle work,
organization, Jellyfin refresh, and notifications.

Spindle is a personal, single-operator system. Prefer clear code, explicit
logging, and straightforward recovery over general-purpose orchestration.

## Process model

Spindle ships as one Go binary with several roles:

- **CLI**: starts/stops/restarts the daemon, inspects and mutates the queue,
  tails/query logs, runs utility workflows, and performs recovery operations.
- **Daemon**: owns disc detection, queue execution, the HTTP API, log files, and
  notifications.
- **HTTP API**: local control/read API used by the CLI and external consumers
  such as Flyer. Normal queue access goes through this API; the daemon owns
  queue database reads and mutations.

The daemon always listens on a Unix socket. Optional TCP binding is available
through `api.bind`, but the default deployment is local-only.

## Pipeline

Queue items move through these stages in order:

| Stage | Purpose | Notes |
|-------|---------|-------|
| `identification` | Scan the disc, infer movie/TV, resolve metadata, build RipSpec | Uses MakeMKV, TMDB, optional BDInfo, KeyDB, and disc ID cache |
| `ripping` | Copy selected disc titles into staging | Uses MakeMKV or restores from rip cache; pauses disc detection while the drive is in use |
| `episode_identification` | Map TV ripped titles to canonical episodes | Skipped for movies and non-TV items |
| `encoding` | Encode ripped media to AV1 through Drapto | Reloads `[encoding]` config before each encode and persists telemetry snapshots |
| `audio_analysis` | Refine audio and optionally detect commentary | Commentary detection is controlled by config and uses WhisperX/LLM when available |
| `subtitling` | Optionally generate display SRTs and forced SRTs | Final Jellyfin-facing subtitles are SRT; muxing into MKV is configurable |
| `organizing` | Copy/move outputs to library or review and refresh Jellyfin | Cleans staging after successful routing |
| `completed` | Terminal success state | Queue item can be cleared |
| `failed` | Terminal failure state until retry/clear | Retains failed stage and error message when the failure came from a stage |

Review is distinct from failure. Review means Spindle produced output but the
operator should inspect it before treating it as normal library output. Failure
means the pipeline could not complete the current stage or the user explicitly
stopped the item.

## Queue and persistence

The queue database is SQLite at `{state_dir}/queue.db` with WAL enabled. It is
transient and holds in-flight or recently completed jobs, not long-term library
state. There are no migrations and no schema versioning; if the schema changes,
clear the database.

The queue stores:

- Current stage and `in_progress` flag.
- Failure, user-stop, and review state.
- Progress fields, active episode key, byte-copy counters, and encoding snapshot
  JSON.
- Disc fingerprint and metadata JSON.
- The RipSpec envelope as opaque JSON text.

Implementation source: `internal/queue`.

## RipSpec envelope

RipSpec is the cross-stage work envelope serialized into the queue item. It
contains stable context such as metadata, disc fingerprint, disc titles, TV
episode mapping, generated assets, audio/subtitle/content-ID attributes, and
per-asset status.

The Go type is the source of truth for exact fields and helper methods:
`internal/ripspec/ripspec.go`.

## Concurrency and recovery

The workflow manager polls the queue and starts stage workers. Semaphore capacity
is one per scarce resource:

- disc: identification and ripping
- encoder: Drapto encoding
- WhisperX: episode ID, audio analysis, and subtitle generation

This means multiple items can be in flight at different stages, but only one
stage using a given scarce resource runs at a time.

Workflow delegates lifecycle finalization to `internal/stage`. Each handler gets
a `stage.Session` and uses it for queue-visible state: RipSpec persistence,
progress, active episode bookkeeping, asset status, and review state. Handlers
receive a context and must stop cleanly when it is canceled.

On daemon startup and shutdown, any item left `in_progress` is reset so it can be
picked up again. Stages are expected to be resumable or to clean stale partial
outputs before rerunning.

## Filesystem layout

Important paths are configured or derived from config:

| Path | Purpose |
|------|---------|
| `staging_dir` | Per-disc working directories keyed by uppercase fingerprint, or `queue-{id}` when no fingerprint exists |
| `library_dir` | Root containing configured movie and TV subdirectories |
| `review_dir` | Outputs that need operator review, grouped by review reason and fingerprint prefix |
| `state_dir` | Queue DB and daemon logs |
| XDG runtime dir, with `/tmp` fallback | Daemon Unix socket and lock file |
| XDG cache dir | Rip cache, disc ID cache file, and created OpenSubtitles cache directory |

Default final naming is owned by `internal/mediameta`: movies are placed under
`{library_dir}/{movies_dir}/Title (Year)/Title (Year).mkv`; TV episodes are
placed under `{library_dir}/{tv_dir}/Show/Season NN/Show - S01E01.mkv` (or an
episode range such as `S01E01-E02`).

Exact path defaults and config parsing live in `internal/config`, the sample
produced by `spindle config init`, and [CONFIG.md](CONFIG.md).

## External dependencies

Daemon status checks currently probe these command-line tools:

- `makemkvcon` for disc scan/rip.
- `ffmpeg` and `ffprobe` for media inspection and transformation.
- `mkvmerge` for subtitle muxing and subtitle-track inspection.

Feature-specific tools and services are used when configured:

- `uvx`/WhisperX for subtitles, commentary analysis, and TV episode
  identification.
- `bd_info` for improved Blu-ray identification.
- KeyDB catalog input for improved title resolution.
- TMDB, OpenSubtitles, OpenRouter/LLM, Jellyfin, and ntfy.

## LLM usage

Spindle uses the configured OpenRouter-compatible LLM client for two active
features:

- **TV episode identification verification**: after deterministic transcript
  matching proposes an ambiguous rip/reference pair, the LLM answers whether the
  two bounded transcript excerpts are from the same episode. It does not choose
  from an entire series or invent episode numbers.
- **Commentary detection**: when `commentary.enabled = true`, candidate
  non-primary audio tracks are transcribed and the LLM classifies each candidate
  as commentary or not-commentary. Classification/transcription failures are
  conservative: suspect tracks are preserved as commentary rather than stripped.

Spindle does not use LLMs for TMDB search, disc title resolution, encoding,
subtitle formatting, forced subtitle lookup, Jellyfin refresh, or queue control.
The proposal in `docs/proposals/LLM_EPISODE_CANDIDATE_PICKER.md` is not active
behavior.

## Package map

The current package layout is intentionally simple:

- `cmd/spindle`: CLI entry point and command definitions.
- `internal/config`, `queue`, `ripspec`, `stage`, `mediameta`: core
  configuration, data, naming, and stage abstractions.
- `internal/daemonrun`, `daemonctl`, `workflow`: daemon runtime, process
  control, and workflow orchestration.
- `internal/identify`, `ripper`, `contentid`, `encoder`, `audioanalysis`,
  `subtitle`, `organizer`: stage handlers.
- `internal/makemkv`, `tmdb`, `opensubtitles`, `llm`, `jellyfin`, `notify`,
  `keydb`: external clients and integrations.
- `internal/media/*`, `transcription`, `ripcache`, `discidcache`, `staging`,
  `fingerprint`, `discmonitor`: domain services.
- `internal/httpapi`, `sockhttp`, `queueaccess`, `queueops`, `logs`,
  `auditgather`: HTTP control, daemon access, structured logging, and
  diagnostics.

See [DEVELOPMENT.md](DEVELOPMENT.md) for dependency rules and change policy.
