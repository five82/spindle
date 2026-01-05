# spindle

Personal workflow for turning optical discs into a Jellyfin ready library. Insert a disc and the daemon handles identification (TMDB), ripping (MakeMKV), encoding to AV1 (Drapto), optional subtitles (OpenSubtitles + WhisperX), organization, Jellyfin refreshes, and notifications.

Single Go binary drives both the CLI and daemon.

Early-stage project; expect frequent changes.

## Install

```bash
go install github.com/five82/spindle/cmd/spindle@latest
```

Prerequisites: Go 1.25+, MakeMKV, Drapto, ffmpeg, mediainfo, fpcalc. See [docs/development.md](docs/development.md) for full setup.

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

See [docs/configuration.md](docs/configuration.md) for all options (Jellyfin, subtitles, notifications, rip cache, etc.).

## Run

```bash
spindle config validate   # check config
spindle start             # launch daemon
spindle show --follow     # tail logs
```

Once the daemon reports `RIPPED`, eject the disc manually; encoding and organization continue in the background.

## Documentation

| Guide | Content |
|-------|---------|
| [configuration.md](docs/configuration.md) | All config options |
| [workflow.md](docs/workflow.md) | Stage-by-stage lifecycle |
| [cli.md](docs/cli.md) | Command reference |
| [api.md](docs/api.md) | HTTP API |
| [content-identification.md](docs/content-identification.md) | TMDB matching internals |
| [commentary-detection.md](docs/commentary-detection.md) | Audio track classification |
| [preset-decider.md](docs/preset-decider.md) | LLM-based encoding presets |
| [development.md](docs/development.md) | Prerequisites and dev setup |
