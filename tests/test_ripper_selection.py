"""Tests for title and track selection logic."""

import pytest
from unittest.mock import Mock

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