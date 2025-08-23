"""Tests for media identification."""

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaIdentifier, MediaInfo


def test_media_info_movie():
    """Test MediaInfo for movies."""
    media = MediaInfo(
        title="The Matrix",
        year=1999,
        media_type="movie",
        tmdb_id=603,
        overview="A computer hacker learns about reality.",
        genres=["Action", "Science Fiction"],
    )

    assert media.is_movie is True
    assert media.is_tv_show is False
    assert media.get_filename() == "The Matrix (1999)"
    assert str(media) == "The Matrix (1999)"


def test_media_info_tv_episode():
    """Test MediaInfo for TV episodes."""
    media = MediaInfo(
        title="Breaking Bad",
        year=2008,
        media_type="tv",
        tmdb_id=1396,
        season=1,
        episode=1,
        episode_title="Pilot",
    )

    assert media.is_movie is False
    assert media.is_tv_show is True
    assert media.get_filename() == "Breaking Bad - S01E01 - Pilot"
    assert str(media) == "Breaking Bad (2008) S01E01"


def test_media_info_library_paths():
    """Test library path generation."""
    from pathlib import Path

    library_root = Path("/home/user/library")

    # Movie
    movie = MediaInfo("The Matrix", 1999, "movie", 603)
    movie_path = movie.get_library_path(library_root)
    assert movie_path == library_root / "Movies" / "The Matrix (1999)"

    # TV Show
    tv = MediaInfo("Breaking Bad", 2008, "tv", 1396, season=1, episode=1)
    tv_path = tv.get_library_path(library_root)
    assert tv_path == library_root / "TV Shows" / "Breaking Bad (2008)" / "Season 01"


def test_filename_parsing():
    """Test filename parsing functionality."""
    config = SpindleConfig(tmdb_api_key="fake_key")
    identifier = MediaIdentifier(config)

    # Test movie filename
    title, year, season, episode = identifier.parse_filename("The.Matrix.1999.mkv")
    assert title == "The Matrix"
    assert year == 1999
    assert season is None
    assert episode is None

    # Test TV show filename
    title, year, season, episode = identifier.parse_filename(
        "Breaking.Bad.S01E01.Pilot.mkv"
    )
    assert title == "Breaking Bad"
    assert season == 1
    assert episode == 1

    # Test with different format
    title, year, season, episode = identifier.parse_filename(
        "Game.of.Thrones.1x01.Winter.Is.Coming.mkv"
    )
    assert title == "Game of Thrones"
    assert season == 1
    assert episode == 1

    # Test with parentheses year
    title, year, season, episode = identifier.parse_filename(
        "Inception (2010) BluRay.mkv"
    )
    assert title == "Inception"
    assert year == 2010

    # Test cleaning
    title, year, season, episode = identifier.parse_filename(
        "The_Dark_Knight-2008.DISC1.mkv"
    )
    assert title == "The Dark Knight"
    assert year == 2008


def test_filename_edge_cases():
    """Test edge cases in filename parsing."""
    config = SpindleConfig(tmdb_api_key="fake_key")
    identifier = MediaIdentifier(config)

    # Empty/minimal filename
    title, year, season, episode = identifier.parse_filename("video.mkv")
    assert title == "video"
    assert year is None

    # Multiple years (should take first)
    title, year, season, episode = identifier.parse_filename("Movie.1999.2000.mkv")
    assert year == 1999

    # No extension
    title, year, season, episode = identifier.parse_filename("Movie.Name.2020")
    assert title == "Movie Name"
    assert year == 2020


def test_safe_filename_generation():
    """Test that generated filenames are filesystem-safe."""
    # Movie with special characters
    media = MediaInfo("Spider-Man: Into the Spider-Verse", 2018, "movie", 324857)
    filename = media.get_filename()
    assert filename == "Spider-Man Into the Spider-Verse (2018)"

    # TV show with special characters
    media = MediaInfo(
        "Marvel's Agents of S.H.I.E.L.D.",
        2013,
        "tv",
        1403,
        season=1,
        episode=1,
        episode_title="Pilot",
    )
    filename = media.get_filename()
    assert filename == "Marvels Agents of SHIELD - S01E01 - Pilot"
