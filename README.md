# spindle

**Spindle automates the trip from optical disc to Plex-ready library.** Insert a
disc and the daemon handles identification (TMDB), ripping (MakeMKV),
encoding to AV1 (drapto), optional subtitle generation (OpenSubtitles +
WhisperX), optional commentary-track retention, organization, Plex refreshes,
and notifications. Opt-in LLM integrations (via OpenRouter) can auto-select
Drapto's grain/clean presets per title and help detect commentary audio tracks
when disc metadata is unreliable.

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
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → [EPISODE_IDENTIFYING → EPISODE_IDENTIFIED]
        → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED
```

`REVIEW` and `FAILED` capture manual intervention paths. Read
`docs/workflow.md` for a stage-by-stage breakdown, file locations, and recovery
ideas.

## Key Commands

| Goal | Command |
| --- | --- |
| Check status & logs | `spindle status`, `spindle show --follow` |
| Inspect queue | `spindle queue list`, `spindle queue status`, `spindle queue health` |
| Inspect item detail | `spindle queue show <id>` (includes episode-by-episode progress) |
| Clean up | `spindle queue clear --completed`, `spindle cache prune` |
| Retry work | `spindle queue retry <id>`, `spindle queue reset-stuck` |
| Utilities | `spindle gensubtitle`, `spindle cache stats`, `spindle test-notify` |

The complete command catalog lives in `docs/cli.md`. HTTP consumers should read
`docs/api.md`.

### Logs & Monitoring

- `spindle show` accepts `--component`, `--lane`, `--request`, and `--item` to zero
  in on a specific workflow runner, background lane, request/correlation ID, or
  queue item when digging through noisy sessions. Combine them, e.g.
  `spindle show --component encoder --lane background --request req-123 --follow`.
- The console logger only prints the highest-signal fields per line; if you need
  every attribute (for example while debugging TMDB responses), set
  `spindle show --follow` to show the complete detail list
  instead of the summarized bullets.

### Adaptive encoding presets (optional)

- Set `preset_decider_enabled = true` in `config.toml` to let an OpenRouter LLM
  decide between `clean`, `grain`, or default Drapto settings on a per-title
  basis. Provide `preset_decider_api_key` (or export `OPENROUTER_API_KEY`) so
  Spindle can call the API, and tweak `preset_decider_model` if you prefer a
  different provider/model.
- When disabled (default) or when confidence is low/missing metadata, Spindle
  sticks with Drapto's built-in defaults and never passes custom presets.
- See `docs/preset-decider.md` for additional details and troubleshooting tips.

### Commentary tracks (optional)

- By default, Spindle keeps only the primary audio track when ripping/encoding.
- Set `commentary_detection_enabled = true` to keep commentary tracks too. The
  detector transcribes short WhisperX snippets from the primary track plus each
  English stereo track, then classifies which candidates are commentary (and
  drops obvious duplicates / music-only / audio description tracks).
- See `docs/commentary-detection.md` for configuration and behavior details.

## Documentation Map

- `docs/configuration.md` — every config key plus tuning tips.
- `docs/workflow.md` — lifecycle walkthrough and monitoring pointers.
- `docs/cli.md` — CLI reference grouped by task.
- `docs/api.md` — HTTP API payloads.
- `docs/content-identification.md` — analyzer internals and debugging notes.
- `docs/preset-decider.md` — LLM-driven Drapto preset selection guide.
- `docs/commentary-detection.md` — WhisperX + LLM commentary detector guide.
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
