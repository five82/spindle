# Design Document Index

Complete specification for a clean room rewrite of Spindle. Each document is
self-contained and focused on a single domain area.

## System Design

| Document | Contents | ~Lines |
|----------|----------|--------|
| [DESIGN_OVERVIEW.md](DESIGN_OVERVIEW.md) | Introduction, external dependencies, services, architecture, configuration, filesystem layout | 620 |
| [DESIGN_QUEUE.md](DESIGN_QUEUE.md) | SQLite queue database: schema, item model, stage model, store operations | 295 |
| [DESIGN_RIPSPEC.md](DESIGN_RIPSPEC.md) | RipSpec envelope: structure, metadata, titles, episodes, assets, attributes, methods | 210 |
| [DESIGN_DAEMON.md](DESIGN_DAEMON.md) | Daemon lifecycle, disc detection pipeline, daemon orchestration layer | 350 |
| [DESIGN_STAGES.md](DESIGN_STAGES.md) | All 7 pipeline stages: identification, ripping, episode ID, encoding, audio analysis, subtitles, organization | 925 |
| [DESIGN_INFRASTRUCTURE.md](DESIGN_INFRASTRUCTURE.md) | Logging, notifications, preflight, shared utilities, log access, audit gathering, config validation, shared transcription service | 620 |
| [DESIGN_PACKAGES.md](DESIGN_PACKAGES.md) | Go package layout, dependency rules, module boundaries, key interfaces | 170 |
| [DESIGN_TESTING.md](DESIGN_TESTING.md) | Testing strategy, interface boundaries for mocking, test categories, fixtures | 200 |

## API Reference

| Document | Contents | ~Lines |
|----------|----------|--------|
| [API_INTERFACES.md](API_INTERFACES.md) | CLI commands, HTTP API (Unix socket + optional TCP) | 890 |
| [API_SERVICES.md](API_SERVICES.md) | External service protocols (MakeMKV, TMDB, OpenSubtitles, WhisperX, LLM, Jellyfin, ntfy, FFprobe, KeyDB, MediaInfo, mkvmerge) | 270 |

## Content ID Algorithm

| Document | Contents | ~Lines |
|----------|----------|--------|
| [CONTENT_ID_DESIGN.md](CONTENT_ID_DESIGN.md) | Episode identification algorithm: transcription, fingerprinting, matching strategies, LLM verification | 540 |

## Quick Reference

**Where to find specific topics:**

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
- Stable-TS post-processing -> DESIGN_STAGES.md Section 6.1.1
- SRT alignment algorithm -> DESIGN_STAGES.md Section 6.4.2
- Subtitle candidate ranking -> DESIGN_STAGES.md Section 6.5
- Go package tree -> DESIGN_PACKAGES.md Section 1
- Dependency layer rules -> DESIGN_PACKAGES.md Section 2
- Test double interfaces -> DESIGN_TESTING.md Section 2
- Error taxonomy -> DESIGN_INFRASTRUCTURE.md Section 5
- CLI command reference -> API_INTERFACES.md Section 1
- HTTP API endpoints -> API_INTERFACES.md Section 2.4
- Operational endpoints (cache/staging/discid) -> API_INTERFACES.md Section 2.4.2
- SSE live events -> API_INTERFACES.md Section 2.4.1
- External service protocols -> API_SERVICES.md
