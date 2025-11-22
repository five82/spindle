# spindle

**Spindle automates the trip from optical disc to Plex-ready library.** Insert a
disc and the daemon handles identification (TMDB), ripping (MakeMKV),
encoding to AV1 (drapto), optional subtitle generation (OpenSubtitles +
WhisperX), organization, Plex refreshes, and notifications.

> ⚙️ Single Go binary (`spindle`) drives both the CLI and daemon.
> 🚧 Early-stage project: expect frequent changes.

## Why Spindle

- End-to-end workflow: disc detection → rip → encode → organize → notify.
- Rich metadata: TMDB matching, TV episode mapping, subtitle validation.
- Resilient queue: recover from failures, retry stages, inspect via CLI or API.
- Friendly ops: ntfy notifications, HTTP status API, and human-readable logs.

## Quick Start

1. **Install prerequisites**
   - Go 1.25+ (`go env GOVERSION`), MakeMKV ≥ 1.17, Drapto (`cargo install --git https://github.com/five82/drapto`).
   - Optional helpers: `bd_info` (`libbluray` tools) for better identification, `eject` util, CUDA 12.8+ for WhisperX acceleration.
2. **Install Spindle**

   ```bash
   go install github.com/five82/spindle/cmd/spindle@latest
   ```

   Ensure `$(go env GOPATH)/bin` (or `GOBIN`) is on your `PATH`.
3. **Create and edit your config**

   ```bash
   spindle config init
   nano ~/.config/spindle/config.toml
   ```

   Minimal example:

   ```toml
   library_dir = "~/Media/Library"
   staging_dir = "~/Media/Staging"
   tmdb_api_key = "tmdb-key-here"
   plex_url = "https://plex.example.com"
   plex_link_enabled = true
   ntfy_topic = "spindle"
   ```

   See `docs/configuration.md` for every knob (Plex, subtitles, rip cache, etc.).
4. **Validate, authorize, and run**

   ```bash
   spindle config validate
   spindle plex link        # once per host when Plex linking is enabled
   spindle start            # launches daemon in the background
   spindle show --follow    # colorful live logs
   ```

Once the daemon reports `RIPPED`, eject the disc manually; encoding and
organization continue in the background.

## Everyday Workflow

Each queue item flows through:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED
        → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED
```

`REVIEW` and `FAILED` capture manual intervention paths. Read
`docs/workflow.md` for a stage-by-stage breakdown, file locations, and recovery
ideas.

## Key Commands

| Goal | Command |
| --- | --- |
| Check status & logs | `spindle status`, `spindle show --follow` |
| Inspect queue | `spindle queue list`, `spindle queue status`, `spindle queue health` |
| Clean up | `spindle queue clear --completed`, `spindle cache prune` |
| Retry work | `spindle queue retry <id>`, `spindle queue reset-stuck` |
| Utilities | `spindle add-file`, `spindle gensubtitle`, `spindle test-notify` |

The complete command catalog lives in `docs/cli.md`. HTTP consumers should read
`docs/api.md`.

## Documentation Map

- `docs/configuration.md` — every config key plus tuning tips.
- `docs/workflow.md` — lifecycle walkthrough and monitoring pointers.
- `docs/cli.md` — CLI reference grouped by task.
- `docs/api.md` — HTTP API payloads.
- `docs/content-identification.md` — analyzer internals and debugging notes.
- `docs/development.md` — hacking on Spindle, architecture deep dives.

## Troubleshooting

- Missing discs or poor metadata: confirm mounts under `/media/cdrom*`, install
  `bd_info`, and review `docs/content-identification.md`.
- Dependencies: run `spindle status` for missing MakeMKV/Drapto hints.
- Subtitle drift: inspect queue logs (`spindle show --follow`) and re-run
  `spindle gensubtitle --forceai` when needed.

If the daemon surprises you, stop it (`spindle stop`), fix the issue, and retry
items with `spindle queue retry <id>`.

## Development

Clone the repo for local hacking:

```bash
git clone https://github.com/five82/spindle.git
cd spindle
go install ./cmd/spindle
```

Run tests and linting before sending patches:

```bash
./check-ci.sh   # runs go test ./... and golangci-lint run
```

The `docs/development.md` file covers repo layout, staging data, and integration
test tips.
