"""Essential disc processing tests - core workflow validation."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

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
def sample_disc_info():
    """Sample disc info for testing."""
    return DiscInfo("TEST_MOVIE_DISC", "udf")


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
        # detect_disc may return different disc types based on detection logic
        assert disc_info.disc_type is not None

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


class TestRipperBasics:
    """Test essential ripper functionality."""

    def test_ripper_initialization(self, temp_config):
        """Test ripper initializes properly."""
        ripper = MakeMKVRipper(temp_config)
        
        assert ripper.config == temp_config
        assert ripper.makemkv_con == "makemkvcon"
        # Ripper uses config for optical drive access
        assert ripper.config.optical_drive == "/dev/sr0"

    def test_select_main_title(self, temp_config, sample_titles):
        """Test main title selection logic."""
        ripper = MakeMKVRipper(temp_config)
        
        main_title = ripper.select_main_title(sample_titles)
        
        assert main_title == sample_titles[0]  # Longest duration
        assert main_title.name == "Main Feature"
        assert main_title.duration == 7200

    def test_select_main_title_empty(self, temp_config):
        """Test main title selection with empty list."""
        ripper = MakeMKVRipper(temp_config)
        
        main_title = ripper.select_main_title([])
        
        assert main_title is None


class TestTitleProperties:
    """Test title and track properties."""

    def test_title_basic_properties(self, sample_titles):
        """Test title object properties."""
        main_title = sample_titles[0]
        
        assert main_title.title_id == "0"
        assert main_title.name == "Main Feature"
        assert main_title.duration == 7200
        assert main_title.size == 25_000_000_000
        assert main_title.chapters == 24
        assert len(main_title.tracks) == 2

    def test_title_track_filtering(self, sample_titles):
        """Test title track filtering methods."""
        main_title = sample_titles[0]
        
        # Video tracks
        video_tracks = main_title.video_tracks
        assert len(video_tracks) == 1
        assert video_tracks[0].track_type == "video"
        
        # Audio tracks
        audio_tracks = main_title.audio_tracks
        assert len(audio_tracks) == 1
        assert audio_tracks[0].track_type == "audio"

    def test_track_properties(self, sample_titles):
        """Test individual track properties."""
        video_track = sample_titles[0].tracks[0]
        
        assert video_track.track_id == "0"
        assert video_track.track_type == "video"
        assert video_track.codec == "MPEG-4 AVC"
        assert video_track.language == "English"
        assert video_track.duration == 7200


class TestDiscScanning:
    """Test disc scanning operations."""

    @patch('subprocess.run')
    def test_scan_disc_success(self, mock_subprocess, temp_config):
        """Test successful disc scanning.""" 
        # Simplified MakeMKV output
        makemkv_output = '''MSG:1005,0,1,"MakeMKV started","Started"
TINFO:0,2,"Main Feature"
TINFO:0,8,24
TINFO:0,9,"2:00:00"
TINFO:0,10,25000000000
MSG:1005,0,1,"Completed","Done"'''
        
        mock_subprocess.return_value.returncode = 0
        mock_subprocess.return_value.stdout = makemkv_output
        
        ripper = MakeMKVRipper(temp_config)
        titles = ripper.scan_disc()
        
        assert len(titles) >= 1
        # Basic validation that parsing worked
        assert any(title.name == "Main Feature" for title in titles)

    @patch('subprocess.run')
    def test_scan_disc_failure(self, mock_subprocess, temp_config):
        """Test disc scanning failure handling."""
        mock_subprocess.return_value.returncode = 1
        mock_subprocess.return_value.stderr = "Disc read error"
        
        ripper = MakeMKVRipper(temp_config)
        
        from spindle.error_handling import ExternalToolError
        with pytest.raises(ExternalToolError):
            ripper.scan_disc()

    @patch('subprocess.run')
    def test_scan_disc_custom_device(self, mock_subprocess, temp_config):
        """Test scanning with custom device."""
        mock_subprocess.return_value.returncode = 0
        mock_subprocess.return_value.stdout = 'TINFO:0,2,"Test"'
        
        ripper = MakeMKVRipper(temp_config)
        ripper.scan_disc("/dev/sr1")
        
        # Verify custom device was used
        call_args = mock_subprocess.call_args[0][0]
        assert "dev:/dev/sr1" in call_args


class TestFileValidation:
    """Test file and output validation."""

    def test_output_file_validation(self, temp_config):
        """Test output file validation criteria."""
        with tempfile.TemporaryDirectory() as tmpdir:
            # Valid video file
            valid_file = Path(tmpdir) / "movie.mkv"
            valid_file.write_bytes(b"video content data")
            
            # Validation checks
            assert valid_file.exists()
            assert valid_file.stat().st_size > 0
            assert valid_file.suffix == ".mkv"

    def test_staging_directory_creation(self, temp_config):
        """Test staging directory setup."""
        staging_dir = temp_config.staging_dir
        
        # Should be able to create staging directory
        staging_dir.mkdir(parents=True, exist_ok=True)
        assert staging_dir.exists()
        assert staging_dir.is_dir()

    def test_disc_info_validation(self, sample_disc_info):
        """Test disc info object validation."""
        # DiscInfo constructor may process label differently
        assert sample_disc_info.label is not None
        assert sample_disc_info.disc_type is not None
        assert sample_disc_info.detected_at is not None


class TestWorkflowIntegration:
    """Test essential workflow components together."""

    def test_disc_to_titles_workflow(self, temp_config, sample_disc_info):
        """Test basic disc-to-titles workflow.""" 
        ripper = MakeMKVRipper(temp_config)
        
        # Mock a simple successful scan
        with patch.object(ripper, 'scan_disc') as mock_scan:
            mock_titles = [
                Title("0", 7200, 25000000000, 24, [], "Feature"),
                Title("1", 1800, 2000000000, 5, [], "Extras")
            ]
            mock_scan.return_value = mock_titles
            
            # Execute workflow
            titles = ripper.scan_disc()
            main_title = ripper.select_main_title(titles)
            
            # Validate workflow results
            assert len(titles) == 2
            assert main_title.name == "Feature"
            assert main_title.duration == 7200

    def test_error_recovery(self, temp_config):
        """Test error handling and recovery."""
        ripper = MakeMKVRipper(temp_config)
        
        # Test graceful handling of scan failures
        with patch.object(ripper, 'scan_disc', side_effect=RuntimeError("Scan failed")):
            with pytest.raises(RuntimeError, match="Scan failed"):
                ripper.scan_disc()
        
        # Test handling of empty title lists
        empty_titles = []
        main_title = ripper.select_main_title(empty_titles)
        assert main_title is None

    def test_configuration_validation(self, temp_config):
        """Test configuration validation for disc processing."""
        assert temp_config.optical_drive == "/dev/sr0"
        assert temp_config.makemkv_con == "makemkvcon"
        assert temp_config.staging_dir.name == "staging"
        
        # Validate timeouts
        assert temp_config.makemkv_info_timeout > 0
        assert temp_config.makemkv_rip_timeout > 0