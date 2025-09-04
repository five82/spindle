"""Essential disc processing tests - core workflow validation."""

import tempfile
from pathlib import Path
from unittest.mock import patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.monitor import DiscInfo, detect_disc, eject_disc
from spindle.disc.ripper import MakeMKVRipper, Title, Track


@pytest.fixture
def temp_config():
    """Create temporary config for testing."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging", 
            library_dir=Path(tmpdir) / "library",
            optical_drive="/dev/sr0",
            makemkv_con="makemkvcon",
        )


@pytest.fixture
def sample_titles():
    """Sample title list for testing."""
    return [
        Title(
            title_id="0",
            name="Main Feature", 
            duration=7200,  # 2 hours
            size=25_000_000_000,  # 25GB
            chapters=24,
            tracks=[
                Track("0", "video", "MPEG-4 AVC", "English", 7200, 20_000_000_000),
                Track("1", "audio", "DTS-HD MA", "English", 7200, 0, "Main Audio"),
            ],
        ),
        Title(
            title_id="1",
            name="Extras",
            duration=1800,  # 30 min
            size=2_000_000_000,
            chapters=5,
            tracks=[Track("0", "video", "MPEG-4 AVC", "English", 1800, 2_000_000_000)],
        ),
    ]


class TestDiscDetection:
    """Test essential disc detection functionality."""

    @patch('subprocess.run')
    def test_detect_disc_success(self, mock_subprocess):
        """Test successful disc detection."""
        mock_subprocess.return_value.returncode = 0
        mock_subprocess.return_value.stdout = 'LABEL="TEST_MOVIE" TYPE="udf"'
        
        disc_info = detect_disc("/dev/sr0")
        
        assert disc_info is not None
        assert "TEST_MOVIE" in disc_info.label

    @patch('subprocess.run')
    def test_detect_disc_no_disc(self, mock_subprocess):
        """Test detection when no disc present."""
        mock_subprocess.return_value.returncode = 2  # No disc
        
        disc_info = detect_disc("/dev/sr0")
        
        assert disc_info is None

    @patch('subprocess.run')
    def test_eject_disc_success(self, mock_subprocess):
        """Test successful disc ejection."""
        mock_subprocess.return_value.returncode = 0
        
        result = eject_disc("/dev/sr0")
        
        assert result is True


class TestRipperWorkflow:
    """Test essential ripper workflow functionality."""

    def test_ripper_initialization(self, temp_config):
        """Test ripper initializes properly."""
        ripper = MakeMKVRipper(temp_config)
        
        assert ripper.config == temp_config
        assert ripper.makemkv_con == "makemkvcon"

    def test_select_main_title(self, temp_config, sample_titles):
        """Test main title selection logic."""
        ripper = MakeMKVRipper(temp_config)
        
        main_title = ripper.select_main_title(sample_titles)
        
        assert main_title == sample_titles[0]  # Longest duration
        assert main_title.duration == 7200

    @patch('subprocess.run')
    def test_scan_disc_success(self, mock_subprocess, temp_config):
        """Test successful disc scanning.""" 
        makemkv_output = '''MSG:1005,0,1,"MakeMKV started","Started"
TINFO:0,2,"Main Feature"
TINFO:0,9,"2:00:00"
MSG:1005,0,1,"Completed","Done"'''
        
        mock_subprocess.return_value.returncode = 0
        mock_subprocess.return_value.stdout = makemkv_output
        
        ripper = MakeMKVRipper(temp_config)
        titles = ripper.scan_disc()
        
        assert len(titles) >= 1
        assert any(title.name == "Main Feature" for title in titles)

    @patch('subprocess.run')
    def test_scan_disc_failure(self, mock_subprocess, temp_config):
        """Test disc scanning failure handling."""
        mock_subprocess.return_value.returncode = 1
        mock_subprocess.return_value.stderr = "Disc read error"
        
        ripper = MakeMKVRipper(temp_config)
        
        from spindle.error_handling import ToolError
        with pytest.raises(ToolError):
            ripper.scan_disc()


class TestWorkflowIntegration:
    """Test end-to-end workflow components."""

    def test_disc_to_titles_workflow(self, temp_config):
        """Test basic disc-to-titles workflow.""" 
        ripper = MakeMKVRipper(temp_config)
        
        with patch.object(ripper, 'scan_disc') as mock_scan:
            mock_titles = [
                Title("0", 7200, 25000000000, 24, [], "Feature"),
                Title("1", 1800, 2000000000, 5, [], "Extras")
            ]
            mock_scan.return_value = mock_titles
            
            titles = ripper.scan_disc()
            main_title = ripper.select_main_title(titles)
            
            assert len(titles) == 2
            assert main_title.name == "Feature"
            assert main_title.duration == 7200

    def test_error_recovery(self, temp_config):
        """Test error handling and recovery."""
        ripper = MakeMKVRipper(temp_config)
        
        with patch.object(ripper, 'scan_disc', side_effect=RuntimeError("Scan failed")):
            with pytest.raises(RuntimeError, match="Scan failed"):
                ripper.scan_disc()
        
        empty_titles = []
        main_title = ripper.select_main_title(empty_titles)
        assert main_title is None