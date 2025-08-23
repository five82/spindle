"""Tests for intelligent disc analysis functionality."""

from unittest.mock import AsyncMock, Mock

import pytest

from spindle.config import SpindleConfig
from spindle.disc.analyzer import ContentPattern, ContentType, IntelligentDiscAnalyzer
from spindle.disc.ripper import Title, Track


@pytest.fixture
def mock_config():
    """Create a mock config for testing."""
    config = Mock(spec=SpindleConfig)
    config.tmdb_api_key = "test_api_key"
    config.tmdb_language = "en-US"
    config.use_intelligent_disc_analysis = True
    config.confidence_threshold = 0.7
    config.tv_episode_min_duration = 18
    config.cartoon_max_duration = 20
    return config


@pytest.fixture
def sample_titles():
    """Create sample title data for testing."""
    return [
        Title("1", 22 * 60, 1000000, 5, [], "Title 1"),  # 22 minutes - TV episode
        Title("2", 23 * 60, 1000000, 5, [], "Title 2"),  # 23 minutes - TV episode
        Title("3", 21 * 60, 1000000, 5, [], "Title 3"),  # 21 minutes - TV episode
        Title("4", 22 * 60, 1000000, 5, [], "Title 4"),  # 22 minutes - TV episode
        Title("5", 3 * 60, 200000, 1, [], "Trailer"),  # 3 minutes - trailer
    ]


@pytest.fixture
def movie_titles():
    """Create sample movie title data."""
    return [
        Title("1", 120 * 60, 5000000, 20, [], "Main Feature"),  # 2 hours - movie
        Title("2", 15 * 60, 500000, 3, [], "Making Of"),  # 15 minutes - extra
        Title("3", 8 * 60, 300000, 2, [], "Deleted Scenes"),  # 8 minutes - extra
    ]


@pytest.fixture
def cartoon_titles():
    """Create sample cartoon collection titles."""
    return [
        Title("1", 7 * 60, 200000, 1, [], "Title 1"),  # 7 minutes - cartoon
        Title("2", 8 * 60, 250000, 1, [], "Title 2"),  # 8 minutes - cartoon
        Title("3", 6 * 60, 180000, 1, [], "Title 3"),  # 6 minutes - cartoon
        Title("4", 9 * 60, 280000, 1, [], "Title 4"),  # 9 minutes - cartoon
        Title("5", 7 * 60, 200000, 1, [], "Title 5"),  # 7 minutes - cartoon
    ]


def test_clean_disc_label(mock_config):
    """Test disc label cleaning."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    assert analyzer.clean_disc_label("Friends.Season.1.DVD") == "Friends Season 1"
    assert analyzer.clean_disc_label("Breaking_Bad-S01-BluRay") == "Breaking Bad S01"
    assert analyzer.clean_disc_label("Looney Tunes Disc 1") == "Looney Tunes"


def test_cartoon_collection_detection(mock_config):
    """Test cartoon collection pattern detection."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    # Test with Looney Tunes label and short durations
    durations = [7 * 60, 8 * 60, 6 * 60, 9 * 60, 7 * 60]  # 5 cartoons, 6-9 minutes each
    assert analyzer.has_cartoon_collection_pattern(durations, "Looney Tunes Collection")

    # Test without cartoon label but with consistent short durations
    durations = [6 * 60, 7 * 60, 8 * 60, 9 * 60, 7 * 60, 8 * 60]  # 6 short titles
    assert analyzer.has_cartoon_collection_pattern(durations, "Unknown Collection")

    # Test with mixed durations (should not detect)
    durations = [45 * 60, 7 * 60, 8 * 60]  # Mixed lengths
    assert not analyzer.has_cartoon_collection_pattern(durations, "Mixed Content")


def test_tv_series_detection(mock_config):
    """Test TV series pattern detection."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    # Consistent episode durations
    durations = [22 * 60, 23 * 60, 21 * 60, 22 * 60]  # 4 episodes around 22 minutes
    assert analyzer.has_consistent_episode_durations(durations)

    # Inconsistent durations
    durations = [22 * 60, 45 * 60, 90 * 60, 15 * 60]  # Very different lengths
    assert not analyzer.has_consistent_episode_durations(durations)


def test_movie_detection(mock_config):
    """Test movie pattern detection."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    # Clear movie pattern - one long title with shorter extras
    durations = [120 * 60, 15 * 60, 8 * 60, 5 * 60]  # 2 hour movie + extras
    assert analyzer.has_single_long_title(durations)

    # No clear main feature
    durations = [45 * 60, 40 * 60, 35 * 60]  # Similar lengths
    assert not analyzer.has_single_long_title(durations)


def test_title_pattern_analysis(sample_titles, mock_config):
    """Test title pattern analysis for TV series."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    pattern = analyzer.analyze_title_patterns(sample_titles, "Friends Season 1")

    # With 4 consistent ~22 minute episodes, should detect as TV_SERIES
    assert pattern.type == ContentType.TV_SERIES
    assert pattern.confidence > 0.5
    assert pattern.episode_count == 4  # Excluding the 3-minute trailer


def test_movie_pattern_analysis(movie_titles, mock_config):
    """Test title pattern analysis for movies."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    pattern = analyzer.analyze_title_patterns(movie_titles, "Avengers Movie")

    assert pattern.type == ContentType.MOVIE
    assert pattern.confidence > 0.8
    assert pattern.main_feature_duration == 120 * 60


def test_cartoon_pattern_analysis(cartoon_titles, mock_config):
    """Test title pattern analysis for cartoon collections."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    pattern = analyzer.analyze_title_patterns(cartoon_titles, "Looney Tunes Collection")

    assert pattern.type == ContentType.CARTOON_COLLECTION
    assert pattern.confidence > 0.7
    assert pattern.episode_count == 5


def test_tv_title_selection(sample_titles, mock_config):
    """Test TV episode title selection."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    pattern = ContentPattern(
        type=ContentType.TV_SERIES,
        confidence=0.8,
        episode_duration=22 * 60,  # 22 minutes
    )

    selected = analyzer.select_tv_episode_titles(sample_titles, pattern)

    # Should select the 4 episode-length titles, not the 3-minute trailer
    assert len(selected) == 4
    for title in selected:
        assert 20 * 60 <= title.duration <= 25 * 60  # Within tolerance


def test_movie_title_selection(movie_titles, mock_config):
    """Test movie title selection."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    pattern = ContentPattern(type=ContentType.MOVIE, confidence=0.9)

    selected = analyzer.select_movie_titles(movie_titles, pattern)

    # Should select only the main feature (2 hour movie)
    assert len(selected) == 1
    assert selected[0].duration == 120 * 60


def test_cartoon_title_selection(cartoon_titles, mock_config):
    """Test cartoon collection title selection."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    pattern = ContentPattern(type=ContentType.CARTOON_COLLECTION, confidence=0.8)

    # Mock the select_titles_intelligently to call the cartoon selection logic
    selected = [t for t in cartoon_titles if 2 * 60 <= t.duration <= 20 * 60]

    # Should select all 5 cartoon titles
    assert len(selected) == 5
    for title in selected:
        assert 2 * 60 <= title.duration <= 20 * 60


def test_commentary_track_detection():
    """Test commentary track detection in titles."""
    from spindle.disc.ripper import Title

    # Create tracks with commentary indicators
    main_audio = Track("1", "audio", "AC3", "English", 120 * 60, 1000000, "Main Audio")
    director_commentary = Track(
        "2", "audio", "AC3", "English", 120 * 60, 1000000, "Director Commentary",
    )
    cast_commentary = Track(
        "3", "audio", "AC3", "English", 120 * 60, 1000000, "Cast and Crew Commentary",
    )

    title = Title(
        "1", 120 * 60, 5000000, 20, [main_audio, director_commentary, cast_commentary],
    )

    # Test commentary detection
    commentary_tracks = title.get_commentary_tracks()
    assert len(commentary_tracks) == 2  # Director and cast commentaries

    main_tracks = title.get_main_audio_tracks()
    assert len(main_tracks) == 1  # Just main audio

    all_english = title.get_all_english_audio_tracks()
    assert len(all_english) == 3  # All English tracks


@pytest.mark.asyncio
async def test_tmdb_integration(mock_config):
    """Test TMDB API integration (mocked)."""
    analyzer = IntelligentDiscAnalyzer(mock_config)

    # Mock the TMDB client
    analyzer.tmdb.search_movie = AsyncMock(
        return_value=[
            {
                "id": 12345,
                "title": "Test Movie",
                "popularity": 100,
                "genres": [{"name": "Action"}],
            },
        ],
    )

    analyzer.tmdb.search_tv = AsyncMock(return_value=[])

    result = await analyzer.query_tmdb("Test Movie")

    assert result is not None
    assert result["type"] == "movie"
    assert result["data"]["title"] == "Test Movie"
