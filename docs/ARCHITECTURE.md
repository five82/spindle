# Spindle Architecture

Status: Active contract.

Spindle automates the path from optical disc to a Jellyfin-ready media library:
disc detection, identification, ripping, encoding, optional audio/subtitle work,
organization, Jellyfin refresh, and notifications.

Spindle is a personal, single-operator system. Prefer clear code, explicit
logging, and straightforward recovery over general-purpose orchestration.

## Process model

Spindle ships as one Go binary with several roles:

- **CLI**: starts/stops the daemon, inspects and mutates the queue, tails logs,
  runs utility workflows, and performs recovery operations.
- **Daemon**: monitors the optical drive, owns pipeline execution, exposes the
  HTTP API, writes logs, and sends notifications.
- **HTTP API**: local control/read API used by the CLI and by external consumers
  such as Flyer.
- **Direct DB fallback**: queue read/mutation commands can work when the daemon
  is unavailable by opening the queue database directly where safe.

The daemon normally listens on a Unix socket. TCP binding exists for explicit
configuration but the default deployment is local-only.

## Pipeline

Queue items move through these stages in order:

| Stage | Purpose | Notes |
|-------|---------|-------|
| `identification` | Scan the disc, infer movie/TV, resolve metadata, build RipSpec | Uses MakeMKV, TMDB, optional KeyDB/disc ID cache |
| `ripping` | Copy selected disc titles into staging | Disc-dependent; pauses disc monitor while active |
| `episode_identification` | Map TV ripped titles to canonical episodes | Skipped for movies |
| `encoding` | Encode ripped media to AV1 through Drapto | Persists encoding progress snapshots |
| `audio_analysis` | Refine audio and optionally detect commentary | Commentary detection is controlled by config |
| `subtitling` | Optionally generate display SRT and forced subtitles | Final Jellyfin-facing subtitles are SRT |
| `organizing` | Copy/move outputs to library or review and refresh Jellyfin | Cleans staging after successful routing |
| `completed` | Terminal success state | Queue item can be cleared |
| `failed` | Terminal failure state until retry/stop/clear | Retains failed stage and error message |

Review is distinct from failure. Review means Spindle produced an output but the
operator should inspect it before treating it as normal library output. Failure
means the pipeline could not complete the current stage.

## Queue and persistence

The queue database is SQLite at `{state_dir}/queue.db`. It is transient and holds
in-flight or recently completed jobs, not long-term library state. There are no
migrations and no schema versioning; if the schema changes, clear the database.

The queue stores:

- Current stage and `in_progress` flag.
- Error/review state.
- Progress fields and encoding snapshot JSON.
- Disc fingerprint and metadata.
- The RipSpec envelope as opaque JSON text.
- Convenience paths for ripped, encoded, and final files.

Implementation source: `internal/queue`.

## RipSpec envelope

RipSpec is the cross-stage work envelope serialized into the queue item. It
contains stable context such as metadata, disc fingerprint, titles, TV episode
mapping, generated assets, and cross-stage attributes.

The Go type is the source of truth for exact fields and helper methods:
`internal/ripspec/ripspec.go`.

## Concurrency and recovery

Spindle runs a single logical queue pipeline and uses semaphores to protect
scarce resources such as the optical drive, encoder, and WhisperX work. Stage
handlers receive a context and must stop cleanly when it is canceled.

On daemon startup, any item left `in_progress` from a crash is returned to a
retryable state for its current stage. Stages are expected to be resumable or to
clean stale partial outputs before rerunning.

## Filesystem layout

Important paths are configured or derived from config:

| Path | Purpose |
|------|---------|
| `staging_dir` | Per-disc working directories keyed by fingerprint |
| `library_dir` | Root for movie and TV library outputs |
| `review_dir` | Outputs that need operator review |
| `state_dir` | Queue DB and daemon logs |
| XDG runtime dir, with `/tmp` fallback | Daemon Unix socket and lock file |
| XDG cache dir | Rip cache, disc ID cache file, OpenSubtitles cache directory |

Exact path defaults and config parsing live in `internal/config` and the sample
produced by `spindle config init`.

## External dependencies

Dependency preflight checks currently expect these command-line tools:

- `makemkvcon` for disc scan/rip.
- `ffmpeg` and `ffprobe` for media inspection and transformation.
- `mkvmerge` for subtitle muxing and subtitle-track inspection.

Feature-specific tools:

- `uvx`/WhisperX for transcription-based subtitles, commentary analysis, and TV
  episode identification.
- `bd_info` is optional input for improved Blu-ray identification.
- A KeyDB catalog is optional input for improved title resolution.

External services are configured as needed: TMDB, OpenSubtitles, OpenRouter/LLM,
Jellyfin, and ntfy.

## Package map

The current package layout is intentionally simple:

- `cmd/spindle`: CLI entry point and command definitions.
- `internal/config`, `queue`, `ripspec`, `stage`: core configuration, data, and
  stage abstractions. `stage.Session` centralizes per-stage RipSpec persistence,
  progress updates, active episode bookkeeping, and review-state mutation.
- `internal/daemon`, `daemonrun`, `daemonctl`, `workflow`, `stageexec`: runtime
  orchestration and daemon control.
- `internal/identify`, `ripper`, `contentid`, `encoder`, `audioanalysis`,
  `subtitle`, `organizer`: stage handlers.
- `internal/makemkv`, `tmdb`, `opensubtitles`, `llm`, `jellyfin`, `notify`,
  `keydb`: external clients.
- `internal/media/*`, `transcription`, `ripcache`, `discidcache`, `staging`,
  `fingerprint`, `discmonitor`: domain services.
- `internal/httpapi`, `sockhttp`, `queueaccess`, `queueops`, `logs`,
  `auditgather`: control, access, and diagnostics.

See [DEVELOPMENT.md](DEVELOPMENT.md) for dependency rules and change policy.
