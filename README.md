# spindle

Automated disc ripping, encoding, and media library management system.

## Overview

spindle automates the complete workflow from physical disc to organized media library with optimal resource usage:

1. **Disc Detection** - Automatically detects inserted physical discs
2. **Content Identification** - Analyzes disc content and identifies media using TMDB API
3. **Intelligent Ripping** - Uses MakeMKV to extract only selected titles with proper naming
4. **Disc Ejection** - Frees optical drive immediately after ripping for next disc
5. **Background Encoding** - Processes video through drapto for efficient AV1 encoding (non-blocking)
6. **Organization & Import** - Organizes files in Plex-compatible structure and triggers library scans

## Features

- **Optimized Workflow** - Clean separation of identification and ripping phases for maximum efficiency
- **True "Insert and Forget"** - Disc ejected immediately after ripping, multiple discs can be in background processing
- **Background Queue Processing** - Encoding and organization happen in background while you process more discs
- **Enhanced Content Identification** - Multi-tier identification system with episode mapping for TV shows  
- **Quality Encoding** - Integration with drapto for optimized AV1 compression (concurrent processing)
- **Immediate Plex Availability** - Movies/shows available as soon as encoding completes
- **Real-time Progress Tracking** - SQLite-based queue with detailed status and progress updates
- **Smart Notifications** - Real-time updates via ntfy.sh for key milestones (ripped, completed, errors)
- **Phase-aware Error Handling** - Separate error handling for identification vs ripping with actionable solutions
- **Manual Processing** - Process existing video files when needed
- **System Dependency Checking** - Automatic validation with helpful install guidance

## Enhanced Content Identification

Spindle uses a **multi-tier identification system** for maximum accuracy:

### Tier 1: Runtime-Verified Search (High Confidence)
- **Disc name analysis** - Cleans and searches disc labels
- **Duration matching** - Compares disc runtime with TMDB data
- **Edition detection** - Distinguishes theatrical vs extended cuts automatically
- **High reliability** for discs with meaningful names

### Tier 2: Intelligent Pattern Analysis (Medium Confidence)
- **TMDB API integration** - Powered by https://www.themoviedb.org
- **Content type detection** - Movies vs TV series (based on disc structure, not genre)
- **Smart title selection** - Automatically selects main content vs extras
- **Fallback reliability** when other methods fail

### Special Features
- **Local caching** - Stores TMDB lookups to minimize API usage
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

4. **Disc Automounting** - For enhanced metadata extraction (HIGHLY RECOMMENDED)
   
   **What it does**: Allows Spindle to read disc metadata files (bdmt_eng.xml, mcmf.xml) for better content identification
   
   **Without automounting**: 
   - ✅ Disc ripping still works (MakeMKV accesses device directly)
   - ⚠️ Reduced identification accuracy (Phase 1 metadata extraction skipped)
   - ⚠️ Falls back to basic disc label and runtime matching only
   
   **Desktop Systems**: Automatic disc mounting is handled by desktop environments (GNOME, KDE, etc.) - no additional configuration needed.
   
   **Server Systems**: Configure automounting via fstab:
   ```bash
   sudo mkdir -p /media/cdrom
   echo '/dev/sr0 /media/cdrom udf,iso9660 ro,auto 0 0' | sudo tee -a /etc/fstab
   sudo mount -a  # Apply changes
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

> **💡 Disc Automounting**: While not required for ripping, automounting significantly improves content identification accuracy by allowing access to disc metadata files. Desktop systems handle this automatically. Server systems need the fstab configuration shown above.


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


   # Optional: Notifications
   ntfy_topic = "https://ntfy.sh/your_topic"
   ```

3. **Get TMDB API key** from https://www.themoviedb.org/settings/api

4. **Get Plex token** - see Plex documentation


## Usage

### Main Workflow - Continuous Processing
```bash
# Start Spindle (runs as background daemon by default)
spindle start
# Output:
# Checking system dependencies...
# Available dependencies: MakeMKV, drapto, eject utility

# Or run in foreground for testing/debugging
spindle start --foreground

# Stop daemon when needed
spindle stop
```

By default, `spindle start` runs as a background daemon with optimized workflow:
1. **Insert a disc** → Automatically identified and ripped with intelligent title selection
2. **Disc ejected immediately** → Ready for next disc (drive freed for continuous processing)
3. **Background processing** → Encode → Organize → Import to Plex (concurrent with new disc processing)
4. **Repeat** → Each movie or TV show becomes available in Plex as soon as encoding completes

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
spindle queue list

# Clear completed items
spindle queue clear --completed

# Test notifications
spindle test-notify
```

### Manual File Processing
```bash
# Add existing video files to queue (processed automatically by continuous mode)
spindle add-file /path/to/video.mkv
```

## Workflow Details

### Optimized Workflow (Default)
```
Disc 1: Insert → Identify → Rip → Eject (Ready for Disc 2)
           ↓ (background processing - non-blocking)
           Encode → Organize → Import → Available in Plex

Disc 2: Insert → Identify → Rip → Eject (Ready for Disc 3)
           ↓ (background processing - concurrent with Disc 1)
           Encode → Organize → Import → Available in Plex

... and so on (multiple discs in various background stages)
```

**Phase Separation Benefits:**
- **Identification first** - Determines what to rip and how to name files
- **Intelligent ripping** - Only extracts selected titles with proper filenames  
- **Immediate disc ejection** - Drive freed as soon as ripping completes
- **Concurrent background processing** - Multiple items encoding simultaneously
- **Maximum throughput** - Can process many discs while previous ones encode
- **Optimal resource usage** - Expensive optical drive used minimally

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
- **Disc automounting** - Highly recommended for enhanced metadata extraction (not required for ripping)
- **eject utility** (`eject`) - Automatic disc ejection
- **Plex Media Server** - For automatic library imports

### Hardware Requirements
- Optical drive (DVD/Blu-ray)
- Sufficient storage for staging and final library
- Network access for TMDB API and Plex

### API Services
- **TMDB API** - Movie/TV identification (required, free)
- **ntfy.sh** - Push notifications (optional, free)

## Development

For development setup, testing, and contribution guidelines, see [docs/development.md](docs/development.md).

## Troubleshooting

### Disc Not Found at Standard Mount Points

If you see "Disc not found at standard mount points" in the logs:

**This is NOT an error** - it just means enhanced metadata extraction is disabled. The disc will still be ripped successfully.

To enable enhanced metadata extraction:

1. **Desktop Systems:** Check that your desktop environment is automounting discs (insert disc and verify it appears in file manager)

2. **Server Systems:** Configure automounting via fstab:
   ```bash
   sudo mkdir -p /media/cdrom
   echo '/dev/sr0 /media/cdrom udf,iso9660 ro,auto 0 0' | sudo tee -a /etc/fstab
   sudo mount -a  # Apply changes
   ```

3. **Verify mounting works:**
   ```bash
   # Insert a disc, then check if it's mounted
   ls -la /media/cdrom /media/cdrom0
   # You should see disc contents (BDMV, VIDEO_TS, etc.)
   ```

### Workflow Issues

**Disc Not Ejecting:**
- Only successful rips eject the disc automatically
- If disc stays in drive, check logs for identification or ripping errors
- Failed identification or ripping keeps disc inserted for retry

**Stuck in IDENTIFYING Status:**
- Check TMDB API connectivity and API key
- Verify disc is properly mounted (if using enhanced identification)
- Check logs for specific identification errors

**Content Identification Issues:**
- **Tier 1 (Runtime-Verified)**: Requires meaningful disc names - generic labels won't match
- **Tier 2 (Pattern Analysis)**: Fallback method - works with any disc but lower accuracy
- **Episode Mapping**: TV shows get individual episode titles and proper S##E## formatting

## Enhanced Error Handling

Spindle features a comprehensive phase-aware error handling system:

- **Phase-specific Errors**: Separate handling for identification, ripping, encoding, and organization failures
- **Categorized Error Types**: Configuration, dependency, hardware, media, and system errors
- **Rich Console Display**: Color-coded messages with emojis and clear guidance  
- **Actionable Solutions**: Specific steps to resolve each type of error with phase context
- **Smart Classification**: Automatically identifies common issues and provides targeted help
- **Recovery Guidance**: Clear distinction between recoverable and critical errors

### Automatic Error Recovery

- **Unidentified Media**: Moved to `review_dir/unidentified/` (configurable)
- **Failed Encoding**: Marked as failed in queue, notifications sent
- **Plex Import Failures**: Logged and marked as failed
- **Network Issues**: Retries with exponential backoff

## Notifications

Spindle sends notifications for key workflow milestones:
- **Disc detection** - New disc inserted and identified
- **Rip completion & disc ejection** - Ready for next disc
- **Encoding progress** - Background processing updates  
- **Media added to Plex** - Available for viewing
- **Queue status changes** - Phase transitions and progress
- **Errors and failures** - Phase-specific error details with recovery guidance
