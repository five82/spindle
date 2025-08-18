#!/bin/bash

# Install Spindle as a user systemd service
# Run as the user who will operate Spindle (NOT as root)
# REQUIRES: uv package manager

set -e

# Ensure we're not running as root
if [ "$EUID" -eq 0 ]; then
    echo "ERROR: Do not run this script as root!"
    echo "Run as the user who will operate Spindle:"
    echo "  ./scripts/install-user-service.sh"
    exit 1
fi

USER_HOME="$HOME"
CONFIG_DIR="$USER_HOME/.config/spindle"
SYSTEMD_DIR="$USER_HOME/.config/systemd/user"
SPINDLE_DIR="$USER_HOME/.local/share/spindle"

echo "Installing Spindle as user service for: $(whoami)"
echo "Home directory: $USER_HOME"

# Check for uv
echo "Checking for uv package manager..."
if ! command -v uv >/dev/null 2>&1; then
    echo "ERROR: uv package manager is required but not found!"
    echo "Install uv first:"
    echo "  curl -LsSf https://astral.sh/uv/install.sh | sh"
    echo "  source ~/.bashrc  # or restart terminal"
    exit 1
fi
echo "✓ uv found at: $(which uv)"

# Create directories
echo "Creating directories..."
mkdir -p "$CONFIG_DIR"
mkdir -p "$SYSTEMD_DIR" 
mkdir -p "$SPINDLE_DIR"

# Check if user is in optical/cdrom groups
echo "Checking group membership for disc access..."
if ! groups | grep -q "optical\|cdrom"; then
    echo "WARNING: User $(whoami) is not in optical or cdrom group"
    echo "You may need to add yourself to these groups for disc access:"
    echo "  sudo usermod -a -G optical,cdrom $(whoami)"
    echo "  # Then log out and back in"
fi

# Create user systemd service
echo "Creating user systemd service..."
cat > "$SYSTEMD_DIR/spindle.service" << 'EOF'
[Unit]
Description=Spindle Media Processing Service (User)
After=graphical-session.target

[Service]
Type=simple
ExecStart=/usr/bin/env bash -c 'cd %h && uv run spindle start --foreground'
Restart=on-failure
RestartSec=5
TimeoutStartSec=300
TimeoutStopSec=30

# Working directory
WorkingDirectory=%h

# Environment
Environment=HOME=%h
Environment=XDG_CONFIG_HOME=%h/.config
Environment=XDG_DATA_HOME=%h/.local/share

[Install]
WantedBy=default.target
EOF

# Reload user systemd
systemctl --user daemon-reload

# Create sample config if it doesn't exist
if [ ! -f "$CONFIG_DIR/config.toml" ]; then
    echo "Creating sample configuration..."
    
    # Try to use spindle to create config
    if command -v uv >/dev/null 2>&1; then
        uv run spindle init-config --path "$CONFIG_DIR/config.toml" 2>/dev/null || create_fallback_config
    else
        create_fallback_config
    fi
fi

create_fallback_config() {
    cat > "$CONFIG_DIR/config.toml" << EOF
# Spindle Configuration
# Edit paths below for your setup

# Directory paths (using user home directory)
staging_dir = "$USER_HOME/.local/share/spindle/staging"
library_dir = "$USER_HOME/your-media-library"      # Edit: Your media library directory
log_dir = "$USER_HOME/.local/share/spindle/logs"
review_dir = "$USER_HOME/your-review-directory"   # Edit: Where unidentified media goes

# Hardware
optical_drive = "/dev/sr0"                         # Edit if needed

# REQUIRED: TMDB API key (get from https://www.themoviedb.org/settings/api)
tmdb_api_key = "your_tmdb_api_key_here"

# REQUIRED: Plex settings (edit for your Plex server)
plex_url = "http://localhost:32400"                # Edit: Your Plex server URL
plex_token = "your_plex_token_here"                # Edit: Your Plex token

# Optional: Notifications
ntfy_topic = "https://ntfy.sh/your_topic"          # Edit or remove

# Encoding settings (defaults are usually fine)
drapto_quality_hd = 25
drapto_quality_uhd = 27
drapto_preset = 4
EOF
}

echo
echo "Spindle user service installed successfully!"
echo
echo "Configuration file: $CONFIG_DIR/config.toml"
echo "Service file: $SYSTEMD_DIR/spindle.service"
echo
echo "Next steps:"
echo "1. Edit configuration: nano $CONFIG_DIR/config.toml"
echo "   - Set correct paths for your media library and review directories"
echo "   - Add your TMDB API key and Plex token"
echo "2. Enable user service: systemctl --user enable spindle"
echo "3. Start service: systemctl --user start spindle"
echo "4. Check status: systemctl --user status spindle"
echo "5. View logs: journalctl --user -u spindle -f"
echo
echo "To enable autostart on boot (optional):"
echo "  sudo loginctl enable-linger $(whoami)"
echo
echo "All files are in your home directory - no root permissions needed!"