# spindle

**Spindle is an automated disc ripping and media library management system.** Insert a Blu-ray or DVD, and Spindle handles everything: identifies the content using TMDB, rips selected titles with MakeMKV, encodes to efficient AV1 format with drapto, organizes files in Plex-compatible structure, and sends notifications when complete.

> ⚙️ **Go stack** – Spindle ships as a Go CLI (`spindle`) and daemon (`spindled`). Install the CLI with `go install github.com/five82/spindle/cmd/spindle@latest` to get started.

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
- **Disc Automounting**:
  - **Desktop**: Handled automatically by desktop environment
  - **Server**: Add to `/etc/fstab`: `/dev/sr0 /media/cdrom udf,iso9660 ro,noauto,x-systemd.automount,x-systemd.device-timeout=10 0 0`
- **eject utility** – Usually pre-installed (`util-linux` package)

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
go build ./cmd/spindled
```

Run `go test ./...` from the repo root to ensure your toolchain is healthy.

### Configuration

Use `spindle config init` to generate `~/.config/spindle/config.toml`, then edit the following keys:

- `library_dir` – Final Plex-ready library location (must exist)
- `staging_dir` – Working directory for ripped/encoded files
- `tmdb_api_key` – https://www.themoviedb.org/settings/api
- `plex_url` – Plex server address used for library refreshes
- `plex_link_enabled` – If `true`, Spindle links to Plex and triggers library scans automatically
- `ntfy_topic` (optional) – Channel for notifications


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

### System Service (Optional)

```bash
# Install user service (no root needed)
./scripts/install-user-service.sh
systemctl --user enable --now spindle

# Enable autostart on boot
sudo loginctl enable-linger $(whoami)
```

### Additional Commands

- `spindle queue list` (use `--status` filters to narrow by state)
- `spindle queue status` for a status-by-status summary
- `spindle queue clear` (add `--completed`, `--failed`, or `--force` for targeted removal)
- `spindle queue clear-failed`
- `spindle queue reset-stuck`
- `spindle queue retry [item_id ...]`
- `spindle queue health` for lifecycle counts
- `spindle queue-health` for database diagnostics
- `spindle add-file /path/to/video.mkv`
- `spindle test-notify`
- `spindle config validate`
- `spindle show --lines 50 --follow` for live tailing


### Workflow Lifecycle

Spindle runs as a daemon and moves each disc through the queue:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED
```

- `FAILED`: unrecoverable issue (inspect logs / notifications)
- `REVIEW`: manual intervention required. When Spindle cannot identify a disc, it still rips/encodes the content, drops the final file into the `review_dir` (default `~/review`) with a unique name (for example `unidentified-1.mkv`), and marks the queue item complete so the pipeline keeps moving.
- Drives eject automatically at `RIPPED`; encoding/organization continues in background
- Notifications (ntfy) fire when discs are detected, identification resolves (or needs review), queue runs start/finish, and at major stage milestones (`RIPPED`, `ENCODED`, `COMPLETED`, errors)
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

- **Disc not ejecting**: Check logs for ripping errors (only successful rips eject)
- **Stuck identifying**: Verify TMDB API key and disc mounting
- **Poor identification**: Generic disc labels reduce accuracy
- **TMDB rate limits**: Retry later; the daemon will back off and recover on its own

See [docs/content-identification.md](docs/content-identification.md) for deeper analyzer diagnostics.

## Features

- **Multi-tier content identification** with TMDB integration and local caching
- **Phase-aware error handling** with actionable solutions and auto-recovery
- **Real-time notifications** via ntfy.sh for key milestones and errors
- **Background processing** with concurrent encoding and Plex integration

## Development

- Install the CLI and daemon from source during active development: `go install ./cmd/spindle` and `go build ./cmd/spindled`.
- Run `go test ./...` for fast feedback and `golangci-lint run` to catch style issues.
- Before committing, execute:

```bash
./check-ci.sh
```

The script verifies Go version, runs `go test ./...`, and executes `golangci-lint run`. See [docs/development.md](docs/development.md) for deeper workflow notes.
