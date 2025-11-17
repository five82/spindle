# spindle

**Spindle is an automated disc ripping and media library management system.** Insert a Blu-ray or DVD, and Spindle handles everything: identifies the content using TMDB, rips selected titles with MakeMKV, encodes to efficient AV1 format with drapto, organizes files in Plex-compatible structure, and sends notifications when complete.

> ⚙️ **Go stack** – Spindle ships as a single Go binary (`spindle`) that runs both the CLI and daemon. Install it with `go install github.com/five82/spindle/cmd/spindle@latest` to get started.

> **Early-stage project**: Expect breaking changes.

**Workflow**: Insert disc → Auto-identify → Rip → **Eject** (drive freed) → Background encode/organize → Available in Plex

## Quick Start

1. Install Go 1.25 or newer.
2. Install the CLI:

   ```bash
   go install github.com/five82/spindle/cmd/spindle@latest
   ```

   Ensure `$(go env GOPATH)/bin` (or `GOBIN` if set) is on your `PATH` so the `spindle` binary is discoverable.

3. Create your initial configuration:

   ```bash
   spindle config init
   nano ~/.config/spindle/config.toml
   ```

Minimal config example:

```toml
library_dir = "~/Media/Library"
staging_dir = "~/Media/Staging"
tmdb_api_key = "tmdb-key-here"
plex_url = "https://plex.example.com"
ntfy_topic = "spindle"
```

```bash
# Start and monitor the daemon
spindle start
spindle show --follow
```


## Installation

### Prerequisites

**Required:**
- **Go** ≥ 1.25 – https://go.dev/dl/
- **MakeMKV** ≥ 1.17 – https://www.makemkv.com/download/
- **drapto** (requires Rust toolchain) – `cargo install --git https://github.com/five82/drapto`

**Optional (for better identification):**
- **bd_info** – For enhanced disc identification when MakeMKV returns generic titles:
  - Ubuntu/Debian: `sudo apt install libbluray-bin`
  - Fedora/CentOS: `sudo dnf install libbluray-utils`
  - Arch: `sudo pacman -S libbluray`
- **Disc Automounting**:
  - **Desktop**: Handled automatically by desktop environment
  - **Server**: Add to `/etc/fstab`: `/dev/sr0 /media/cdrom udf,iso9660 ro,noauto,x-systemd.automount,x-systemd.device-timeout=10 0 0`
- **eject utility** – Usually pre-installed (`util-linux` package)
- **KEYDB.cfg cache** – Spindle automatically refreshes the AACS key database when Disc IDs are stale. If you want to seed the cache manually, drop a current `KEYDB.cfg` at `~/.config/spindle/keydb/KEYDB.cfg` (or change `keydb_path` in your config).

Start the daemon once to verify dependencies—missing tools are reported with installation hints.


### Install

Install the CLI directly from source (recommended for daily use):

```bash
go install github.com/five82/spindle/cmd/spindle@latest
```

The binary lands in `$(go env GOBIN)` or `$(go env GOPATH)/bin`. Add that directory to your `PATH` if needed.

For local development:

```bash
git clone https://github.com/five82/spindle.git
cd spindle
go install ./cmd/spindle
```

Run `go test ./...` from the repo root to ensure your toolchain is healthy.

### Configuration

Use `spindle config init` to generate `~/.config/spindle/config.toml`, then edit the following keys:

- `library_dir` – Final Plex-ready library location (must exist)
- `staging_dir` – Working directory for ripped/encoded files
- `tmdb_api_key` – https://www.themoviedb.org/settings/api
- `plex_url` – Plex server address used for library refreshes
- `plex_auth_path` – Path where Spindle stores the Plex authorization token (defaults to `~/.config/spindle/plex_auth.json`)
- `plex_link_enabled` – If `true`, Spindle links to Plex and triggers library scans automatically
- `ntfy_topic` (optional) – Channel for notifications
- `subtitles_enabled` (optional) – Enable WhisperX subtitle generation after encoding (requires `uv`/`uvx`)
- `whisperx_cuda_enabled` (optional) – Set `true` to run WhisperX with CUDA (requires CUDA 12.8+ and cuDNN 9.1); leave `false` to fall back to CPU
- `whisperx_vad_method` (optional) – Voice activity detector for WhisperX; `silero` (default) runs fully offline, `pyannote` offers tighter alignment but needs a Hugging Face token. Spindle validates the token before each run and drops back to `silero` if Hugging Face rejects it (check the subtitle logs for the authentication message).
- `whisperx_hf_token` (optional) – Hugging Face access token required when using `whisperx_vad_method = "pyannote"`; create one at https://huggingface.co/settings/tokens. The subtitle stage logs whether authentication succeeded or if it fell back to `silero`.
- `opensubtitles_enabled` (optional) – When true, Spindle downloads subtitles from OpenSubtitles before falling back to WhisperX transcription. This also enables the post-rip content-ID pass that compares WhisperX transcripts from each ripped episode against OpenSubtitles references to lock in the correct episode order.
- `opensubtitles_api_key` (optional) – API key for OpenSubtitles. Required when `opensubtitles_enabled = true`. Create one from your OpenSubtitles profile under **API consumers**.
- `opensubtitles_user_agent` (optional) – Custom user agent string registered with OpenSubtitles. Required when `opensubtitles_enabled = true`.
- `opensubtitles_languages` (optional) – Preferred subtitle languages (ISO 639-1 codes, for example `['en','es']`). Used for OpenSubtitles searches.
- `opensubtitles_user_token` (optional) – OpenSubtitles JWT for authenticated downloads. Provides higher daily download limits than anonymous mode.
- Subtitle pipeline: duration guardrails soft-reject mismatched candidates (logged with `soft_reject=true`), an intro-gap exception allows common disc intros, and a mis-ID guard flags items for review (“suspect mis-identification (subtitle offsets)”) while attempting a WhisperX-only fallback. Logs label the chosen `subtitle_source` as `opensubtitles` or `whisperx` so you can see what Plex will ingest.
- `api_bind` – Host:port for the built-in JSON API (defaults to `127.0.0.1:7487`)
- `keydb_path` – Location where Spindle stores/reads `KEYDB.cfg` for Disc ID lookups (defaults to `~/.config/spindle/keydb/KEYDB.cfg`)
- `keydb_download_url` – Mirror URL Spindle uses when auto-refreshing `KEYDB.cfg`
- `keydb_download_timeout` – Download timeout (seconds) for the KEYDB refresh
- `identification_overrides_path` – Optional JSON file containing curated disc overrides (defaults to `~/.config/spindle/overrides/identification.json`)


## Usage

```bash
# Start daemon
spindle start

# Monitor live logs (color-coded)
spindle show --follow

# Check status
spindle status

# Stop daemon
spindle stop

# Authorize Plex (run once, prompts for link code)
spindle plex link
```

### Additional Commands

- `spindle queue list` (use `--status` filters to narrow by state)
- `spindle queue status` for a status-by-status summary
- `spindle queue clear` (add `--completed` or `--failed` for targeted removal)
- `spindle queue clear-failed`
- `spindle queue reset-stuck`
- `spindle queue retry [item_id ...]`
- `spindle queue health` for lifecycle counts
- `spindle queue-health` for database diagnostics
- `spindle add-file /path/to/video.mkv`
- `spindle gensubtitle /path/to/video.mkv` (add `--forceai` to skip OpenSubtitles downloads)
- `spindle test-notify`
- `spindle config validate`
- `spindle show --lines 50 --follow` for live tailing
- `spindle cache stats` (shows rip cache usage; requires `rip_cache_enabled = true` in `config.toml`)
- `spindle cache prune` (forces immediate pruning to size/free-space targets)

### HTTP API

When the daemon is running, Spindle exposes a read-only JSON API (default `http://127.0.0.1:7487`).

- `GET /api/status` – Daemon runtime information, dependency health, and workflow status.
- `GET /api/queue` – List queue items; filter with repeated `status` query parameters.
- `GET /api/queue/{id}` – Inspect a single queue item.

Adjust the bind address with the `api_bind` configuration key. The API is intended for dashboards and TUIs on trusted networks.


### Workflow Lifecycle

Spindle runs as a daemon and moves each disc through the queue:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → [SUBTITLING → SUBTITLED] → ORGANIZING → COMPLETED
```

*(The subtitle stage runs only when `subtitles_enabled = true` in your config.)*

- `FAILED`: unrecoverable issue (inspect logs / notifications)
- `REVIEW`: manual intervention required. When Spindle cannot identify a disc, it still rips/encodes the content, drops the final file into the `review_dir` (default `~/review`) with a unique name (for example `unidentified-1.mkv`), and marks the queue item complete so the pipeline keeps moving.
- Rip completion is marked at `RIPPED`; you'll receive a notification and can eject the disc manually while encoding/organization continue in the background
- When OpenSubtitles + WhisperX are available, the ripper immediately transcribes every ripped episode and compares it to OpenSubtitles references so discs with shuffled playlists still produce the correct SxxEyy assignments before encoding starts.
- Notifications (ntfy) fire when discs are detected, a disc is identified with title/year, rips finish, encodes complete, the library import succeeds, and whenever an error occurs
- Read `docs/workflow.md` for a detailed walkthrough


## Troubleshooting

### Disc Not Found at Standard Mount Points

"Disc not found at standard mount points" is not an error - ripping still works. Enhanced metadata extraction requires disc mounting (see Installation > Prerequisites > Disc Automounting).

To verify mounting:
```bash
# Insert a disc, then check if it's mounted
ls -la /media/cdrom /media/cdrom0
# You should see disc contents (BDMV, VIDEO_TS, etc.)
```

### Common Issues

- **Disc not ejecting**: Manually run `eject /dev/sr0` (or your configured drive) after the rip notification; adjust udev/fstab permissions if the command refuses to unmount
- **Stuck identifying**: Verify TMDB API key and disc mounting
- **Poor identification**: Generic disc labels reduce accuracy - install `libbluray-utils` for bd_info fallback
- **TMDB rate limits**: Retry later; the daemon will back off and recover on its own

See [docs/content-identification.md](docs/content-identification.md) for deeper analyzer diagnostics.

## Features

- **Multi-tier content identification** with TMDB integration and local caching
- **Phase-aware error handling** with actionable solutions and auto-recovery
- **Real-time notifications** via ntfy.sh for key milestones and errors
- **Background processing** with concurrent encoding and Plex integration

## Development

- Install the CLI/daemon binary from source during active development: `go install ./cmd/spindle`.
- Run `go test ./...` for fast feedback and `golangci-lint run` to catch style issues.
- Before committing, execute:

```bash
./check-ci.sh
```

The script verifies Go version, runs `go test ./...`, and executes `golangci-lint run`. See [docs/development.md](docs/development.md) for deeper workflow notes.
