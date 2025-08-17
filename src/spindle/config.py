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

    # MakeMKV settings
    makemkv_con: str = Field(default="makemkvcon")
    min_title_duration: int = Field(default=3600)  # 60 minutes in seconds

    # TMDB API
    tmdb_api_key: str | None = None
    tmdb_language: str = Field(default="en-US")

    # Drapto integration
    drapto_binary: str = Field(default="drapto")
    drapto_quality_hd: int = Field(default=25)
    drapto_quality_uhd: int = Field(default=27)
    drapto_preset: int = Field(default=4)

    # Plex settings
    plex_url: str | None = None
    plex_token: str | None = None
    movies_library: str = Field(default="Movies")
    tv_library: str = Field(default="TV Shows")

    # Notifications
    ntfy_topic: str | None = None

    # Processing
    auto_process: bool = Field(default=False)
    batch_size: int = Field(default=10)

    @field_validator("staging_dir", "library_dir", "log_dir", "review_dir", mode="before")
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
            raise ValueError("TMDB API key is required")
        return v

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

# Directory paths (REQUIRED - edit for your setup)
staging_dir = "~/.local/share/spindle/staging"  # Temporary files during processing
library_dir = "~/your-media-library"            # Your media library directory  
log_dir = "~/.local/share/spindle/logs"         # Log files
review_dir = "~/your-review-directory"          # Unidentified media

# Hardware
optical_drive = "/dev/sr0"

# MakeMKV settings
makemkv_con = "makemkvcon"
min_title_duration = 3600  # 60 minutes

# TMDB API (required)
tmdb_api_key = "your_tmdb_api_key_here"
tmdb_language = "en-US"

# Drapto encoding settings
drapto_binary = "drapto"
drapto_quality_hd = 25
drapto_quality_uhd = 27
drapto_preset = 4

# Plex settings (optional - remove if not using Plex)
plex_url = "http://localhost:32400"
plex_token = "your_plex_token_here"
movies_library = "Movies"
tv_library = "TV Shows"

# Notifications
ntfy_topic = "https://ntfy.sh/your_topic"

# Processing options
auto_process = false
batch_size = 10
"""

    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w") as f:
        f.write(sample_config)
