"""Integration tests combining multiple components."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.ripper import MakeMKVRipper


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


@pytest.fixture
def sample_makemkv_output():
    """Sample MakeMKV robot output for testing."""
    return """MSG:1005,0,1,"MakeMKV v1.17.4 linux(x64-release) started","MakeMKV v1.17.4 linux(x64-release) started"
MSG:2003,0,0,"Opening files on harddrive at /dev/sr0","Opening files on harddrive at /dev/sr0"
TINFO:0,2,"Test Movie (2023)"
TINFO:0,8,21
TINFO:0,9,"2:15:30"
TINFO:0,10,25769803776
TINFO:1,2,"Chapter 01"
TINFO:1,8,8
TINFO:1,9,"0:05:43"
TINFO:1,10,1073741824
SINFO:0,0,1,"Video"
SINFO:0,0,6,"MPEG-4 AVC"
SINFO:0,0,3,"English"
SINFO:0,0,30,"Main Video"
SINFO:0,1,1,"Audio"
SINFO:0,1,6,"DTS-HD Master Audio"
SINFO:0,1,3,"English"
SINFO:0,1,30,"Main Audio"
SINFO:0,2,1,"Audio"
SINFO:0,2,6,"DTS"
SINFO:0,2,3,"English"
SINFO:0,2,30,"Director's Commentary"
SINFO:0,3,1,"Subtitles"
SINFO:0,3,6,"PGS"
SINFO:0,3,3,"English"
SINFO:0,3,30,"Full Subtitles"
SINFO:1,0,1,"Video"
SINFO:1,0,6,"MPEG-4 AVC"
SINFO:1,0,3,"English"
MSG:1005,0,1,"Operation successfully completed","Operation successfully completed"
"""


class TestIntegration:
    """Integration tests combining multiple components."""

    @patch("subprocess.run")
    def test_full_workflow_integration(
        self,
        mock_subprocess,
        mock_config,
        temp_dirs,
        sample_makemkv_output,
    ):
        """Test complete workflow from scan to rip."""

        # Mock subprocess calls
        def subprocess_side_effect(*args, **kwargs):
            cmd = args[0]
            if "info" in cmd:
                # Return sample output for disc scan
                result = Mock()
                result.returncode = 0
                result.stdout = sample_makemkv_output
                return result
            if "mkv" in cmd:
                # Simulate ripping process
                result = Mock()
                result.returncode = 0
                # Create output file
                output_file = temp_dirs["output"] / "Test-Movie-2023.mkv"
                output_file.write_text("ripped content")
                return result
            return Mock(returncode=1)

        mock_subprocess.side_effect = subprocess_side_effect

        ripper = MakeMKVRipper(mock_config)
        result = ripper.rip_disc(temp_dirs["output"])

        assert result.exists()
        assert result.name == "Test-Movie-2023.mkv"
        # We expect at least scan + rip calls, but may have additional calls for disc label
        assert mock_subprocess.call_count >= 2