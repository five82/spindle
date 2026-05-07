# Spindle Development Guide

Status: Active contract.

This guide defines the expectations for making changes now that Spindle is in a
feature-complete and bugfix-focused phase.

## Core rules

- Use the Go toolchain: `go build`, `go test`, `golangci-lint`, and
  `./check-ci.sh`.
- Do not introduce alternate build systems.
- Do not run `git commit` or `git push` unless explicitly asked.
- Prefer clear, maintainable code over compatibility shims.
- Finish the work you start; do not leave TODOs as a substitute for scope.
- Before handing work back, run `./check-ci.sh` or explain why it could not be
  run.

## Source of truth and docs policy

Spindle no longer keeps exhaustive rewrite specifications in the working tree.
Git history is the archive for those deleted docs.

Current source-of-truth order:

1. Tests define exact behavior where practical.
2. Code defines implementation details.
3. Active docs define user-visible behavior, operational policy, and durable
   architecture boundaries.
4. ADRs explain why major decisions were made.

Update active docs when a change affects users, operators, external consumers,
architecture boundaries, config, output layout, logging/notification contracts,
or external dependencies. For narrow bugfixes and algorithm edge cases, prefer a
focused regression test over prose.

## Testing

Default commands:

```bash
go test ./...
./check-ci.sh
```

`./check-ci.sh` verifies the Go toolchain, tidy module state, CI-equivalent
build without `go.work`, normal tests, race tests, CGO build, lint, and
`govulncheck`.

Testing priorities:

- Algorithms and scoring decisions.
- Parsers for external tool/service output.
- Queue stage transitions and retry behavior.
- RipSpec helpers and asset state changes.
- HTTP API response envelopes and fields used by consumers.
- Config loading, defaults, validation, and generated sample config.
- Subtitle formatting and validation.

Use the standard library test stack unless there is a strong reason otherwise:
`testing`, `httptest`, temp directories, table tests, subtests, and testdata.

## Logging and observability

Operational decisions must be visible at INFO without enabling DEBUG. If a
decision changes what Spindle does next, log it with:

- `decision_type`
- `decision_result`
- `decision_reason`

Level guidance:

| Level | Use for |
|-------|---------|
| INFO | Stage start/complete, track selection, preset choice, skip decisions, fallback logic, cache hits |
| DEBUG | Raw dumps, detailed metrics, internal state that does not change behavior |
| WARN | Degraded behavior; include `event_type`, `error_hint`, and `impact` where useful |
| ERROR | Operation failed; include `event_type`, `error_hint`, and `error` where useful |

Progress messages should follow:

```text
Phase N/M - Action (context)
```

Example: `Phase 2/3 - Ripping selected titles (5 of 12)`.

Decision type constants live in `internal/logs/decision.go` when shared across
packages. Do not keep a separate exhaustive prose catalog.

## Subtitle policy

Spindle's Jellyfin-facing subtitle output is SRT. Do not use PGS subtitles as
final library output. Source subtitle tracks may be inspected for metadata or
forced-subtitle signals, but primary display subtitles are generated or handled
as SRT.

## Package dependency rules

Keep dependencies flowing from lower-level packages toward orchestration and CLI:

1. Foundation: small utilities and shared types (`logs`, `textutil`,
   `srtutil`, `fileutil`, `language`, `encodingstate`, `deps`, `mediameta`).
2. Data and boundaries: `config`, `queue`, `ripspec`, `stage`.
3. Domain services and clients: external clients, media helpers, caches,
   transcription, staging, fingerprinting, disc monitoring.
4. Stage handlers: identify, ripper, contentid, encoder, audioanalysis,
   subtitle, organizer.
5. Orchestration and access: workflow, httpapi, sockhttp, queueaccess,
   queueops, auditgather.
6. Daemon control/runtime.
7. CLI entry point.

Prohibited dependencies:

- Stage handlers must not import each other.
- `queue` must not import `ripspec`; the queue store treats RipSpec as opaque
  text.
- `config` must not import client packages.
- No package may import `cmd/spindle`.

## Drapto local development

Local development uses a gitignored `go.work` to reference `../drapto` directly.
CI uses the version pinned in `go.mod`. After pushing Drapto changes, update
Spindle with:

```bash
go get github.com/five82/drapto@main
go mod tidy
```

## ADRs

Add or update an ADR for major decisions such as:

- New external service or replacement of a major dependency.
- Queue/RipSpec model redesign.
- Persistent storage or migration policy changes.
- Pipeline stage order or lifecycle redesign.
- Subtitle output policy changes.
- Major concurrency/orchestration changes.

ADR files live in `docs/adr/` and should be short: context, decision,
consequences, and status.
