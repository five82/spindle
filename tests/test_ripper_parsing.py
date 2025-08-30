"""Tests for MakeMKV output parsing."""

import pytest
from unittest.mock import Mock

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

    def test_parse_duration_edge_cases(self, mock_config):
        """Test duration parsing with edge cases."""
        ripper = MakeMKVRipper(mock_config)

        # Test various invalid formats
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