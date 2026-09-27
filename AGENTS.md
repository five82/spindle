# AGENTS.md

## Ground rules

- Go applications use the Go toolchain (`go build`, `go test`, `golangci-lint`).
  `.forgejo/build-native.sh` uses upstream build tools only to prepare cached
  native CI dependencies, not to build the Go applications.
- From the monorepo root, use `./deploy.sh spindle|flyer|reel` for deployments;
  do not reproduce its steps manually. A target is required.
- For a queue schema change, stop Spindle and run `spindle queue clear --all --yes`
  while stopped before `./deploy.sh spindle`: only the stopped-daemon command
  deletes the transient DB files. The deploy script preserves stopped state,
  so run `spindle start` afterward if the daemon should be running.
- Before handing work back, run `./check-ci.sh` (tests, race, CGO, lint, govulncheck) or explain why you couldn't.
- Finish the work you start; ask before dropping scope or leaving TODOs.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters.
- When asked to commit, commit to the current branch - normally `main`. Do not
  create a branch, open a PR, or push elsewhere unless explicitly told to.

## Project

Personal single-operator tool: optical disc -> Loom library (MakeMKV rip,
Reel AV1 target-quality encode, TMDB metadata, OpenSubtitles subtitles synced
against WhisperX transcripts, ntfy).
Feature-complete and in a bugfix phase — avoid over-engineering. Break
forward: no backwards compatibility, no compat layers, no deprecated paths.
Queue writes go through the daemon HTTP API. Stopped-daemon exceptions:
`status` / `queue list` / `queue show` fall back to a direct read-only DB
read, and `queue clear --all` deletes the transient queue DB files.

## Repository

One Go module, `github.com/five82/spindle`, contains:

| Component | Source | Role |
|-----------|--------|------|
| Spindle | `cmd/spindle`, `internal` | Daemon + CLI |
| Flyer | `flyer/` | Read-only HTTP terminal monitor |
| Reel | `reel/` | AV1 encoding library and CLI |

The private Forgejo instance is authoritative; GitHub
(`https://github.com/five82/spindle`) is a one-way public push mirror. Push
development changes to `origin`, not GitHub. Keep private host addresses in
local Git configuration, never in tracked files. Preserve Spindle's existing
history; the mirror uses the original GitHub repository.

Shuttle remains separate at `~/projects/shuttle/`:
[GitHub](https://github.com/five82/shuttle).

## Complexity budget

YAGNI and KISS: build only what the current task requires; when two
approaches work, take the simpler one.

Production LOC should be flat or negative; tests may grow freely. Before any
fix, identify the invariant that makes the bug impossible and what existing
code becomes redundant if it's enforced — prefer deletion and stronger
invariants over additive patches. No new packages, interfaces, exported
symbols, config flags, workers, caches, or abstraction layers unless they
clearly reduce total complexity. Avoid helper sprawl: don't extract
single-use helpers unless they represent a real domain concept. Don't add
configuration to avoid making a design decision. For non-trivial work,
report the production LOC delta, new exported surface, and what was removed
or simplified.

## Behavior and observability

- Preserve user-visible behavior unless intentionally changing it. Removing
  distinct output (log messages, CLI feedback) is a behavior change.
- Every decision that changes what happens next is logged at INFO with
  `decision_type`, `decision_result`, `decision_reason`. WARN includes
  `event_type`, `error_hint`, `impact`; ERROR includes `event_type`,
  `error_hint`, `error`. DEBUG is raw data and metrics, never decisions.
- Progress format: `"Phase N/M - Action (context)"`.

## Hard invariants

- Final display subtitle output is SRT. Never PGS as final library output.
- The queue DB is transient: no migrations, no schema versioning. Schema
  changes mean clear the database.
- `queue` must not import `ripspec` (RipSpec is opaque text to the store);
  stage-handler packages must not import one another; `config` must not import
  client packages. The `apply` stage owns all encoded-file rewrites after the
  encoding and analysis branches join.

## Component boundaries and checks

Spindle embeds `github.com/five82/spindle/reel`; Reel must not import Spindle
internals. Flyer only accesses daemon state through its read-only HTTP client
(and existing local log access), never by importing daemon or encoder packages.
Keep Flyer's build independent of CGO and encoder libraries. No shared utility
or API package is needed merely because the components share a repository.

There is one root `go.mod`, no `go.work`, and no Reel version pin. Run the root
`./check-ci.sh` for all components. It selects `no_vship` without libvship;
local deployment checks must exercise the default VSHIP build. Forgejo runs on
the ARM64 `debian-13` runner, where ThreadSanitizer is unavailable; the AMD64
workstation must pass race detection before deployment.

## Metrics

`<state_dir>/metrics.jsonl` is the durable performance record: one
self-describing JSON object appended per completed item (the queue DB is
transient and daemon logs expire; this file accumulates). Use it to answer
performance questions — stage durations and resource-wait seconds, rip
throughput with `rip.drive_vendor`/`rip.drive_model` identifying the physical
drive, and per-episode encode stats (`encodes[]`: `resolution_class`
2160p/1080p/sd, `speed` as video-seconds per wall-second, `phase_seconds`,
Reel's `target_quality` CRF-search aggregate including the
`ssimu2_calibration_offset` grain/complexity proxy, and `grain_treatment` —
the grain gate's verdict (`treated`, `median_bpp` against its treatment cutoff)
with source-matched grain estimation details and the
`denoise_ceiling_jod_mean`/`_min` that caps a treated title's reported scores).
Records are append-only; fields may be added over time, so
query by field name, not position. Example:

```sh
jq -s '[.[] | .encodes[]] | group_by(.resolution_class)
  | map({class: .[0].resolution_class, mean_speed: (map(.speed) | add/length)})' \
  ~/.local/state/spindle/metrics.jsonl
```

## Documentation

`README.md` is the operator guide. Cobra help and the generated config sample
own command and option reference. Code and tests own implementation and HTTP
behavior. Keep non-obvious rationale beside the constrained code; use git
history for superseded plans and decisions. Do not add implementation docs,
ADRs, or proposals unless the user explicitly asks for them.
