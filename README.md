# spindle

Workflow for turning optical discs into a Jellyfin ready library. Insert a disc and the daemon handles identification (TMDB), ripping (MakeMKV), encoding to AV1 (Drapto), optional subtitles (WhisperX transcription), organization, Jellyfin refreshes, and notifications.

Single Go binary drives both the CLI and daemon.

## Expectations

This repository is shared as is. Spindle is a personal tool I built for my own encoding workflow, hardware, and preferences. I've open sourced it because I believe in sharing but I'm not an active maintainer.

- Personal-first: Things will change and break as I iterate.
- Best-effort only: This is a part-time hobby project and I work on it when I'm able to. I may be slow to respond to questions or may not respond at all.
- PRs: Pull requests are welcome if they align with the project's goals but I may be slow to review them or may not accept changes that don't fit my own use case.
- “Vibe coded”: I’m not a Go developer and this project started as (and remains) a vibe-coding experiment. Expect rough edges.

## Install

```bash
go install github.com/five82/spindle/cmd/spindle@latest
```

Prerequisites: Go 1.26+, MakeMKV, ffmpeg, ffprobe. `spindle status` also checks for mkvmerge, which is needed for default subtitle muxing. Optional feature tools include uvx (for WhisperX subtitles/commentary/episode ID) and bd_info (for improved Blu-ray identification).

## Configure

```bash
spindle config init
nano ~/.config/spindle/config.toml
```

Minimal config:

```toml
[paths]
library_dir = "~/Media/Library"
staging_dir = "~/Media/Staging"

[tmdb]
api_key = "your-tmdb-key"
```

Run `spindle config init --path ./spindle.toml` to generate a fully commented sample config with all options (Jellyfin, subtitles, notifications, rip cache, etc.).

## Run

```bash
spindle config validate   # check config
spindle start             # launch daemon
spindle status            # check daemon and dependencies
spindle logs --follow     # tail logs
```

Once ripping completes and the drive-available notification appears, eject the disc manually; encoding and organization continue in the background.

## Documentation

- [docs/README.md](docs/README.md) - documentation map and source-of-truth policy
- [docs/user/workflow.md](docs/user/workflow.md) - stage-by-stage lifecycle and recovery
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) - system architecture overview
- [docs/API.md](docs/API.md) - HTTP API and stable CLI workflows
- `spindle config init` - generate all config options with comments
- `spindle --help` - command reference
