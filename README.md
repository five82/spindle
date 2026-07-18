# spindle

Spindle turns optical discs into a Jellyfin-ready library. Insert a disc and
the daemon handles identification with TMDB, ripping with MakeMKV, AV1 encoding
with Reel, optional WhisperX subtitles and commentary detection, organization,
Jellyfin refreshes, and notifications.

A single Go binary provides both the operator CLI and daemon.

## Expectations

This repository is shared as is. Spindle is a personal tool built for one
encoding workflow, hardware setup, and set of preferences. It is open source in
the spirit of sharing, but it is not actively maintained as a general-purpose
product.

- Personal-first: behavior may change as the workflow evolves.
- Best-effort only: questions and issues may receive a slow response or none.
- Pull requests are welcome when they fit the project's goals and use case.
- "Vibe coded": this began as, and remains, a vibe-coding experiment.

## Install

```bash
go install github.com/five82/spindle/cmd/spindle@latest
```

Requirements:

- Go 1.26.5+
- MakeMKV (`makemkvcon`)
- FFmpeg and ffprobe
- mkvmerge for the default subtitle muxing behavior
- Reel's native libraries: SVT-AV1, FFmpeg libraries, libopusenc, and libvship
- A TMDB API key

Optional tools and services include `uvx`/WhisperX, `bd_info`, OpenSubtitles,
OpenRouter, Jellyfin, and ntfy. `spindle status` reports the
locally required command and library checks.

## Configure

Generate the complete commented configuration:

```bash
spindle config init
# Edit the path printed by config init.
spindle config validate
```

A minimal override is:

```toml
[paths]
library_dir = "~/Media/Library"
staging_dir = "~/Media/Staging"

[tmdb]
api_key = "your-tmdb-key"
```

The generated sample shows every option, environment override, and default.
Use `--config /path/to/config.toml` for a non-default location.

To expose the daemon API to the read-only Flyer monitor, configure a TCP
listener and, for anything beyond trusted localhost access, a bearer token:

```toml
[api]
bind = "127.0.0.1:7487"
token = "choose-a-token"
```

The daemon always also listens on its local Unix socket.

## Run

```bash
spindle start
spindle status
spindle logs --follow
```

Insert a disc after the daemon starts. When the rip-complete notification says
the drive is available, eject the disc manually; encoding, analysis, and
organization continue in the background.

Useful inspection commands:

```bash
spindle status
spindle queue list
spindle queue show <id>
spindle logs --follow --item <id>
```

Use `spindle --help` and `spindle <command> --help` for the current command and
flag reference.

## Pipeline and review

Queue items run identification, ripping, episode-identification, encoding,
analysis, subtitling, apply, and organizing tasks before reaching a completed
or failed terminal state. This is a task graph rather than a strict sequence:
encoding can consume titles while ripping continues, and analysis can overlap
encoding. `queue show` reports the live per-task state.

A failed item stopped before completion. Fix the reported cause and retry it.
An item that needs review can still complete, but questionable output is routed
to the configured review area instead of being silently accepted. Clean TV
episodes may reach the library while only unresolved episodes go to review.

Final Jellyfin-facing display subtitles are SRT. They are muxed into the MKV by
default or kept as sidecars when muxing is disabled or fails.

## Recovery

Retry a failed item or every failed item:

```bash
spindle queue retry <id>
spindle queue retry
```

Retry only one failed TV episode:

```bash
spindle queue retry <id> --episode s01e05
```

Stop an item and later resume it with retry:

```bash
spindle queue cancel <id>
spindle queue retry <id>
```

If the daemon crashed, restart it. Running task state is reset on startup so
work can be resumed safely:

```bash
spindle start
```

The queue database is transient. If it must be discarded while the daemon is
stopped:

```bash
spindle queue clear --all
```

This deletes only `queue.db` and its WAL/SHM files. It does not delete staging,
cache, library, or review media. Other queue reads and mutations require the
running daemon.

Inspect or clean leftover working directories with:

```bash
spindle staging list
spindle staging clean
```

## Files

Locations come from the generated configuration:

- `staging_dir`: per-item ripped, encoded, transcript, and subtitle artifacts
- `library_dir`: clean movie and TV outputs using Jellyfin-style names
- `review_dir`: outputs requiring operator inspection, grouped by reason
- `state_dir`: timestamped JSON daemon logs and the transient queue database
- XDG cache: rip cache, disc-ID cache, and OpenSubtitles cache
- XDG runtime directory, with `/tmp` fallback: daemon socket and lock

Successful organization cleans that item's staging directory. Cleanup failures
are warnings so completed media is not discarded merely because temporary
files could not be removed.
