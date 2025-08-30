"""Tests for Title class."""

import pytest

from spindle.disc.ripper import Title, Track


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