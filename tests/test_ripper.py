"""Tests for MakeMKV ripper integration."""

import subprocess
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


class TestTrack:
    """Test Track class."""

    def test_track_initialization(self):
        """Test Track object initialization."""
        track = Track(
            track_id="1",
            track_type="video",
            codec="H.264",
            language="English",
            duration=7200,
            size=5000000000,
            title="Main Video",
            is_default=True,
        )

        assert track.track_id == "1"
        assert track.track_type == "video"
        assert track.codec == "H.264"
        assert track.language == "English"
        assert track.duration == 7200
        assert track.size == 5000000000
        assert track.title == "Main Video"
        assert track.is_default is True

    def test_track_str(self):
        """Test Track string representation."""
        track = Track("1", "audio", "DTS", "English", 3600, 1000000)
        expected = "audio track 1: DTS (English)"
        assert str(track) == expected


class TestTitle:
    """Test Title class."""

    def test_title_initialization(self, sample_titles):
        """Test Title object initialization."""
        title = sample_titles[0]  # Main title

        assert title.title_id == "0"
        assert title.duration == 8130
        assert title.size == 25769803776
        assert title.chapters == 21
        assert len(title.tracks) == 4
        assert title.name == "Test Movie (2023)"

    def test_title_with_no_name(self):
        """Test Title with default name generation."""
        title = Title("5", 3600, 1000000, 10, [])
        assert title.name == "Title 5"

    def test_video_tracks_property(self, sample_titles):
        """Test video tracks filtering."""
        title = sample_titles[0]
        video_tracks = title.video_tracks

        assert len(video_tracks) == 1
        assert video_tracks[0].track_type == "video"
        assert video_tracks[0].codec == "MPEG-4 AVC"

    def test_audio_tracks_property(self, sample_titles):
        """Test audio tracks filtering."""
        title = sample_titles[0]
        audio_tracks = title.audio_tracks

        assert len(audio_tracks) == 2
        assert all(t.track_type == "audio" for t in audio_tracks)

    def test_subtitle_tracks_property(self, sample_titles):
        """Test subtitle tracks filtering."""
        title = sample_titles[0]
        subtitle_tracks = title.subtitle_tracks

        assert len(subtitle_tracks) == 1
        assert subtitle_tracks[0].track_type == "subtitle"

    def test_get_english_audio_tracks(self, sample_titles):
        """Test English audio track filtering."""
        title = sample_titles[0]
        english_tracks = title.get_english_audio_tracks()

        assert len(english_tracks) == 2
        assert all(t.language.lower().startswith("en") for t in english_tracks)

    def test_get_commentary_tracks(self, sample_titles):
        """Test commentary track detection."""
        title = sample_titles[0]
        commentary_tracks = title.get_commentary_tracks()

        assert len(commentary_tracks) == 1
        assert "commentary" in commentary_tracks[0].title.lower()

    def test_get_main_audio_tracks(self, sample_titles):
        """Test main audio track filtering (excludes commentary)."""
        title = sample_titles[0]
        main_tracks = title.get_main_audio_tracks()

        assert len(main_tracks) == 1
        assert "commentary" not in main_tracks[0].title.lower()

    def test_get_all_english_audio_tracks(self, sample_titles):
        """Test getting all English audio tracks."""
        title = sample_titles[0]
        all_english = title.get_all_english_audio_tracks()

        assert len(all_english) == 2
        assert all(t.language.lower().startswith("en") for t in all_english)

    def test_title_str(self, sample_titles):
        """Test Title string representation."""
        title = sample_titles[0]
        result = str(title)

        assert "Test Movie (2023)" in result
        assert "02:15:30" in result  # Duration formatted as HH:MM:SS
        assert "4 tracks" in result


class TestMakeMKVRipperInit:
    """Test MakeMKVRipper initialization."""

    def test_init(self, mock_config):
        """Test ripper initialization."""
        ripper = MakeMKVRipper(mock_config)

        assert ripper.config == mock_config
        assert ripper.makemkv_con == "makemkvcon"


class TestMakeMKVOutputParsing:
    """Test MakeMKV output parsing."""

    def test_parse_duration_valid(self, mock_config):
        """Test parsing valid duration strings."""
        ripper = MakeMKVRipper(mock_config)

        assert ripper._parse_duration("2:15:30") == 8130
        assert ripper._parse_duration("0:05:43") == 343
        assert ripper._parse_duration("1:30:00") == 5400

    def test_parse_duration_invalid(self, mock_config):
        """Test parsing invalid duration strings."""
        ripper = MakeMKVRipper(mock_config)

        assert ripper._parse_duration("invalid") == 0
        assert ripper._parse_duration("1:2") == 0  # Wrong format
        assert ripper._parse_duration("") == 0

    def test_parse_makemkv_output(self, mock_config, sample_makemkv_output):
        """Test parsing complete MakeMKV robot output."""
        ripper = MakeMKVRipper(mock_config)
        titles = ripper._parse_makemkv_output(sample_makemkv_output)

        assert len(titles) == 2

        # Check main title
        main_title = titles[0]
        assert main_title.title_id == "0"
        assert main_title.name == "Test Movie (2023)"
        assert main_title.duration == 8130  # 2:15:30
        assert main_title.chapters == 21
        assert len(main_title.tracks) == 4

        # Check tracks
        video_tracks = main_title.video_tracks
        assert len(video_tracks) == 1
        assert video_tracks[0].codec == "MPEG-4 AVC"

        audio_tracks = main_title.audio_tracks
        assert len(audio_tracks) == 2
        assert audio_tracks[0].codec == "DTS-HD Master Audio"
        assert audio_tracks[1].codec == "DTS"
        assert "commentary" in audio_tracks[1].title.lower()

        subtitle_tracks = main_title.subtitle_tracks
        assert len(subtitle_tracks) == 1
        assert subtitle_tracks[0].codec == "PGS"

    def test_parse_empty_output(self, mock_config):
        """Test parsing empty MakeMKV output."""
        ripper = MakeMKVRipper(mock_config)
        titles = ripper._parse_makemkv_output("")

        assert titles == []

    def test_parse_malformed_output(self, mock_config):
        """Test parsing malformed MakeMKV output."""
        ripper = MakeMKVRipper(mock_config)
        malformed_output = """
        TINFO:invalid,line,format
        SINFO:also,invalid
        MSG:some,message
        """

        titles = ripper._parse_makemkv_output(malformed_output)
        assert titles == []


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


class TestTitleSelection:
    """Test title selection logic."""

    def test_select_main_title_empty_list(self, mock_config):
        """Test selecting main title from empty list."""
        ripper = MakeMKVRipper(mock_config)
        result = ripper.select_main_title([])

        assert result is None

    def test_select_main_title_basic(self, mock_config, sample_titles):
        """Test basic title selection (longest duration)."""
        ripper = MakeMKVRipper(mock_config)
        result = ripper.select_main_title(sample_titles)

        assert result == sample_titles[0]  # Longest title
        assert result.name == "Test Movie (2023)"


class TestTrackSelection:
    """Test track selection for ripping."""

    def test_select_tracks_basic(self, mock_config, sample_titles):
        """Test basic track selection."""
        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        selected = ripper._select_tracks_for_rip(title)

        # Should include video, main audio, and commentary
        track_types = [t.track_type for t in selected]
        assert "video" in track_types
        assert "audio" in track_types

        # Check that video track is included
        video_tracks = [t for t in selected if t.track_type == "video"]
        assert len(video_tracks) == 1

    def test_select_tracks_no_commentary(self, mock_config, sample_titles):
        """Test track selection without commentary."""
        mock_config.include_commentary_tracks = False
        mock_config.include_all_english_audio = False  # Don't include all English audio
        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        selected = ripper._select_tracks_for_rip(title)

        # Should not include commentary tracks
        commentary_tracks = [
            t for t in selected if "commentary" in (t.title or "").lower()
        ]
        assert len(commentary_tracks) == 0

    def test_select_tracks_no_all_english_audio(self, mock_config, sample_titles):
        """Test track selection with limited English audio."""
        mock_config.include_all_english_audio = False
        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        selected = ripper._select_tracks_for_rip(title)

        # Should include main audio but not commentary by default
        audio_tracks = [t for t in selected if t.track_type == "audio"]
        main_tracks = [
            t for t in audio_tracks if "commentary" not in (t.title or "").lower()
        ]

        assert len(main_tracks) >= 1

    def test_select_tracks_with_alternate_audio(self, mock_config, sample_titles):
        """Test track selection with alternate audio languages."""
        mock_config.include_alternate_audio = True

        # Add a non-English audio track for testing
        spanish_track = Track("4", "audio", "DTS", "Spanish", 8130, 0, "Spanish Audio")
        sample_titles[0].tracks.append(spanish_track)

        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        selected = ripper._select_tracks_for_rip(title)

        # Should include all audio tracks
        audio_tracks = [t for t in selected if t.track_type == "audio"]
        languages = [t.language for t in audio_tracks]

        assert "Spanish" in languages

    def test_select_tracks_deduplication(self, mock_config, sample_titles):
        """Test that track selection removes duplicates."""
        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

        selected = ripper._select_tracks_for_rip(title)

        # Check no duplicate track IDs
        track_ids = [t.track_id for t in selected]
        assert len(track_ids) == len(set(track_ids))


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
        import time

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

    def test_track_selection_edge_cases(self, mock_config):
        """Test track selection with various edge cases."""
        ripper = MakeMKVRipper(mock_config)

        # Title with no tracks
        empty_title = Title("0", 3600, 1000000, 10, [])
        selected = ripper._select_tracks_for_rip(empty_title)
        assert selected == []

        # Title with only subtitle tracks
        subtitle_track = Track("0", "subtitle", "PGS", "English", 0, 0)
        subtitle_only_title = Title("0", 3600, 1000000, 10, [subtitle_track])
        selected = ripper._select_tracks_for_rip(subtitle_only_title)
        assert len(selected) == 0  # Subtitles not selected by default

    def test_parse_makemkv_progress_formats(self, mock_config):
        """Test parsing various MakeMKV progress output formats."""
        ripper = MakeMKVRipper(mock_config)
        
        # Test regular progress format
        result = ripper._parse_makemkv_progress("Current progress - 25% , Total progress - 30%")
        assert result is not None
        assert result["type"] == "ripping_progress"
        assert result["percentage"] == 30  # Uses total progress
        assert result["current"] == 25
        assert result["stage"] == "Saving to MKV file"
        
        # Test robot progress format (PRGV) with significant change
        # PRGV format is PRGV:current,total,max where max is always 65536
        result = ripper._parse_makemkv_progress('PRGV:32768,32768,65536')
        assert result is not None  # First call should report (50% is > 5% threshold from -1)
        assert result["type"] == "ripping_progress" 
        assert result["percentage"] == 50.0
        assert result["current"] == 32768
        assert result["maximum"] == 65536
        assert result["stage"] == "Saving to MKV file"
        
        # Test filtering of minor progress updates
        result = ripper._parse_makemkv_progress('PRGV:33000,33000,65536')
        assert result is None  # Should be filtered (50.3% is < 5% change from 50%)
        
        # Test filtering of track completion when total is 0
        result = ripper._parse_makemkv_progress('PRGV:65536,0,65536')
        assert result is None  # Should be filtered (individual track complete but total not started)
        
        # Test significant progress update
        result = ripper._parse_makemkv_progress('PRGV:36045,36045,65536')
        assert result is not None  # Should report (55.0% is >= 5% change from 50%)
        
        # Test action messages
        result = ripper._parse_makemkv_progress("Current action: Processing BD+ code")
        assert result is not None
        assert result["type"] == "ripping_status"
        assert result["message"] == "Processing BD+ code"
        
        # Test operation messages
        result = ripper._parse_makemkv_progress("Current operation: Opening Blu-ray disc")
        assert result is not None
        assert result["type"] == "ripping_status"
        assert result["message"] == "Opening Blu-ray disc"
        
        # Test non-progress lines
        result = ripper._parse_makemkv_progress("File 00002.mpls was added as title #0")
        assert result is None
        
        result = ripper._parse_makemkv_progress("Using LibreDrive mode")
        assert result is None

    def test_parse_duration_edge_cases(self, mock_config):
        """Test duration parsing with edge cases."""
        ripper = MakeMKVRipper(mock_config)

        # Test various invalid formats - skip None test since it would cause AttributeError
        assert ripper._parse_duration("1:2:3:4") == 0  # Too many parts
        assert ripper._parse_duration("invalid") == 0
        assert ripper._parse_duration("") == 0
        assert ripper._parse_duration("1:2") == 0  # Too few parts

        # Test boundary values
        assert ripper._parse_duration("0:00:00") == 0
        assert ripper._parse_duration("23:59:59") == 86399

        # Test that function doesn't validate time values - it just converts
        # (25 hours * 3600) + (61 minutes * 60) + 70 seconds = 93730
        assert ripper._parse_duration("25:61:70") == 93730

        # Test MakeMKV format with prefix
        assert ripper._parse_duration('0,"1:39:03') == 5943  # 1*3600 + 39*60 + 3
        assert ripper._parse_duration('0,"0:07:05') == 425  # 7*60 + 5
        assert ripper._parse_duration('0,"0:04:55') == 295  # 4*60 + 55

    def test_makemkv_command_generation(self, mock_config, sample_titles, temp_dirs):
        """Test MakeMKV command generation with various track selections."""
        ripper = MakeMKVRipper(mock_config)
        title = sample_titles[0]

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

