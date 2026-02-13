# spindle

Personal workflow for turning optical discs into a Jellyfin ready library. Insert a disc and the daemon handles identification (TMDB), ripping (MakeMKV), encoding to AV1 (Drapto), optional subtitles (OpenSubtitles + WhisperX), organization, Jellyfin refreshes, and notifications.

Single Go binary drives both the CLI and daemon.

Early-stage project; expect frequent changes.

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
