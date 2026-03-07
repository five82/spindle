# Design Document Index

Complete specification for a clean room rewrite of Spindle. Each document is
self-contained and focused on a single domain area.

## System Design

| Document | Contents | ~Lines |
|----------|----------|--------|
| [DESIGN_OVERVIEW.md](DESIGN_OVERVIEW.md) | Introduction, external dependencies, services, architecture, configuration, filesystem layout | 620 |
| [DESIGN_QUEUE.md](DESIGN_QUEUE.md) | SQLite queue database: schema, item model, status state machine, store operations | 295 |
| [DESIGN_RIPSPEC.md](DESIGN_RIPSPEC.md) | RipSpec envelope: structure, metadata, titles, episodes, assets, attributes, methods | 210 |
| [DESIGN_DAEMON.md](DESIGN_DAEMON.md) | Daemon lifecycle, disc detection pipeline, daemon orchestration layer | 350 |
| [DESIGN_STAGES.md](DESIGN_STAGES.md) | All 7 pipeline stages: identification, ripping, episode ID, encoding, audio analysis, subtitles, organization | 510 |
| [DESIGN_INFRASTRUCTURE.md](DESIGN_INFRASTRUCTURE.md) | Logging, notifications, preflight, shared utilities, log access, audit gathering, config validation | 520 |

## API Reference

| Document | Contents | ~Lines |
|----------|----------|--------|
| [API_INTERFACES.md](API_INTERFACES.md) | CLI commands, IPC protocol, HTTP API | 890 |
| [API_SERVICES.md](API_SERVICES.md) | External service protocols (MakeMKV, TMDB, OpenSubtitles, WhisperX, LLM, Jellyfin, ntfy, FFprobe, KeyDB, MediaInfo, mkvmerge) | 270 |

## Content ID Algorithm

| Document | Contents | ~Lines |
|----------|----------|--------|
| [CONTENT_ID_DESIGN.md](CONTENT_ID_DESIGN.md) | Episode identification algorithm: transcription, fingerprinting, matching strategies, LLM verification | 540 |

## Quick Reference

**Where to find specific topics:**

- Configuration fields and defaults -> DESIGN_OVERVIEW.md Section 5
- Queue status lifecycle -> DESIGN_QUEUE.md Section 4
- RipSpec data model -> DESIGN_RIPSPEC.md
- Disc fingerprinting -> DESIGN_DAEMON.md Section 2.4
- Stage execution lifecycle -> DESIGN_OVERVIEW.md Section 4.6
- MakeMKV robot format -> DESIGN_STAGES.md Section 1.1.1
- Audio track selection -> DESIGN_INFRASTRUCTURE.md Section 4.5
- CLI command reference -> API_INTERFACES.md Section 1
- HTTP API endpoints -> API_INTERFACES.md Section 3.4
- External service protocols -> API_SERVICES.md
