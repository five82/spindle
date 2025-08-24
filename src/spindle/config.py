"""Configuration management for Spindle."""

import os
from pathlib import Path

import tomli
from pydantic import BaseModel, Field, field_validator


class SpindleConfig(BaseModel):
    """Main configuration for Spindle."""

    # Paths - Generic defaults, MUST be configured in config.toml for your setup
    staging_dir: Path = Field(default=Path("~/.local/share/spindle/staging"))
    library_dir: Path = Field(default=Path("~/library"))
    log_dir: Path = Field(default=Path("~/.local/share/spindle/logs"))
    review_dir: Path = Field(default=Path("~/review"))

    # Hardware
    optical_drive: str = Field(default="/dev/sr0")

    # TMDB API
    tmdb_api_key: str | None = None
    tmdb_language: str = Field(default="en-US")

    # Drapto integration
    drapto_quality_sd: int = Field(default=23)
    drapto_quality_hd: int = Field(default=25)
    drapto_quality_uhd: int = Field(default=27)
    drapto_preset: int = Field(default=4)

    # Library organization
    movies_dir: str = Field(default="movies")
    tv_dir: str = Field(default="tv")

    # Plex settings
    plex_url: str | None = None
    plex_token: str | None = None
    movies_library: str = Field(default="Movies")
    tv_library: str = Field(default="TV Shows")

    # Notifications
    ntfy_topic: str | None = None

    # Timeout Settings (seconds)
    makemkv_rip_timeout: int = Field(default=3600)  # 1 hour
    makemkv_info_timeout: int = Field(default=60)  # 1 minute
    makemkv_eject_timeout: int = Field(default=30)  # 30 seconds
    drapto_version_timeout: int = Field(default=10)  # 10 seconds
    tmdb_request_timeout: int = Field(default=30)  # 30 seconds
    ntfy_request_timeout: int = Field(default=10)  # 10 seconds
    disc_monitor_timeout: int = Field(default=5)  # 5 seconds

    # Processing Intervals (seconds)
    queue_poll_interval: int = Field(default=5)  # Check queue every 5 seconds
    error_retry_interval: int = Field(default=10)  # Wait 10 seconds before retry
    status_display_interval: int = Field(default=30)  # Show status every 30 seconds
    plex_scan_interval: int = Field(default=5)  # Check Plex scan status

    # Content Detection & Analysis
    use_intelligent_disc_analysis: bool = Field(default=True)
    confidence_threshold: float = Field(default=0.7)
    prefer_api_over_heuristics: bool = Field(default=True)

    # Audio Track Selection
    include_all_english_audio: bool = Field(default=True)
    include_commentary_tracks: bool = Field(default=True)
    include_alternate_audio: bool = Field(default=False)

    # TV Series Detection
    tv_episode_min_duration: int = Field(default=18)  # minutes
    tv_episode_max_duration: int = Field(default=90)  # minutes
    rip_all_episodes: bool = Field(default=True)
    episode_mapping_strategy: str = Field(
        default="hybrid",
    )  # "sequential", "duration", "hybrid"

    # Movie Detection
    movie_min_duration: int = Field(default=70)  # minutes
    include_movie_extras: bool = Field(default=False)
    max_extras_duration: int = Field(default=30)  # minutes

    # Cartoon/Short Content Detection
    allow_short_content: bool = Field(default=True)
    cartoon_min_duration: int = Field(default=2)  # minutes
    cartoon_max_duration: int = Field(default=20)  # minutes
    detect_cartoon_collections: bool = Field(default=True)

    @field_validator(
        "staging_dir",
        "library_dir",
        "log_dir",
        "review_dir",
        mode="before",
    )
    @classmethod
    def expand_paths(cls, v: Path | str) -> Path:
        """Expand user home directory in paths."""
        if isinstance(v, str):
            v = Path(v)
        return v.expanduser().resolve()

    @field_validator("tmdb_api_key", mode="after")
    @classmethod
    def tmdb_key_required(cls, v: str | None) -> str | None:
        """Validate TMDB API key is provided."""
        if not v:
            # Try to get from environment
            v = os.getenv("TMDB_API_KEY")
        if not v:
            msg = "TMDB API key is required"
            raise ValueError(msg)
        return v

    @property
    def makemkv_con(self) -> str:
        """MakeMKV command-line tool executable name."""
        return "makemkvcon"

    @property
    def drapto_binary(self) -> str:
        """Drapto video encoder executable name."""
        return "drapto"

    def ensure_directories(self) -> None:
        """Create required directories if they don't exist."""
        for dir_path in [self.staging_dir, self.log_dir, self.review_dir]:
            dir_path.mkdir(parents=True, exist_ok=True)
            # No ownership changes needed - user owns their own directories


def load_config(config_path: Path | None = None) -> SpindleConfig:
    """Load configuration from file or defaults."""
    if config_path is None:
        # Check common config locations (user config first)
        possible_paths = [
            Path.home() / ".config" / "spindle" / "config.toml",  # User config
            Path.cwd() / "spindle.toml",  # Current directory
        ]

        for path in possible_paths:
            if path.exists():
                config_path = path
                break

    if config_path and config_path.exists():
        with open(config_path, "rb") as f:
            config_data = tomli.load(f)
        return SpindleConfig(**config_data)
    # Use defaults
    return SpindleConfig()


def create_sample_config(path: Path) -> None:
    """Create a sample configuration file."""
    sample_config = """# Spindle Configuration
# ====================
# Edit the REQUIRED settings below, then customize optional settings as needed.

# ============================================================================
# REQUIRED SETTINGS - You MUST change these before running Spindle
# ============================================================================

# TMDB API (required for media identification)
tmdb_api_key = "your_tmdb_api_key_here"           # Get from themoviedb.org/settings/api

# Directory paths - CRITICAL: Update these for your system
library_dir = "~/your-media-library"              # MUST EXIST: Your final media library directory

# Library Organization
movies_dir = "movies"                             # MUST EXIST: Subdirectory name for movies within library_dir
tv_dir = "tv"                                     # MUST EXIST: Subdirectory name for TV shows within library_dir

# ============================================================================
# COMMONLY CUSTOMIZED SETTINGS
# ============================================================================

# Directory paths
review_dir = "~/review"                           # Auto-created: Unidentified media for manual review
staging_dir = "~/.local/share/spindle/staging"    # Auto-created: Temporary files during processing
log_dir = "~/.local/share/spindle/logs"           # Auto-created: Log files and queue database

# Hardware
optical_drive = "/dev/sr0"                        # Device path for optical drive (may be /dev/sr1, etc.)

# Plex Integration (optional - remove section if not using Plex)
plex_url = "http://localhost:32400"               # Plex server URL
plex_token = "your_plex_token_here"               # Get from Plex Settings > Account > Authorized Devices
movies_library = "Movies"                         # Name of your Plex movie library
tv_library = "TV Shows"                           # Name of your Plex TV shows library

# Notifications (optional)
ntfy_topic = "https://ntfy.sh/your_topic"         # Notification service URL (create at ntfy.sh)

# ============================================================================
# OPTIONAL CUSTOMIZATION
# ============================================================================

# Metadata & Language
tmdb_language = "en-US"                           # Language for TMDB metadata (ISO 639-1)

# ============================================================================
# ADVANCED SETTINGS - Most users can leave these as defaults
# ============================================================================

# Content Detection & Analysis
use_intelligent_disc_analysis = true              # Enable AI-powered content detection (only disable for troubleshooting)
confidence_threshold = 0.7                        # Minimum confidence for automatic classification (0.0-1.0)
prefer_api_over_heuristics = true                 # Prioritize TMDB data over pattern analysis

# Media Duration Filtering (minutes)
tv_episode_min_duration = 18                      # Minimum episode length
tv_episode_max_duration = 90                      # Maximum episode length
movie_min_duration = 70                           # Minimum movie length
cartoon_min_duration = 2                          # Minimum cartoon length
cartoon_max_duration = 20                         # Maximum cartoon length
max_extras_duration = 30                          # Maximum extra content length

# Content Processing Behavior
rip_all_episodes = true                           # Rip all episodes on disc
episode_mapping_strategy = "hybrid"               # How to map titles to episodes: "duration", "sequential", "hybrid"
include_movie_extras = false                      # Include extras/deleted scenes
allow_short_content = true                        # Allow content < 20 minutes (cartoons)
detect_cartoon_collections = true                 # Detect Looney Tunes style collections

# Audio Track Selection
include_all_english_audio = true                  # Include main audio + commentaries
include_commentary_tracks = true                  # Include director/cast commentaries
include_alternate_audio = false                   # Include non-English audio tracks

# Video Encoding Quality (CRF values: lower = higher quality/larger files)
drapto_quality_sd = 23                            # Standard Definition (<1920px width) - CRF value 0-63
drapto_quality_hd = 25                            # High Definition (1920-3839px width) - CRF value 0-63
drapto_quality_uhd = 27                           # Ultra High Definition (>=3840px width) - CRF value 0-63
drapto_preset = 4                                 # SVT-AV1 preset 0-13 (lower = slower/better quality)

# Operation Timeouts (seconds)
makemkv_rip_timeout = 3600                        # MakeMKV ripping timeout (1 hour)
makemkv_info_timeout = 60                         # MakeMKV disc info timeout (1 minute)
makemkv_eject_timeout = 30                        # Disc eject timeout (30 seconds)
drapto_version_timeout = 10                       # Drapto version check timeout
tmdb_request_timeout = 30                         # TMDB API request timeout
ntfy_request_timeout = 10                         # Notification request timeout
disc_monitor_timeout = 5                          # Disc monitoring timeout

# Processing Intervals (seconds)
queue_poll_interval = 5                           # How often to check processing queue
error_retry_interval = 10                         # Wait time before retrying failed operations
status_display_interval = 30                      # How often to display status updates
plex_scan_interval = 5                            # How often to check Plex scan progress
"""

    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w") as f:
        f.write(sample_config)
