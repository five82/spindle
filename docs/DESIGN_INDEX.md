# Design Document Index

Status: Normative index.

Complete specification for Spindle's current implementation. Each document owns a
single domain area; avoid duplicating detailed behavior across documents. When a
field/schema is backed by Go structs, the referenced source file is the final
implementation source of truth.

Documentation status markers:
- **Normative spec**: expected design behavior for implementation work.
- **User guide**: operational guidance; defer to normative specs for internals.
- **Proposal**: non-normative until accepted into the design specs.

## System Design

| Document | Owns |
|----------|------|
| [DESIGN_OVERVIEW.md](DESIGN_OVERVIEW.md) | Product scope, external dependencies, services, architecture, configuration, filesystem layout |
| [DESIGN_QUEUE.md](DESIGN_QUEUE.md) | SQLite queue database schema, item model, stage model, store operations |
| [DESIGN_RIPSPEC.md](DESIGN_RIPSPEC.md) | RipSpec envelope structure, metadata, titles, episodes, assets, attributes, methods |
| [DESIGN_DAEMON.md](DESIGN_DAEMON.md) | Daemon lifecycle, disc detection pipeline, daemon orchestration layer |
| [DESIGN_STAGES.md](DESIGN_STAGES.md) | Stage contracts: inputs, outputs, skip/failure semantics, persistence, major decisions |
| [DESIGN_INFRASTRUCTURE.md](DESIGN_INFRASTRUCTURE.md) | Logging, notifications, dependency checks, shared utilities, log access, audit gathering, config validation, shared transcription service |
| [DESIGN_PACKAGES.md](DESIGN_PACKAGES.md) | Go package layout, dependency rules, module boundaries, key interfaces |
| [DESIGN_TESTING.md](DESIGN_TESTING.md) | Testing strategy, interface boundaries for test doubles, coverage goals |

## API Reference

| Document | Owns |
|----------|------|
| [API_INTERFACES.md](API_INTERFACES.md) | CLI commands, HTTP API endpoints, response schemas |
| [API_SERVICES.md](API_SERVICES.md) | External service/tool protocols: MakeMKV, TMDB, OpenSubtitles, WhisperX, LLM, Jellyfin, ntfy, FFprobe, KeyDB, mkvmerge, Drapto |

## LLM Prompts

| Document | Owns |
|----------|------|
| [DESIGN_LLM_PROMPTS.md](DESIGN_LLM_PROMPTS.md) | Exact system/user prompts, response schemas, trigger conditions, failure behavior for all LLM use cases |

## Content ID Algorithm

| Document | Owns |
|----------|------|
| [CONTENT_ID_DESIGN.md](CONTENT_ID_DESIGN.md) | Episode identification algorithm: transcription, reference acquisition, matching, confidence, review conditions, LLM verification |

## Non-Normative Supporting Docs

| Document | Status | Purpose |
|----------|--------|---------|
| [user/workflow.md](user/workflow.md) | User guide | Operator-facing workflow and recovery guide |
| [proposals/LLM_EPISODE_CANDIDATE_PICKER.md](proposals/LLM_EPISODE_CANDIDATE_PICKER.md) | Deferred proposal | Candidate future LLM fallback design; not implemented |

## Ownership Rules

- Put exact CLI commands and HTTP schemas only in `API_INTERFACES.md`.
- Put external tool/service wire protocols only in `API_SERVICES.md`.
- Put stage inputs, outputs, skip/failure semantics, and decision catalog only
  in `DESIGN_STAGES.md`.
- Put detailed episode matching policy only in `CONTENT_ID_DESIGN.md`.
- Put exact LLM prompts only in `DESIGN_LLM_PROMPTS.md`.
- Put Go struct-backed data models in the implementation first, then summarize
  stable semantics in the owning spec.

## Quick Reference

- Configuration fields and defaults -> DESIGN_OVERVIEW.md Section 5
- Queue stage model -> DESIGN_QUEUE.md Section 4
- RipSpec data model -> DESIGN_RIPSPEC.md
- Disc fingerprinting -> DESIGN_DAEMON.md Section 2.4
- Stage execution lifecycle -> DESIGN_OVERVIEW.md Section 4.6
- Stage cancellation contract -> DESIGN_OVERVIEW.md Section 4.6.1
- Resource semaphores -> DESIGN_OVERVIEW.md Section 4.3
- MakeMKV robot format -> DESIGN_STAGES.md Section 1.1.1
- Audio track selection -> DESIGN_INFRASTRUCTURE.md Section 4.5
- Shared transcription service -> DESIGN_INFRASTRUCTURE.md Section 9
- Forced subtitle timing -> DESIGN_STAGES.md Section 6.6.2
- Subtitle candidate ranking -> DESIGN_STAGES.md Section 6.7
- Go package tree -> DESIGN_PACKAGES.md Section 1
- Dependency layer rules -> DESIGN_PACKAGES.md Section 2
- Test double interfaces -> DESIGN_TESTING.md Section 2
- Error taxonomy -> DESIGN_INFRASTRUCTURE.md Section 5
- CLI command reference -> API_INTERFACES.md Section 1
- HTTP API endpoints -> API_INTERFACES.md Section 2.4
- LLM prompt specifications -> DESIGN_LLM_PROMPTS.md
- External service protocols -> API_SERVICES.md
