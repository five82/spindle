"""Tests for disc scanning functionality."""

import subprocess
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


class TestDiscScanning:
    """Test disc scanning functionality."""

    @patch("subprocess.run")
    def test_scan_disc_success(
        self,
        mock_subprocess,
        mock_config,
        sample_makemkv_output,
    ):
        """Test successful disc scanning."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = sample_makemkv_output
        mock_subprocess.return_value = mock_result

        ripper = MakeMKVRipper(mock_config)
        titles = ripper.scan_disc()

        mock_subprocess.assert_called_once_with(
            ["makemkvcon", "info", "dev:/dev/sr0", "--robot"],
            check=False,
            capture_output=True,
            text=True,
            timeout=60,
        )

        assert len(titles) == 2
        assert titles[0].name == "Test Movie (2023)"

    @patch("subprocess.run")
    def test_scan_disc_custom_device(
        self,
        mock_subprocess,
        mock_config,
        sample_makemkv_output,
    ):
        """Test disc scanning with custom device."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = sample_makemkv_output
        mock_subprocess.return_value = mock_result

        ripper = MakeMKVRipper(mock_config)
        titles = ripper.scan_disc("/dev/sr1")

        mock_subprocess.assert_called_once_with(
            ["makemkvcon", "info", "dev:/dev/sr1", "--robot"],
            check=False,
            capture_output=True,
            text=True,
            timeout=60,
        )

        assert len(titles) == 2

    @patch("subprocess.run")
    def test_scan_disc_failure(self, mock_subprocess, mock_config):
        """Test disc scanning failure."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_result.stderr = "Disc read error"
        mock_subprocess.return_value = mock_result

        ripper = MakeMKVRipper(mock_config)

        with pytest.raises(RuntimeError, match="MakeMKV scan failed"):
            ripper.scan_disc()

    @patch("subprocess.run")
    def test_scan_disc_timeout(self, mock_subprocess, mock_config):
        """Test disc scanning timeout."""
        mock_subprocess.side_effect = subprocess.TimeoutExpired("cmd", 60)

        ripper = MakeMKVRipper(mock_config)

        with pytest.raises(RuntimeError, match="MakeMKV scan timed out"):
            ripper.scan_disc()

    @patch("subprocess.run")
    def test_scan_disc_subprocess_error(self, mock_subprocess, mock_config):
        """Test disc scanning subprocess error."""
        mock_subprocess.side_effect = subprocess.CalledProcessError(1, "cmd", "error")

        ripper = MakeMKVRipper(mock_config)

        with pytest.raises(RuntimeError, match="MakeMKV scan failed"):
            ripper.scan_disc()


class TestDiscLabelRetrieval:
    """Test disc label retrieval functionality."""

    @patch("subprocess.run")
    def test_get_disc_label_success(self, mock_subprocess, mock_config):
        """Test successful disc label retrieval."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_result.stdout = 'DRV:0 "TEST_MOVIE_DISC" name test\nOther line'
        mock_subprocess.return_value = mock_result

        ripper = MakeMKVRipper(mock_config)
        label = ripper._get_disc_label()

        assert label == "TEST_MOVIE_DISC"

    @patch("subprocess.run")
    @patch("os.path.exists")
    def test_get_disc_label_fallback(self, mock_exists, mock_subprocess, mock_config):
        """Test disc label retrieval with fallback method."""
        # First call (MakeMKV) fails
        mock_subprocess.side_effect = [
            Mock(returncode=1),  # MakeMKV fails
            Mock(returncode=0, stdout="FALLBACK_LABEL\n"),  # lsblk succeeds
        ]
        mock_exists.return_value = True

        ripper = MakeMKVRipper(mock_config)
        label = ripper._get_disc_label()

        assert label == "FALLBACK_LABEL"
        assert mock_subprocess.call_count == 2

    @patch("subprocess.run")
    def test_get_disc_label_failure(self, mock_subprocess, mock_config):
        """Test disc label retrieval failure."""
        mock_subprocess.side_effect = Exception("Command failed")

        ripper = MakeMKVRipper(mock_config)
        label = ripper._get_disc_label()

        assert label == ""

    @patch("subprocess.run")
    def test_get_disc_label_timeout(self, mock_subprocess, mock_config):
        """Test disc label retrieval timeout."""
        mock_subprocess.side_effect = subprocess.TimeoutExpired("cmd", 30)

        ripper = MakeMKVRipper(mock_config)
        label = ripper._get_disc_label()

        assert label == ""