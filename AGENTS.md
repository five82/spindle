# AGENTS.md

## Ground rules

- Do not run `git commit` or `git push` unless explicitly asked.
- Go toolchain only (`go build`, `go test`, `golangci-lint`); no alternate build systems.
- Before handing work back, run `./check-ci.sh` (tests, race, CGO, lint, govulncheck) or explain why you couldn't.
- Finish the work you start; ask before dropping scope or leaving TODOs.
- Coordinate major trade-offs with the user; never unilaterally defer functionality.
- Keep edits ASCII unless the file already uses extended characters.

## Project

Personal single-operator tool: optical disc -> Jellyfin library (MakeMKV rip,
Reel AV1 target-quality encode, TMDB metadata, WhisperX subtitles, ntfy).
Feature-complete and in a bugfix phase — avoid over-engineering. Break
forward: no backwards compatibility, no compat layers, no deprecated paths.
Queue access goes through the daemon HTTP API; the only stopped-daemon
exception is `queue clear --all` (deletes the transient queue DB files).

Related repos: `../reel` (AV1 encoder used as a library),
`../flyer` (read-only TUI, the HTTP API's one consumer).

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

- Jellyfin-facing subtitle output is SRT. Never PGS as final library output.
- The queue DB is transient: no migrations, no schema versioning. Schema
  changes mean clear the database.
- `queue` must not import `ripspec` (RipSpec is opaque text to the store);
  stage-handler packages must not import one another; `config` must not import
  client packages. The `apply` stage owns all encoded-file rewrites after the
  encoding and analysis branches join.

## Reel dependency

Local dev uses a gitignored `go.work` referencing `../reel`; CI uses the
`go.mod` pin and builds reel with `-tags no_vship` (no libvship on the
runner). After pushing reel changes:
`go get codeberg.org/five82/reel@latest && go mod tidy`.

## Documentation

`README.md` is the operator guide. Cobra help and the generated config sample
own command and option reference. Code and tests own implementation and HTTP
behavior. Keep non-obvious rationale beside the constrained code; use git
history for superseded plans and decisions. Do not add implementation docs,
ADRs, or proposals unless the user explicitly asks for them.
