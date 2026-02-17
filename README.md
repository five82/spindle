# spindle

Workflow for turning optical discs into a Jellyfin ready library. Insert a disc and the daemon handles identification (TMDB), ripping (MakeMKV), encoding to AV1 (Drapto), optional subtitles (OpenSubtitles + WhisperX), organization, Jellyfin refreshes, and notifications.

Single Go binary drives both the CLI and daemon.

## Expectations

This repository is shared as is. Spindle is a personal tool I built for my own encoding workflow, hardware, and preferences. I’m open sourcing it in the spirit of sharing.

• Personal-first: Things will change and break as I iterate.
• Best-effort only: This is a part-time hobby project and I work on it when I'm able to. I may be slow to respond to questions or may not respond at all.
• PRs welcome (when aligned): I’m happy to review pull requests if they align with the project’s direction and keep things simple/maintainable. For larger changes, please open a discussion thread.
• “Vibe coded”: I’m not a Go developer and this project started as (and remains) a vibe-coding experiment. Expect rough edges.

## Install

```bash
go install github.com/five82/spindle/cmd/spindle@latest
```

Prerequisites: Go 1.26+, MakeMKV, ffmpeg, mediainfo. Optional: mkvmerge (for subtitle muxing).

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

See `sample_config.toml` for all options (Jellyfin, subtitles, notifications, rip cache, etc.).

## Run

```bash
spindle config validate   # check config
spindle start             # launch daemon
spindle status            # check daemon and dependencies
spindle show --follow     # tail logs
```

Once the daemon reports `RIPPED`, eject the disc manually; encoding and organization continue in the background.

## Documentation

- [docs/workflow.md](docs/workflow.md) - stage-by-stage lifecycle
- `sample_config.toml` - all config options with comments
- `spindle --help` - command reference
