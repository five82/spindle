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
- **Enhanced Content Identification** - Multi-tier identification with UPC/barcode support
- **Quality Encoding** - Integration with drapto for optimized AV1 compression
- **Immediate Plex Import** - Movies available as soon as encoded (no waiting for batch)
- **Progress Tracking** - SQLite-based queue management with status tracking
- **Notifications** - Real-time updates via ntfy.sh for each stage
- **Error Handling** - Unidentified media moved to review directory
- **Manual Processing** - Process existing video files when needed
- **System Dependency Checking** - Automatic validation with helpful install guidance

## Enhanced Content Identification

Spindle uses a **multi-tier identification system** for maximum accuracy:

### Tier 1: UPC/Barcode Identification (Highest Confidence)
- **Extracts UPC codes** from Blu-ray disc metadata (`/BDMV/META/DL/*.xml`)
- **UPCitemdb.com integration** - Converts barcode to product information
- **TMDB verification** - Cross-references product data with movie database
- **Perfect accuracy** for commercial releases with embedded UPC codes

### Tier 2: Runtime-Verified Search (High Confidence)
- **Disc name analysis** - Cleans and searches disc labels
- **Duration matching** - Compares disc runtime with TMDB data
- **Edition detection** - Distinguishes theatrical vs extended cuts automatically
- **High reliability** for discs with meaningful names

### Tier 3: Intelligent Pattern Analysis (Medium Confidence)
- **TMDB API integration** - Powered by https://www.themoviedb.org
- **Content type detection** - Movies, TV series, cartoon collections, documentaries
- **Smart title selection** - Automatically selects main content vs extras
- **Fallback reliability** when other methods fail

### Special Features
- **Local caching** - Stores UPC lookups to minimize API usage
- **Graceful degradation** - Works even with missing optional dependencies
- **Multi-format support** - Handles various disc structures and metadata formats

## Installation

### Prerequisites

#### Required Dependencies
Spindle automatically checks for these on startup and will exit if missing:

1. **MakeMKV** - For disc ripping
   ```bash
   # Download and install from https://www.makemkv.com/download/
   # Requires license key for Blu-ray ripping
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

#### Optional Dependencies
These enhance functionality but are not required to run:

4. **udisks2** - For automatic disc mounting (enables UPC identification)
   ```bash
   # Debian/Ubuntu
   sudo apt install udisks2

   # RHEL/CentOS/Fedora
   sudo dnf install udisks2

   # Arch Linux
   sudo pacman -S udisks2

   # Configure user-mountable optical drive
   echo '/dev/sr0 /media/$USER/optical udf,iso9660 ro,user,noauto 0 0' | sudo tee -a /etc/fstab
   sudo mkdir -p /media/$USER/optical && sudo chown $USER:$USER /media/$USER/optical
   ```

5. **eject utility** - For automatic disc ejection
   ```bash
   # Usually pre-installed, but if needed:
   # Debian/Ubuntu
   sudo apt install eject

   # RHEL/CentOS/Fedora
   sudo dnf install util-linux

   # Arch Linux
   sudo pacman -S util-linux
   ```

> **💡 Tip**: Run `spindle start` to see which dependencies are missing with platform-specific install instructions.

> **⚠️ Important**: For UPC identification to work, the optical drive must be configured as user-mountable in `/etc/fstab`. Spindle will automatically detect configuration issues and show the exact commands needed to fix them.

### Install Spindle

**⚠️ IMPORTANT: Spindle requires uv package manager. Standard pip will not work.**

```bash
# Install as a global tool (recommended for end users)
uv tool install git+https://github.com/five82/spindle.git

# Or install in current environment
uv pip install git+https://github.com/five82/spindle.git
```

## Configuration

1. **Create configuration file**:
   ```bash
   spindle init-config
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

   # Optional: Enhanced content identification (for UPC/barcode lookups)
   # upcitemdb_api_key = "your_upc_api_key"        # Get from devs.upcitemdb.com

   # Optional: Notifications
   ntfy_topic = "https://ntfy.sh/your_topic"
   ```

3. **Get TMDB API key** from https://www.themoviedb.org/settings/api

4. **Get Plex token** - see Plex documentation

5. **Optional: Get UPC API key** from https://devs.upcitemdb.com for enhanced disc identification

## Usage

### Main Workflow - Continuous Processing
```bash
# Start Spindle (runs as background daemon by default)
spindle start
# Output:
# Checking system dependencies...
# Available dependencies: MakeMKV, drapto
# Missing optional dependencies (features will be disabled):
#   • udisks2: Automatic disc mounting (enables UPC identification)
#     Debian/Ubuntu: sudo apt install udisks2

# Or run in foreground for testing/debugging
spindle start --foreground

# Stop daemon when needed
spindle stop
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
spindle status

# View queue contents
spindle queue-list

# Clear completed items
spindle queue-clear --completed

# Test notifications
spindle test-notify
```

### Manual File Processing
```bash
# Add existing video files to queue (processed automatically by continuous mode)
spindle add-file /path/to/video.mkv
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

**Required** (validated at startup):
- **uv** - Python package manager (REQUIRED)
- **Python 3.11+**
- **MakeMKV** (`makemkvcon`) - DVD/Blu-ray ripping
- **drapto** - AV1 video encoder (Rust-based)

**Optional** (gracefully disabled if missing):
- **udisks2** (`udisksctl`) - Auto-mounting for UPC identification
- **eject utility** (`eject`) - Automatic disc ejection
- **Plex Media Server** - For automatic library imports

### Hardware Requirements
- Optical drive (DVD/Blu-ray)
- Sufficient storage for staging and final library
- Network access for TMDB API, optional UPC lookups, and Plex

### API Services
- **TMDB API** - Movie/TV identification (required, free)
- **UPCitemdb.com** - Enhanced barcode identification (optional, free tier: 100 lookups/day)
- **ntfy.sh** - Push notifications (optional, free)

## Development

For development setup, testing, and contribution guidelines, see [docs/development.md](docs/development.md).

## Troubleshooting

### UPC Identification Not Working

If you see "Phase 1 SKIPPED: Disc not mounted - UPC identification unavailable":

1. **Check dependency status:**
   ```bash
   spindle start  # Will show detailed dependency status and fstab configuration commands
   ```

2. **Configure fstab for user mounting:**
   ```bash
   echo '/dev/sr0 /media/$USER/optical udf,iso9660 ro,user,noauto 0 0' | sudo tee -a /etc/fstab
   sudo mkdir -p /media/$USER/optical && sudo chown $USER:$USER /media/$USER/optical
   ```

3. **Verify mounting works:**
   ```bash
   mount /dev/sr0  # Should mount without sudo
   umount /dev/sr0  # Clean up
   ```

### Content Identification Issues

- **Phase 1 (UPC)**: Requires disc to be mounted - configure fstab for user mounting
- **Phase 2 (Runtime)**: Requires meaningful disc names - "LOGICAL_VOLUME_ID" won't match anything
- **Phase 3 (Pattern)**: Fallback method - works with any disc but lower accuracy

### Permission Errors

If you see `Phase 1 SKIPPED: Disc not mounted - UPC identification unavailable`, you need to configure user-mountable optical drives:

#### Configure fstab for User Mounting

Add the optical drive to `/etc/fstab` to allow regular users to mount it:

```bash
# Add fstab entry for user-mountable optical drive
echo '/dev/sr0 /media/$USER/optical udf,iso9660 ro,user,noauto 0 0' | sudo tee -a /etc/fstab

# Create mount point with correct ownership
sudo mkdir -p /media/$USER/optical && sudo chown $USER:$USER /media/$USER/optical

# Test that it works
mount /dev/sr0  # Should mount without sudo
umount /dev/sr0  # Clean up
```

#### Troubleshooting

If `mount /dev/sr0` fails:
1. Check the fstab entry: `grep sr0 /etc/fstab`
2. Verify mount point exists: `ls -la /media/$USER/optical`
3. Check disc is detected: `lsblk | grep sr0`

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
