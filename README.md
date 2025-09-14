# spindle

**Spindle is an automated disc ripping and media library management system.** Insert a Blu-ray or DVD, and Spindle handles everything: identifies the content using TMDB, rips selected titles with MakeMKV, encodes to efficient AV1 format with drapto, organizes files in Plex-compatible structure, and sends notifications when complete.

**Workflow**: Insert disc → Auto-identify → Rip → **Eject** (drive freed) → Background encode/organize → Available in Plex


## Installation

### Prerequisites

**Required:**
- **uv** - `curl -LsSf https://astral.sh/uv/install.sh | sh`
- **MakeMKV** - Download from https://www.makemkv.com/download/
- **drapto** - `cargo install --git https://github.com/five82/drapto`

**Optional (for better identification):**
- **Disc Automounting**:
  - **Desktop**: Handled automatically by desktop environment
  - **Server**: Add to `/etc/fstab`: `/dev/sr0 /media/cdrom udf,iso9660 ro,noauto,x-systemd.automount,x-systemd.device-timeout=10 0 0`
- **eject utility** - Usually pre-installed (`util-linux` package)

> Run `spindle start` to check missing dependencies with install instructions.


### Install

```bash
uv tool install git+https://github.com/five82/spindle.git
```

### Configuration

```bash
# Create and edit config
spindle init-config
nano ~/.config/spindle/config.toml
```

Required settings:
- `tmdb_api_key` - Get from https://www.themoviedb.org/settings/api
- `plex_url` and `plex_token` - See Plex documentation
- `library_dir` - Your media library path


## Usage

```bash
# Start daemon
spindle start

# Monitor logs
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

```bash
# View queue
spindle queue list
spindle queue clear --completed

# Process existing files
spindle add-file /path/to/video.mkv

# Test notifications
spindle test-notify
```


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

## Features

- **Multi-tier content identification** with TMDB integration and local caching
- **Phase-aware error handling** with actionable solutions and auto-recovery
- **Real-time notifications** via ntfy.sh for key milestones and errors
- **Background processing** with concurrent encoding and Plex integration

## Development

For development setup, testing, and contribution guidelines, see [docs/development.md](docs/development.md).
