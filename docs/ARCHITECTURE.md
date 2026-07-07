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

The pipeline is a per-item DAG, not a linear sequence. Each stage below is one
scheduler task; `Depends on` lists the template edges registered in
`internal/daemonrun`:

| Stage | Depends on | Claims | Purpose |
|-------|------------|--------|---------|
| `identification` | - | drive | Scan the disc, infer movie/TV, resolve metadata, build RipSpec (MakeMKV, TMDB, optional BDInfo, KeyDB, disc ID cache) |
| `ripping` | identification | drive | Copy selected disc titles into staging with MakeMKV, or restore from rip cache |
| `episode_identification` | ripping | gpu (TV only) | Map TV ripped titles to canonical episodes; a claim-free no-op for movies |
| `encoding` | identification | encode | Encode to AV1 through Reel target-quality mode in a `spindle encode-worker` subprocess. Runs in parallel with ripping, streaming completed ripped assets as they land |
| `analysis` | episode_identification | gpu | Per-episode commentary detection from RIPPED audio (config-gated, WhisperX/LLM) |
| `subtitling` | analysis | gpu | Generate display SRTs from ripped audio/transcript artifacts into staging; generation only, no muxing |
| `apply` | subtitling, encoding | - | Joins both branches; owns every write to encoded files: audio refinement, commentary disposition, duration validation, subtitle muxing |
| `organizing` | apply | - | Copy/move outputs to library or review, refresh Jellyfin, clean staging |
| `completed` | | | Terminal success state |
| `failed` | | | Terminal failure state until retry/clear; retains failed stage and error message |

The analysis branch (episode ID, commentary, subtitle generation) reads ripped
sources, so it runs concurrently with encoding. Interleaved log timelines for
one item are normal, not disorder.

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

- Current stage and `in_progress` flag (the scheduler's coarse position; it
  lags running tasks during overlap windows).
- Failure, user-stop, and review state.
- Encoding snapshot JSON.
- Disc fingerprint and metadata JSON.
- The RipSpec envelope as opaque JSON text.
- A `tasks` table: one row per pipeline stage per item with state
  (pending/running/done/failed/skipped), attempts, dependency edges, and
  per-task progress (percent, message, byte counters, active asset key).
  Task rows are a projection of `(template, item stage)`: retry and
  stage moves delete them and the scheduler recompiles lazily.

Implementation source: `internal/queue`.

## RipSpec envelope

RipSpec is the cross-stage work envelope serialized into the queue item. It
contains stable context such as metadata, disc fingerprint, disc titles, TV
episode mapping, generated assets, audio/subtitle/content-ID attributes, and
per-asset status.

The Go type is the source of truth for exact fields and helper methods:
`internal/ripspec/ripspec.go`.

## Concurrency and recovery

The workflow manager runs a task scheduler: it dispatches any task whose
dependency edges are done and whose resource claims fit the configured
budgets, waking on task completion with a timer fallback. Resource budgets
are capacity 1 each:

- `drive`: identification and ripping (rip-cache restores skip the drive work
  but the stage still holds the claim only briefly for validation)
- `gpu`: WhisperX work — episode ID (TV only), analysis, subtitling
- `encode`: Reel target-quality encoding. Exactly one encode runs at a time;
  cross-tier (1080p+4K) pairing was removed after concurrent CVVDP metric
  pools exhausted VRAM (see ADR 0002)

Multiple items can be in flight, and one item's encoding runs concurrently
with its ripping and analysis branches. Encodes execute in a
`spindle encode-worker` subprocess (same binary re-executed) streaming
reporter events as JSON lines, so a cgo crash fails one job instead of the
daemon.

Workflow delegates lifecycle finalization to `internal/stage`. Each handler
gets a `stage.Session` bound to its task row and uses it for queue-visible
state: RipSpec persistence, per-task progress, asset status, and review
state. Because branch handlers overlap on one item, envelope writes go
through merge operations (`MergeSave` and helpers) under a per-item lock —
whole-envelope saves from overlapping handlers would be last-writer-wins.
Only the handler running a task writes that task's progress columns.
Handlers receive a context and must stop cleanly when it is canceled.

On daemon startup and shutdown, running tasks and `in_progress` flags are
reset so items can be picked up again. Tasks are idempotent: they detect
existing output or clean stale partial output before rerunning.

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

Daemon status checks currently probe these command-line tools and native libraries:

- `makemkvcon` for disc scan/rip.
- `ffprobe` for Spindle-side media inspection, validation, stream selection, and audit data.
- `ffmpeg` for Spindle-side audio extraction, metadata/disposition remuxes, and debug diagnostics.
- `mkvmerge` for subtitle muxing and subtitle-track inspection.
- Reel native encoding libraries: SVT-AV1, FFmpeg libraries, libopusenc, and libvship.

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
- **Subtitle audit**: after the display SRT is formatted, a single JSON-mode
  call reviews all cues and proposes remove/replace edits in fixed error
  categories (hallucination, credits music, music bleed, garbled, homophone,
  broken, repeated, encoding). It is an improver, not a gate: edits are
  resolved by exact cue-text match rather than trusting model indices, high-risk
  removals are capped and trigger a whole-response rejection plus review flag if
  exceeded, and any missing config, API error, or rejected response just skips
  the audit with a warning. See ADR 0003.

Spindle does not use LLMs for TMDB search, disc title resolution, encoding,
Jellyfin refresh, or queue control.
The proposal in `docs/proposals/LLM_EPISODE_CANDIDATE_PICKER.md` is not active
behavior.

## Package map and dependency rules

The current package layout is intentionally simple:

- `cmd/spindle`: CLI entry point and command definitions.
- `internal/config`, `queue`, `ripspec`, `stage`, `mediameta`: core
  configuration, data, naming, and stage abstractions.
- `internal/daemonrun`, `daemonctl`, `workflow`: daemon runtime, process
  control, and workflow orchestration.
- `internal/identify`, `ripper`, `contentid`, `encoder`, `audioanalysis`
  (analysis and apply stages), `subtitle`, `organizer`: stage handlers.
- `internal/makemkv`, `tmdb`, `opensubtitles`, `llm`, `jellyfin`, `notify`,
  `keydb`: external clients and integrations.
- `internal/media/*`, `transcription`, `ripcache`, `discidcache`,
  `stagingdir`, `fingerprint`, `discmonitor`: domain services.
- `internal/httpapi`, `sockhttp`, `queueaccess`, `queueops`, `logs`,
  `auditgather`: HTTP control, daemon access, structured logging, and
  diagnostics.

Dependencies flow from lower layers toward orchestration and CLI:

1. Foundation utilities (`logs`, `textutil`, `srtutil`, `fileutil`,
   `language`, `encodingstate`, `deps`, `mediameta`).
2. Data and boundaries: `config`, `queue`, `ripspec`, `stage`.
3. Domain services and clients: external clients, media helpers, caches,
   transcription, staging, fingerprinting, disc monitoring.
4. Stage handlers.
5. Orchestration and access: `workflow`, `httpapi`, `sockhttp`,
   `queueaccess`, `queueops`, `auditgather`.
6. Daemon control/runtime.
7. CLI entry point.

Prohibited: stage handlers importing each other; `queue` importing `ripspec`
(the store treats RipSpec as opaque text); `config` importing client
packages; anything importing `cmd/spindle`.

See [AGENTS.md](../AGENTS.md) for change policy and the complexity budget.
