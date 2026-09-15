# spindle

Spindle turns optical discs into a [Loom](https://github.com/five82/loom)-ready
library. Insert a disc and the daemon handles identification with
[TMDB](https://www.themoviedb.org/), ripping with
[MakeMKV](https://www.makemkv.com/), AV1 encoding with
[Reel](https://github.com/five82/reel), subtitles and commentary detection,
organization, Loom scans, and notifications.

A single Go binary provides both the operator CLI and daemon.

## Expectations

Spindle is a personal tool built for one encoding workflow, hardware setup, and
set of preferences. It is open source in the spirit of sharing, but not
actively maintained as a general-purpose product: behavior may change as the
workflow evolves, and questions or issues may receive a slow response or none.
Pull requests are welcome when they fit the project's goals. The project began
as and remains an experiment — expect rough edges.

## Install

```bash
go install github.com/five82/spindle/cmd/spindle@latest
```

Requirements:

- Linux; optical-disc detection is Linux-only
- Go 1.27.1+
- MakeMKV (`makemkvcon`)
- FFmpeg and ffprobe
- mkvmerge (used when subtitle muxing into the MKV is enabled)
- `uvx`, which runs WhisperX, stable-ts, and ffsubsync on demand
- Reel's native libraries: SVT-AV1, FFmpeg libraries, libopusenc, libvship
- A TMDB API key

Optional tools and services include `bd_info`, OpenSubtitles, OpenRouter, Loom,
and ntfy. `spindle status` reports the locally required command and library
checks, and `./check-deps.sh` reports dependency and CI action updates.

To deploy a source checkout on the machine running Spindle:

```bash
./check-ci.sh
./deploy.sh
```

The deploy script builds the working tree, keeps the previous binary beside the
installed one, and preserves daemon state: a running daemon is restarted while
a stopped daemon remains stopped. Because the daemon drains in-flight work
before exiting, a deploy waits at most for the current disc rip;
`spindle stop --force` skips the drain.

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

The generated sample shows every option, environment override, and default. Use
`--config /path/to/config.toml` for a non-default location, and
`spindle --help` / `spindle <command> --help` for the current command and flag
reference.

To expose the daemon API to the read-only
[Flyer](https://github.com/five82/flyer) monitor, configure a TCP listener and,
for anything beyond trusted localhost access, a bearer token:

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
spindle queue audit <id>       # digest to stdout, full JSON report to a temp file
spindle logs --follow --item <id>
```

`status`, `queue list`, and `queue show` also work while the daemon is stopped:
they fall back to a direct read-only view of the queue database.

## Pipeline

Queue items run identification, ripping, episode-identification, encoding,
analysis, subtitling, apply, and organizing tasks before reaching a completed
or failed terminal state. This is a task graph rather than a strict sequence:
encoding can consume titles while ripping continues, and analysis can overlap
encoding. `queue show` reports the live per-task state.

A failed item stops short of completion. Fix the reported cause and retry it.
An item that needs review can still complete, but questionable output is routed
to the configured review area instead of being silently accepted. Clean TV
episodes may reach the library while only unresolved episodes go to review.

Successful organization cleans that item's staging directory. Cleanup failures
are warnings, so completed media is never discarded over leftover temporary
files.

## Subtitles

Final display subtitles are SRT, muxed into the MKV by default
(`[subtitles] mux_into_mkv = true`) or kept as sidecars when muxing is disabled
or fails.

The pipeline downloads the identified title's OpenSubtitles track, cleans promo
lines and SDH annotation from it, retimes it against the rip's WhisperX
transcript with ffsubsync, and adopts it only when it verifies against that
transcript. When no candidate verifies, the title completes without subtitles:
the skip is logged as a warning, flagged by `spindle queue audit <id>`, and
listed in the completion notification.

`spindle subtitle` runs the same adoption process manually for any file. It
reads the `[tmdbid-ID]` marker from library paths (or takes `--tmdb-id`, with
`--season`/`--episode` for TV), and writes a sidecar with `--external` instead
of muxing. It is useful for orchestrated discs and for retrying after better
uploads appear. WhisperX subtitle generation lives in the `whisperx-subtitles`
agent skill in `.agents/skills/`, for titles nothing on OpenSubtitles matches.

## Manual orchestration

The daemon automates the standard case: one disc, one feature or one TV season.
Edge cases — disc extras, theatrical shorts, multi-disc movies, multiple
editions, discs MakeMKV struggles with — are handled by a coding agent using
the `orchestrate` skill in `.agents/skills/orchestrate/`. The skill drives the
same building blocks by hand: `spindle disc scan`, `spindle rip`,
`spindle encode`, `spindle subtitle`, and `spindle loom scan`. The daemon must
be stopped while orchestration runs; `spindle rip` and `spindle encode` enforce
this.

## Rip cache

With `[rip_cache] enabled = true`, every successful rip is also cached, up to
`max_gib`. Re-inserting the same disc restores it from the cache instead of
ripping again, and cached rips survive a queue clear. The cache can also be
driven directly:

```bash
spindle cache rip            # rip a disc into the cache instead of the queue
spindle cache list           # list cached rips with their entry selectors
spindle cache process 2      # queue a cached rip for processing
spindle cache remove a1b2c3  # remove one entry by selector
spindle cache clear          # remove every entry
```

`spindle cache rip` requires the daemon to be stopped; `cache process` requires
it to be running.

## Recovery

```bash
spindle queue retry <id>                    # retry one failed item
spindle queue retry                         # retry every failed item
spindle queue retry <id> --episode s01e05   # retry one failed TV episode
spindle queue cancel <id>                   # stop it, resume later with retry
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
- `library_dir`: clean movie and TV outputs using Loom-style names
- `review_dir`: per-item folders named for the review reason and disc
  fingerprint, holding outputs that require operator inspection
- `state_dir`: timestamped JSON daemon logs, the transient queue database, and
  `metrics.jsonl` — one appended record per completed item, holding stage
  durations and resource waits, rip throughput per physical drive, and
  per-episode encode stats; durable across queue clears and queryable with
  `jq` or an LLM
- XDG cache: rip cache, disc-ID cache, and OpenSubtitles cache
- XDG runtime directory, with `/tmp` fallback: daemon socket and lock

Identified library paths include Loom TMDB IDs:

```text
Movies/Movie (2024) [tmdbid-123456]/Movie (2024) [tmdbid-123456].mkv
TV/Show (2020) [tmdbid-654321]/Season 01/Show - S01E05.mkv
```
