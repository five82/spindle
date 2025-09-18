# spindle

**Spindle is an automated disc ripping and media library management system.** Insert a Blu-ray or DVD, and Spindle handles everything: identifies the content using TMDB, rips selected titles with MakeMKV, encodes to efficient AV1 format with drapto, organizes files in Plex-compatible structure, and sends notifications when complete.

> **Early-stage project**: Expect breaking changes.

**Workflow**: Insert disc → Auto-identify → Rip → **Eject** (drive freed) → Background encode/organize → Available in Plex

## Quick Start

```bash
# Install Spindle as a tool (uv manages the environment)
uv tool install git+https://github.com/five82/spindle.git

# Create initial config
spindle init-config

# Edit required settings
nano ~/.config/spindle/config.toml
```

Minimal config example:

```toml
library_dir = "~/Media/Library"
staging_dir = "~/Media/Staging"
tmdb_api_key = "tmdb-key-here"
plex_url = "https://plex.example.com"
plex_token = "plex-token"
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
- **uv** (latest stable) - `curl -LsSf https://astral.sh/uv/install.sh | sh`
- **MakeMKV** ≥ 1.17 - https://www.makemkv.com/download/
- **drapto** (requires Rust toolchain) - `cargo install --git https://github.com/five82/drapto`

**Optional (for better identification):**
- **Disc Automounting**:
  - **Desktop**: Handled automatically by desktop environment
  - **Server**: Add to `/etc/fstab`: `/dev/sr0 /media/cdrom udf,iso9660 ro,noauto,x-systemd.automount,x-systemd.device-timeout=10 0 0`
- **eject utility** - Usually pre-installed (`util-linux` package)

Start the daemon once to verify dependencies—missing tools are reported with installation hints.


### Install

Global tool (recommended for daily use):

```bash
uv tool install git+https://github.com/five82/spindle.git
```

Local development clone:

```bash
git clone https://github.com/five82/spindle.git
cd spindle
uv pip install -e ".[dev]"
```

### Configuration

Use `spindle init-config` to generate `~/.config/spindle/config.toml`, then edit the following keys:

- `library_dir` – Final Plex-ready library location (must exist)
- `staging_dir` – Working directory for ripped/encoded files
- `tmdb_api_key` – https://www.themoviedb.org/settings/api
- `plex_url` / `plex_token` – Plex server + token for library scans
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

- `spindle queue status` / `spindle queue list`
- `spindle queue clear --completed` (also `--failed` / `--force`)
- `spindle queue retry <item_id>`
- `spindle queue-health`
- `spindle add-file /path/to/video.mkv`
- `spindle config validate`
- `spindle test-notify`


### Workflow Lifecycle

Spindle runs as a daemon and moves each disc through the queue:

```
PENDING → IDENTIFYING → IDENTIFIED → RIPPING → RIPPED → ENCODING → ENCODED → ORGANIZING → COMPLETED
```

- `FAILED`: unrecoverable issue (inspect logs / notifications)
- `REVIEW`: manual intervention required, files moved to `review_dir`
- Drives eject automatically at `RIPPED`; encoding/organization continues in background
- Notifications (ntfy) fire at major milestones (`RIPPED`, `COMPLETED`, errors)


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
- **TMDB rate limits**: Retry later or delete cached entries (`rm ~/.local/share/spindle/logs/tmdb_cache.db`)
- **Cache confusion**: Inspect cache DBs under `~/.local/share/spindle/logs/`

See [docs/content-identification.md](docs/content-identification.md) for deeper analyzer diagnostics.

## Features

- **Multi-tier content identification** with TMDB integration and local caching
- **Phase-aware error handling** with actionable solutions and auto-recovery
- **Real-time notifications** via ntfy.sh for key milestones and errors
- **Background processing** with concurrent encoding and Plex integration

## Development

Run `uv pip install -e ".[dev]"` once, then rely on `uv run` for tooling. Before committing, execute:

```bash
./check-ci.sh
```

It runs pytest with coverage, `black --check`, `ruff`, `uv build`, and `twine check`. See [docs/development.md](docs/development.md) for maintainer notes.
