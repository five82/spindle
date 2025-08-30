"""Tests for disc ripping functionality."""

import subprocess
import tempfile
import time
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


@pytest.fixture
def sample_titles():
    """Create sample Title objects for testing."""
    video_track = Track(
        track_id="0",
        track_type="video",
        codec="MPEG-4 AVC",
        language="English",
        duration=8130,  # 2:15:30
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

    commentary_track = Track(
        track_id="2",
        track_type="audio",
        codec="DTS",
        language="English",
        duration=8130,
        size=0,
        title="Director's Commentary",
    )

    subtitle_track = Track(
        track_id="3",
        track_type="subtitle",
        codec="PGS",
        language="English",
        duration=0,
        size=0,
        title="Full Subtitles",
    )

    main_title = Title(
        title_id="0",
        duration=8130,  # 2:15:30
        size=25769803776,
        chapters=21,
        tracks=[video_track, audio_track, commentary_track, subtitle_track],
        name="Test Movie (2023)",
    )

    short_title = Title(
        title_id="1",
        duration=343,  # 5:43
        size=1073741824,
        chapters=8,
        tracks=[Track("0", "video", "MPEG-4 AVC", "English", 343, 1073741824)],
        name="Chapter 01",
    )

    return [main_title, short_title]


class TestRipping:
    """Test disc ripping functionality."""

    @patch("subprocess.run")
    def test_rip_title_success(
        self,
        mock_subprocess,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test successful title ripping."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result

        # Create a fake output file
        output_file = temp_dirs["output"] / "Test-Movie-2023.mkv"
        output_file.write_text("fake video content")

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        result = ripper.rip_title(title, temp_dirs["output"])

        assert result == output_file
        assert result.exists()

        # Check subprocess call
        mock_subprocess.assert_called_once()
        call_args = mock_subprocess.call_args[0][0]
        assert call_args[0] == "makemkvcon"
        assert call_args[1] == "mkv"
        assert "--robot" in call_args
        assert "dev:/dev/sr0" in call_args
        assert "0" in call_args  # title ID
        # No progress flag when no callback provided

    @patch("subprocess.run")
    def test_rip_title_failure(
        self,
        mock_subprocess,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test title ripping failure."""
        mock_result = Mock()
        mock_result.returncode = 1
        mock_result.stderr = "Rip failed"
        mock_subprocess.return_value = mock_result

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        with pytest.raises(RuntimeError, match="MakeMKV rip failed"):
            ripper.rip_title(title, temp_dirs["output"])

    @patch("subprocess.run")
    def test_rip_title_timeout(
        self,
        mock_subprocess,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test title ripping timeout."""
        mock_subprocess.side_effect = subprocess.TimeoutExpired("cmd", 3600)

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        with pytest.raises(RuntimeError, match="MakeMKV rip timed out"):
            ripper.rip_title(title, temp_dirs["output"])

    @patch("subprocess.run")
    def test_rip_title_no_output_file(
        self,
        mock_subprocess,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test title ripping when no output file is created."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        with pytest.raises(RuntimeError, match="No output file found"):
            ripper.rip_title(title, temp_dirs["output"])

    @patch("subprocess.run")
    def test_rip_title_multiple_output_files(
        self,
        mock_subprocess,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test title ripping with multiple output files (selects newest)."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result

        # Create multiple output files
        old_file = temp_dirs["output"] / "old_file.mkv"
        new_file = temp_dirs["output"] / "new_file.mkv"

        old_file.write_text("old content")
        # Make new file newer by touching it after a delay
        time.sleep(0.01)
        new_file.write_text("new content")

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        result = ripper.rip_title(title, temp_dirs["output"])

        assert result == new_file  # Should return the newer file

    @patch("subprocess.run")
    def test_rip_title_custom_device(
        self,
        mock_subprocess,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test title ripping with custom device."""
        mock_result = Mock()
        mock_result.returncode = 0
        mock_subprocess.return_value = mock_result

        output_file = temp_dirs["output"] / "test.mkv"
        output_file.write_text("content")

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        ripper.rip_title(title, temp_dirs["output"], device="/dev/sr2")

        call_args = mock_subprocess.call_args[0][0]
        assert "dev:/dev/sr2" in call_args

    def test_rip_title_creates_output_dir(self, mock_config, sample_titles, temp_dirs):
        """Test that rip_title creates output directory."""
        nonexistent_dir = temp_dirs["output"] / "new_subdir"
        assert not nonexistent_dir.exists()

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        with patch("subprocess.run") as mock_subprocess:
            mock_result = Mock()
            mock_result.returncode = 0
            mock_subprocess.return_value = mock_result

            # Create output file after directory is created
            def create_file(*args, **kwargs):
                output_file = nonexistent_dir / "test.mkv"
                output_file.write_text("content")
                return mock_result

            mock_subprocess.side_effect = create_file

            ripper.rip_title(title, nonexistent_dir)

            assert nonexistent_dir.exists()

    @patch("subprocess.Popen")
    def test_rip_title_with_progress_callback(self, mock_popen, mock_config, sample_titles, temp_dirs):
        """Test ripping with progress callback uses Popen and progress flag."""
        # Mock the Popen process
        mock_process = Mock()
        
        # Create an iterator that returns progress lines then empty strings
        def readline_generator():
            lines = [
                """PRGV:0,32768,65536""",  # 50% progress
                """PRGV:0,65536,65536""",  # 100% progress
            ]
            for line in lines:
                yield line
            # After the lines, return empty strings indefinitely
            while True:
                yield ""
        
        readline_iter = readline_generator()
        mock_process.stdout.readline.side_effect = lambda: next(readline_iter)
        
        # poll() should return None while running, then 0 when done
        poll_calls = 0
        def poll_side_effect():
            nonlocal poll_calls
            poll_calls += 1
            if poll_calls <= 2:  # First two calls return None (still running)
                return None
            return 0  # Process finished successfully
        
        mock_process.poll.side_effect = poll_side_effect
        mock_popen.return_value = mock_process
        
        # Create a fake output file
        output_file = temp_dirs["output"] / "Test-Movie-2023.mkv"
        output_file.write_text("fake video content")
        
        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]
        
        # Track progress calls
        progress_calls = []
        def progress_callback(data):
            progress_calls.append(data)
        
        result = ripper.rip_title(title, temp_dirs["output"], progress_callback=progress_callback)
        
        assert result == output_file
        
        # Check that Popen was used instead of run when progress callback provided
        mock_popen.assert_called_once()
        call_args, call_kwargs = mock_popen.call_args
        cmd = call_args[0]
        
        # Should contain progress flag and robot mode
        assert "--progress=-same" in cmd
        assert "--robot" in cmd
        
        # Should have received progress updates
        assert len(progress_calls) == 2
        assert progress_calls[0]["type"] == "ripping_progress"
        assert progress_calls[0]["percentage"] == 50.0
        assert progress_calls[1]["percentage"] == 100.0


class TestFullDiscRipping:
    """Test complete disc ripping workflow."""

    @patch.object(MakeMKVRipper, "rip_title")
    @patch.object(MakeMKVRipper, "select_main_title")
    @patch.object(MakeMKVRipper, "_get_disc_label")
    @patch.object(MakeMKVRipper, "scan_disc")
    def test_rip_disc_success(
        self,
        mock_scan,
        mock_get_label,
        mock_select,
        mock_rip,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test successful complete disc ripping."""
        mock_scan.return_value = sample_titles
        mock_get_label.return_value = "TEST_DISC"
        mock_select.return_value = sample_titles[0]

        output_file = temp_dirs["output"] / "ripped.mkv"
        mock_rip.return_value = output_file

        ripper = MakeMKVRipper(mock_config)
        result = ripper.rip_disc(temp_dirs["output"])

        assert result == output_file
        mock_scan.assert_called_once_with(None)
        mock_get_label.assert_called_once_with(None)
        mock_select.assert_called_once_with(sample_titles, "TEST_DISC")
        mock_rip.assert_called_once_with(sample_titles[0], temp_dirs["output"], None)

    @patch.object(MakeMKVRipper, "select_main_title")
    @patch.object(MakeMKVRipper, "_get_disc_label")
    @patch.object(MakeMKVRipper, "scan_disc")
    def test_rip_disc_no_title(
        self,
        mock_scan,
        mock_get_label,
        mock_select,
        mock_config,
        temp_dirs,
    ):
        """Test disc ripping when no suitable title found."""
        mock_scan.return_value = []
        mock_get_label.return_value = ""
        mock_select.return_value = None

        ripper = MakeMKVRipper(mock_config)

        with pytest.raises(RuntimeError, match="No suitable title found"):
            ripper.rip_disc(temp_dirs["output"])

    @patch.object(MakeMKVRipper, "rip_title")
    @patch.object(MakeMKVRipper, "select_main_title")
    @patch.object(MakeMKVRipper, "_get_disc_label")
    @patch.object(MakeMKVRipper, "scan_disc")
    def test_rip_disc_custom_device(
        self,
        mock_scan,
        mock_get_label,
        mock_select,
        mock_rip,
        mock_config,
        sample_titles,
        temp_dirs,
    ):
        """Test disc ripping with custom device."""
        mock_scan.return_value = sample_titles
        mock_get_label.return_value = "TEST_DISC"
        mock_select.return_value = sample_titles[0]

        output_file = temp_dirs["output"] / "ripped.mkv"
        mock_rip.return_value = output_file

        ripper = MakeMKVRipper(mock_config)
        ripper.rip_disc(temp_dirs["output"], device="/dev/sr3")

        mock_scan.assert_called_once_with("/dev/sr3")
        mock_get_label.assert_called_once_with("/dev/sr3")
        mock_rip.assert_called_once_with(
            sample_titles[0],
            temp_dirs["output"],
            "/dev/sr3",
        )