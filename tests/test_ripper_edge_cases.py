"""Tests for edge cases and error conditions."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.ripper import MakeMKVRipper, Title, Track


@pytest.fixture
def mock_config():
    """Create a mock configuration for testing."""
    config = Mock(spec=SpindleConfig)
    config.makemkv_con = "makemkvcon"
    config.optical_drive = "/dev/sr0"
    config.makemkv_info_timeout = 60
    config.makemkv_rip_timeout = 3600
    config.makemkv_eject_timeout = 30
    config.movie_min_duration = 45
    config.tv_episode_min_duration = 15
    config.tv_episode_max_duration = 90
    config.cartoon_min_duration = 5
    config.cartoon_max_duration = 30
    config.max_extras_duration = 10
    config.include_movie_extras = True
    config.allow_short_content = True
    config.include_all_english_audio = True
    config.include_commentary_tracks = True
    config.include_alternate_audio = False
    config.tmdb_api_key = "test_key"
    config.confidence_threshold = 0.7
    config.use_intelligent_disc_analysis = True
    return config


@pytest.fixture
def temp_dirs():
    """Create temporary directories for testing."""
    temp_dir = Path(tempfile.mkdtemp())
    dirs = {"output": temp_dir / "output", "staging": temp_dir / "staging"}

    for dir_path in dirs.values():
        dir_path.mkdir(parents=True, exist_ok=True)

    yield dirs

    # Cleanup
    import shutil

    shutil.rmtree(temp_dir)


class TestMakeMKVRipperInit:
    """Test MakeMKVRipper initialization."""

    def test_init(self, mock_config):
        """Test ripper initialization."""
        ripper = MakeMKVRipper(mock_config)

        assert ripper.config == mock_config
        assert ripper.makemkv_con == "makemkvcon"


class TestEdgeCases:
    """Test edge cases and error conditions."""

    def test_title_name_sanitization(self, mock_config, temp_dirs):
        """Test that title names are properly sanitized for filenames."""
        ripper = MakeMKVRipper(mock_config)

        # Create title with special characters
        special_title = Title(
            "0",
            3600,
            1000000,
            10,
            [],
            name="Test: Movie & More! [2023]",
        )

        with patch("subprocess.run") as mock_subprocess:
            mock_result = Mock()
            mock_result.returncode = 0
            mock_subprocess.return_value = mock_result

            # Create expected sanitized output file
            expected_file = temp_dirs["output"] / "Test-Movie-More-2023.mkv"
            expected_file.write_text("content")

            result = ripper.rip_title(special_title, temp_dirs["output"])

            # Should handle special characters in filename
            assert "Test" in str(result)
            assert "Movie" in str(result)

    def test_makemkv_command_generation(self, mock_config, temp_dirs):
        """Test MakeMKV command generation with various track selections."""
        ripper = MakeMKVRipper(mock_config)
        
        # Create a sample title with tracks
        video_track = Track(
            track_id="0",
            track_type="video",
            codec="MPEG-4 AVC",
            language="English",
            duration=8130,
            size=25769803776,
            title="Main Video",
        )

        audio_track = Track(
            track_id="1",
            track_type="audio",
            codec="DTS-HD Master Audio",
            language="English",
            duration=8130,
            size=0,
            title="Main Audio",
        )

        title = Title(
            title_id="0",
            duration=8130,
            size=25769803776,
            chapters=21,
            tracks=[video_track, audio_track],
            name="Test Movie (2023)",
        )

        with patch("subprocess.run") as mock_subprocess:
            mock_result = Mock()
            mock_result.returncode = 0
            mock_subprocess.return_value = mock_result

            output_file = temp_dirs["output"] / "test.mkv"
            output_file.write_text("content")

            ripper.rip_title(title, temp_dirs["output"])

            # Check that command is properly formed
            call_args = mock_subprocess.call_args[0][0]

            # Should contain the basic makemkv command structure
            assert call_args[0] == "makemkvcon"
            assert call_args[1] == "mkv"
            assert call_args[2] == "--noscan"  # Skip scan flag
            assert call_args[3] == "--robot"  # Robot mode for structured output
            assert call_args[4].startswith("dev:")  # Device specification
            assert call_args[5] == title.title_id  # Title ID
            assert str(temp_dirs["output"]) in call_args[6]  # Output directory