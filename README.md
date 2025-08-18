# spindle

Automated disc ripping, encoding, and media library management system.

## Overview

spindle automates the complete workflow from physical disc to organized media library:

1. **Disc Detection** - Automatically detects inserted physical discs
2. **Ripping** - Uses MakeMKV to extract main content with English audio tracks
3. **Identification** - Identifies content using TMDB API
4. **Encoding** - Processes video through drapto for efficient AV1 encoding
5. **Organization** - Organizes files in Plex-compatible structure
6. **Import** - Triggers Plex library scans and sends notifications

## Features

- **Continuous Processing** - Insert disc → auto-rip → auto-process → ready for next disc
- **Background Queue Processing** - Each item processed as soon as previous stage completes
- **Smart Identification** - TMDB integration for movies and TV shows
- **Quality Encoding** - Integration with drapto for optimized AV1 compression
- **Immediate Plex Import** - Movies available as soon as encoded (no waiting for batch)
- **Progress Tracking** - SQLite-based queue management with status tracking
- **Notifications** - Real-time updates via ntfy.sh for each stage
- **Error Handling** - Unidentified media moved to review directory
- **Manual Processing** - Process existing video files when needed

## Installation

### Prerequisites

1. **MakeMKV** - For disc ripping
   ```bash
   # Install MakeMKV on Debian/Ubuntu
   # Follow official MakeMKV installation instructions
   ```

2. **drapto** - For video encoding
   ```bash
   cargo install --git https://github.com/five82/drapto
   ```

3. **uv** - Python package manager (REQUIRED)
   ```bash
   # Install uv first (https://docs.astral.sh/uv/getting-started/installation/)
   curl -LsSf https://astral.sh/uv/install.sh | sh
   ```

### Install Spindle

**⚠️ IMPORTANT: Spindle requires uv package manager. Standard pip will not work.**

```bash
# Install directly from GitHub repository
uv pip install git+https://github.com/five82/spindle.git
```

## Configuration

1. **Create configuration file**:
   ```bash
   uv run spindle init-config
   ```

2. **Edit configuration** at `~/.config/spindle/config.toml`:
   ```toml
   # Directory paths (edit for your setup)
   staging_dir = "~/.local/share/spindle/staging"  # Temporary processing
   library_dir = "~/your-media-library"            # Your media library directory
   log_dir = "~/.local/share/spindle/logs"         # Log files
   review_dir = "~/your-review-directory"          # Unidentified media

   # REQUIRED: API credentials
   tmdb_api_key = "your_tmdb_api_key"
   plex_url = "http://localhost:32400"
   plex_token = "your_plex_token"

   # Optional: Notifications
   ntfy_topic = "https://ntfy.sh/your_topic"
   ```

3. **Get TMDB API key** from https://www.themoviedb.org/settings/api

4. **Get Plex token** - see Plex documentation

## Usage

### Main Workflow - Continuous Processing
```bash
# Start Spindle (runs as background daemon by default)
uv run spindle start

# Or run in foreground for testing/debugging
uv run spindle start --foreground

# Stop daemon when needed
uv run spindle stop
```

By default, `spindle start` runs as a background daemon:
1. **Insert a disc** → Automatically ripped and added to queue
2. **Background processing** → Identify → Encode → Import to Plex
3. **Disc ejected** → Ready for next disc
4. **Repeat** → Each movie or TV show becomes available in Plex as soon as it's done

**Default Daemon Mode Benefits:**
- Runs independently of your terminal session
- Survives SSH disconnections (but not reboots unless using systemd service)
- Logs activity to `log_dir/spindle.log`
- Can insert discs anytime, processing happens automatically
- Use `--foreground` flag only for testing/debugging

### Install as User Service
```bash
# Install as user systemd service (no root needed!)
./scripts/install-user-service.sh

# Edit configuration
nano ~/.config/spindle/config.toml

# Enable and start service
systemctl --user enable spindle
systemctl --user start spindle

# Check status and logs
systemctl --user status spindle
journalctl --user -u spindle -f

# Optional: Enable autostart on boot
sudo loginctl enable-linger $(whoami)
```

**User Service Benefits:**
- No root permissions needed
- No permission issues with media directories
- Runs under your user account
- Simple directory structure in home folder
- Easy access to config and logs

### System Management
```bash
# Check system status and queue
uv run spindle status

# View queue contents
uv run spindle queue-list

# Clear completed items
uv run spindle queue-clear --completed

# Test notifications
uv run spindle test-notify
```

### Manual File Processing
```bash
# Add existing video files to queue (processed automatically by continuous mode)
uv run spindle add-file /path/to/video.mkv
```

## Workflow Details

### Continuous Processing (Default)
```
Disc 1 Insert → Rip → Eject → Ready for Disc 2
    ↓ (background processing)
    Identify → Encode → Organize → Import → Available in Plex

Disc 2 Insert → Rip → Eject → Ready for Disc 3
    ↓ (background processing)
    Identify → Encode → Organize → Import → Available in Plex

... and so on
```

**Key Benefits:**
- Minimal disc handling time (just rip and eject)
- Movies and TV shows appear in Plex as soon as they're done encoding
- Can process many discs in a ripping session
- Background processing continues overnight

### Manual Processing
```
Existing files → Add to queue → Identify → Encode → Organize → Import
```

## Directory Structure

```
library_dir/
├── Movies/
│   └── Movie Title (Year)/
│       └── Movie Title (Year).mkv
└── TV Shows/
    └── Show Title (Year)/
        └── Season 01/
            └── Show Title - S01E01 - Episode Title.mkv

review_dir/
└── unidentified/
    └── unidentified_file.mkv
```

## Requirements

### Software Dependencies
- **uv** - Python package manager (REQUIRED)
- Python 3.11+
- MakeMKV (makemkvcon)
- drapto (Rust-based encoder)
- Optional: Plex Media Server

### Hardware Requirements
- Optical drive
- Sufficient storage for staging and final library
- Network access for TMDB API and Plex

## Development

For development setup, testing, and contribution guidelines, see [docs/development.md](docs/development.md).

## Error Handling

- **Unidentified Media**: Moved to `review_dir/unidentified/` (configurable)
- **Failed Encoding**: Marked as failed in queue, notifications sent
- **Plex Import Failures**: Logged and marked as failed
- **Network Issues**: Retries with exponential backoff

## Notifications

Spindle sends notifications for:
- Disc detection
- Rip completion
- Encoding progress
- Media added to Plex
- Queue status updates
- Errors and failures
