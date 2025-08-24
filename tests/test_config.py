"""Tests for configuration management."""

import tempfile
from pathlib import Path

from spindle.config import SpindleConfig, create_sample_config, load_config


def test_spindle_config_defaults():
    """Test default configuration values."""
    config = SpindleConfig()

    assert str(config.staging_dir) == "~/.local/share/spindle/staging"
    assert str(config.library_dir) == "~/library"
    assert config.optical_drive == "/dev/sr0"
    assert config.makemkv_con == "makemkvcon"
    assert config.tmdb_language == "en-US"
    assert config.drapto_binary == "drapto"
    assert config.drapto_quality_sd == 23
    assert config.drapto_quality_hd == 25
    assert config.drapto_quality_uhd == 27
    assert config.drapto_preset == 4
    assert config.movies_library == "Movies"
    assert config.tv_library == "TV Shows"

    # Content detection defaults
    assert config.use_intelligent_disc_analysis is True
    assert config.confidence_threshold == 0.7
    assert config.include_all_english_audio is True
    assert config.include_commentary_tracks is True
    assert config.tv_episode_min_duration == 18
    assert config.allow_short_content is True
    assert config.cartoon_max_duration == 20


def test_config_path_expansion():
    """Test that paths are properly expanded."""
    config = SpindleConfig(
        staging_dir="~/spindle/staging",
        library_dir="~/library/media",
    )

    assert config.staging_dir == Path.home() / "spindle" / "staging"
    assert config.library_dir == Path.home() / "library" / "media"


def test_create_sample_config():
    """Test creating a sample configuration file."""
    with tempfile.TemporaryDirectory() as tmpdir:
        config_path = Path(tmpdir) / "config.toml"

        create_sample_config(config_path)

        assert config_path.exists()
        assert config_path.is_file()

        # Check that it contains expected content
        content = config_path.read_text()
        assert "staging_dir" in content
        assert "tmdb_api_key" in content
        assert "plex_url" in content


def test_load_config_from_file():
    """Test loading configuration from a file."""
    with tempfile.TemporaryDirectory() as tmpdir:
        config_path = Path(tmpdir) / "config.toml"

        # Create a test config file
        config_content = """
staging_dir = "/test/staging"
library_dir = "/test/library"
optical_drive = "/dev/sr1"
tmdb_api_key = "test_key"
drapto_quality_hd = 20
"""
        config_path.write_text(config_content)

        config = load_config(config_path)

        assert config.staging_dir == Path("/test/staging")
        assert config.library_dir == Path("/test/library")
        assert config.optical_drive == "/dev/sr1"
        assert config.tmdb_api_key == "test_key"
        assert config.drapto_quality_hd == 20


def test_load_config_defaults():
    """Test loading configuration with defaults when no file exists."""
    with tempfile.TemporaryDirectory() as tmpdir:
        nonexistent_path = Path(tmpdir) / "nonexistent.toml"

        # Should use defaults and not raise an error
        config = load_config(nonexistent_path)

        assert str(config.staging_dir) == "~/.local/share/spindle/staging"
        assert config.optical_drive == "/dev/sr0"


def test_config_ensure_directories():
    """Test that ensure_directories creates required directories."""
    with tempfile.TemporaryDirectory() as tmpdir:
        config = SpindleConfig(
            staging_dir=Path(tmpdir) / "staging",
            log_dir=Path(tmpdir) / "logs",
        )

        # Directories shouldn't exist yet
        assert not config.staging_dir.exists()
        assert not config.log_dir.exists()

        config.ensure_directories()

        # Now they should exist
        assert config.staging_dir.exists()
        assert config.log_dir.exists()
