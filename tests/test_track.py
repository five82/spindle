"""Tests for Track class."""

from spindle.disc.ripper import Track


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