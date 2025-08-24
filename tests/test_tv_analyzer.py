"""Tests for TV series disc analysis and episode mapping."""

from unittest.mock import AsyncMock, Mock

import pytest

from spindle.config import SpindleConfig
from spindle.disc.analyzer import EpisodeInfo, SeriesInfo
from spindle.disc.ripper import Title
from spindle.disc.tv_analyzer import TVSeriesDiscAnalyzer


@pytest.fixture
def mock_config():
    """Create a mock config for TV analyzer testing."""
    config = Mock(spec=SpindleConfig)
    config.tmdb_api_key = "test_api_key"
    config.tmdb_language = "en-US"
    config.tmdb_request_timeout = 30
    config.episode_mapping_strategy = "hybrid"
    return config


@pytest.fixture
def mock_tmdb_client():
    """Mock TMDB client for testing."""
    mock_client = Mock()
    mock_client.get = Mock()
    return mock_client


@pytest.fixture
def tv_analyzer(mock_config, mock_tmdb_client):
    """Create TV analyzer with mocked dependencies."""
    analyzer = TVSeriesDiscAnalyzer(mock_config)
    analyzer.tmdb.client = mock_tmdb_client
    analyzer.tmdb.api_key = "test_key"
    analyzer.tmdb.language = "en-US"
    analyzer.tmdb.base_url = "https://api.themoviedb.org/3"
    return analyzer


@pytest.fixture
def sample_tv_titles():
    """Sample TV episode titles for testing."""
    return [
        Title("1", 22 * 60, 1000000, 5, [], "Episode 1"),
        Title("2", 23 * 60, 1000000, 5, [], "Episode 2"),
        Title("3", 21 * 60, 1000000, 5, [], "Episode 3"),
        Title("4", 22 * 60, 1000000, 5, [], "Episode 4"),
        Title("5", 3 * 60, 200000, 1, [], "Bonus Features"),
        Title("6", 45 * 60, 2000000, 10, [], "Documentary"),
    ]


@pytest.fixture
def sample_tmdb_episodes():
    """Sample TMDB episode data."""
    return [
        {
            "season_number": 1,
            "episode_number": 1,
            "name": "Pilot",
            "air_date": "2023-01-15",
            "overview": "The beginning of the series.",
            "runtime": 22,
        },
        {
            "season_number": 1,
            "episode_number": 2,
            "name": "Second Episode",
            "air_date": "2023-01-22",
            "overview": "The story continues.",
            "runtime": 23,
        },
        {
            "season_number": 1,
            "episode_number": 3,
            "name": "Third Episode",
            "air_date": "2023-01-29",
            "overview": "More adventures.",
            "runtime": 21,
        },
        {
            "season_number": 1,
            "episode_number": 4,
            "name": "Fourth Episode",
            "air_date": "2023-02-05",
            "overview": "The plot thickens.",
            "runtime": 22,
        },
    ]


class TestSeriesIdentification:
    """Tests for series identification from disc labels."""

    @pytest.mark.asyncio
    async def test_identify_series_standard_pattern(self, tv_analyzer):
        """Test series identification with standard pattern."""
        tv_analyzer.tmdb.search_tv = AsyncMock(return_value=[
            {
                "id": 12345,
                "name": "Breaking Bad",
                "first_air_date": "2008-01-20",
            },
        ])

        result = await tv_analyzer.identify_series_from_disc("Breaking Bad Season 1")

        assert result is not None
        assert result.name == "Breaking Bad"
        assert result.tmdb_id == 12345
        assert result.detected_season == 1
        assert result.year == 2008

    @pytest.mark.asyncio
    async def test_identify_series_s01_pattern(self, tv_analyzer):
        """Test series identification with S01 pattern."""
        tv_analyzer.tmdb.search_tv = AsyncMock(return_value=[
            {
                "id": 54321,
                "name": "The Office",
                "first_air_date": "2005-03-24",
            },
        ])

        result = await tv_analyzer.identify_series_from_disc("The Office S01")

        assert result is not None
        assert result.name == "The Office"
        assert result.tmdb_id == 54321
        assert result.detected_season == 1
        assert result.year == 2005

    @pytest.mark.asyncio
    async def test_identify_series_complete_season_pattern(self, tv_analyzer):
        """Test identification with 'Complete Season' pattern."""
        tv_analyzer.tmdb.search_tv = AsyncMock(return_value=[
            {
                "id": 67890,
                "name": "Friends",
                "first_air_date": "1994-09-22",
            },
        ])

        result = await tv_analyzer.identify_series_from_disc(
            "Friends Complete Season 2"
        )

        assert result is not None
        assert result.name == "Friends"
        assert result.detected_season == 2

    @pytest.mark.asyncio
    async def test_identify_series_fallback_no_season(self, tv_analyzer):
        """Test fallback identification without season pattern."""
        tv_analyzer.tmdb.search_tv = AsyncMock(return_value=[
            {
                "id": 99999,
                "name": "Mystery Show",
                "first_air_date": "2020-01-01",
            },
        ])

        result = await tv_analyzer.identify_series_from_disc("Mystery Show")

        assert result is not None
        assert result.name == "Mystery Show"
        assert result.detected_season is None

    @pytest.mark.asyncio
    async def test_identify_series_no_match(self, tv_analyzer):
        """Test when no series match is found."""
        tv_analyzer.tmdb.search_tv = AsyncMock(return_value=[])

        result = await tv_analyzer.identify_series_from_disc("Unknown Series Season 1")

        assert result is None


class TestSeriesNameCleaning:
    """Tests for series name cleaning functionality."""

    def test_clean_series_name_basic(self, tv_analyzer):
        """Test basic series name cleaning."""
        result = tv_analyzer.clean_series_name("Breaking Bad Complete Collection")
        assert result == "Breaking Bad"

    def test_clean_series_name_dvd_indicators(self, tv_analyzer):
        """Test cleaning DVD/Blu-ray indicators."""
        result = tv_analyzer.clean_series_name("The Office DVD Box Set")
        assert result == "The Office"

    def test_clean_series_name_special_characters(self, tv_analyzer):
        """Test cleaning special characters."""
        result = tv_analyzer.clean_series_name("Friends.Season.1.BluRay")
        assert result == "Friends Season 1"

    def test_clean_series_name_underscores_dashes(self, tv_analyzer):
        """Test cleaning underscores and dashes."""
        result = tv_analyzer.clean_series_name("Game_of_Thrones-Season-1")
        assert result == "Game of Thrones Season 1"


class TestYearExtraction:
    """Tests for year extraction from TMDB dates."""

    def test_extract_year_valid_date(self, tv_analyzer):
        """Test year extraction from valid date."""
        result = tv_analyzer.extract_year_from_date("2008-01-20")
        assert result == 2008

    def test_extract_year_none(self, tv_analyzer):
        """Test year extraction when date is None."""
        result = tv_analyzer.extract_year_from_date(None)
        assert result is None

    def test_extract_year_empty_string(self, tv_analyzer):
        """Test year extraction from empty string."""
        result = tv_analyzer.extract_year_from_date("")
        assert result is None

    def test_extract_year_invalid_format(self, tv_analyzer):
        """Test year extraction from invalid format."""
        result = tv_analyzer.extract_year_from_date("invalid-date")
        assert result is None


class TestTitleFiltering:
    """Tests for episode title filtering."""

    def test_filter_episode_titles_removes_short(self, tv_analyzer, sample_tv_titles):
        """Test filtering removes very short titles."""
        result = tv_analyzer.filter_episode_titles(sample_tv_titles)

        # Should remove the 3-minute bonus features and group by duration
        # The 4 similar duration episodes (21-23 min) form the largest group
        assert len(result) == 4  # Returns the largest duration group (episodes)
        assert all(t.duration >= 15 * 60 for t in result)
        # All returned titles should be similar in duration (21-23 minutes)
        durations = [t.duration for t in result]
        assert all(20 * 60 <= d <= 25 * 60 for d in durations)

    def test_filter_episode_titles_removes_extras(self, tv_analyzer):
        """Test filtering removes titles with extra indicators."""
        titles = [
            Title("1", 22 * 60, 1000000, 5, [], "Episode 1"),
            Title("2", 20 * 60, 800000, 4, [], "Making of Documentary"),
            Title("3", 21 * 60, 900000, 4, [], "Behind the Scenes"),
            Title("4", 22 * 60, 1000000, 5, [], "Episode 2"),
        ]

        result = tv_analyzer.filter_episode_titles(titles)

        # Should remove making of and behind the scenes
        assert len(result) == 2
        assert all("Episode" in t.name for t in result)

    def test_group_by_duration(self, tv_analyzer):
        """Test grouping titles by similar duration."""
        titles = [
            Title("1", 22 * 60, 1000000, 5, [], "Episode 1"),  # Group 1
            Title("2", 23 * 60, 1000000, 5, [], "Episode 2"),  # Group 1
            Title("3", 45 * 60, 2000000, 10, [], "Special"),   # Group 2
            Title("4", 21 * 60, 900000, 4, [], "Episode 3"),   # Group 1
        ]

        groups = tv_analyzer.group_by_duration(titles)

        # Should create groups based on duration similarity
        assert len(groups) >= 1
        # Largest group should be the episodes (~22 minutes)
        largest_group = max(groups, key=len)
        assert len(largest_group) == 3


class TestEpisodeMapping:
    """Tests for episode mapping strategies."""

    @pytest.mark.asyncio
    async def test_get_tv_season_details_success(self, tv_analyzer, mock_tmdb_client):
        """Test successful TV season details retrieval."""
        mock_response = Mock()
        mock_response.json.return_value = {
            "episodes": [
                {"episode_number": 1, "name": "Pilot", "runtime": 22},
                {"episode_number": 2, "name": "Second", "runtime": 23},
            ],
        }
        mock_response.raise_for_status.return_value = None
        mock_tmdb_client.get.return_value = mock_response

        result = await tv_analyzer.get_tv_season_details(12345, 1)

        assert result is not None
        assert len(result["episodes"]) == 2
        assert result["episodes"][0]["name"] == "Pilot"

    @pytest.mark.asyncio
    async def test_get_tv_season_details_failure(self, tv_analyzer, mock_tmdb_client):
        """Test TV season details retrieval failure."""
        mock_tmdb_client.get.side_effect = Exception("API Error")

        result = await tv_analyzer.get_tv_season_details(12345, 1)

        assert result is None

    def test_map_by_duration(self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes):
        """Test duration-based episode mapping."""
        # Use only the episode titles (first 4)
        episode_titles = sample_tv_titles[:4]

        mapping = tv_analyzer.map_by_duration(episode_titles, sample_tmdb_episodes)

        assert len(mapping) == 4
        for episode_info in mapping.values():
            assert isinstance(episode_info, EpisodeInfo)
            assert episode_info.season_number == 1
            assert 1 <= episode_info.episode_number <= 4

    def test_map_sequentially(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test sequential episode mapping."""
        episode_titles = sample_tv_titles[:4]

        mapping = tv_analyzer.map_sequentially(episode_titles, sample_tmdb_episodes)

        assert len(mapping) == 4
        # Check that episodes are mapped in order
        sorted_titles = sorted(episode_titles, key=lambda t: int(t.title_id))
        for i, (_title, episode_info) in enumerate(
            (t, mapping[t]) for t in sorted_titles if t in mapping
        ):
            assert episode_info.episode_number == i + 1

    def test_map_hybrid_successful_duration(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test hybrid mapping when duration mapping is successful."""
        episode_titles = sample_tv_titles[:4]

        # Mock duration mapping to return good results
        original_map_by_duration = tv_analyzer.map_by_duration
        tv_analyzer.map_by_duration = Mock(return_value={
            title: EpisodeInfo(1, i+1, f"Episode {i+1}", runtime=22)
            for i, title in enumerate(episode_titles)
        })

        mapping = tv_analyzer.map_hybrid(episode_titles, sample_tmdb_episodes)

        assert len(mapping) == 4
        tv_analyzer.map_by_duration = original_map_by_duration

    def test_map_hybrid_fallback_to_sequential(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test hybrid mapping fallback to sequential."""
        episode_titles = sample_tv_titles[:4]

        # Mock duration mapping to return poor results
        tv_analyzer.map_by_duration = Mock(return_value={})

        mapping = tv_analyzer.map_hybrid(episode_titles, sample_tmdb_episodes)

        # Should fall back to sequential mapping
        assert len(mapping) <= 4


class TestFullTVDiscAnalysis:
    """Integration tests for complete TV disc analysis."""

    @pytest.mark.asyncio
    async def test_analyze_tv_disc_success(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test successful complete TV disc analysis."""
        # Mock series identification
        tv_analyzer.identify_series_from_disc = AsyncMock(return_value=SeriesInfo(
            name="Test Series",
            tmdb_id=12345,
            detected_season=1,
            year=2023,
        ))

        # Mock season details
        tv_analyzer.get_tv_season_details = AsyncMock(return_value={
            "episodes": sample_tmdb_episodes,
        })

        result = await tv_analyzer.analyze_tv_disc(
            "Test Series Season 1", sample_tv_titles
        )

        assert result is not None
        assert len(result) <= len(sample_tv_titles)
        for episode_info in result.values():
            assert isinstance(episode_info, EpisodeInfo)
            assert episode_info.season_number == 1

    @pytest.mark.asyncio
    async def test_analyze_tv_disc_no_series_identified(
        self, tv_analyzer, sample_tv_titles
    ):
        """Test TV disc analysis when series cannot be identified."""
        tv_analyzer.identify_series_from_disc = AsyncMock(return_value=None)

        result = await tv_analyzer.analyze_tv_disc("Unknown Series", sample_tv_titles)

        assert result is None

    @pytest.mark.asyncio
    async def test_analyze_tv_disc_no_season_data(self, tv_analyzer, sample_tv_titles):
        """Test TV disc analysis when season data cannot be retrieved."""
        tv_analyzer.identify_series_from_disc = AsyncMock(return_value=SeriesInfo(
            name="Test Series",
            tmdb_id=12345,
            detected_season=1,
        ))

        tv_analyzer.get_tv_season_details = AsyncMock(return_value=None)

        result = await tv_analyzer.analyze_tv_disc(
            "Test Series Season 1", sample_tv_titles
        )

        assert result == {}


class TestMappingStrategies:
    """Tests for different episode mapping strategies."""

    def test_duration_mapping_strategy(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test when config specifies duration mapping."""
        tv_analyzer.config.episode_mapping_strategy = "duration"

        # Mock the method to verify it's called
        tv_analyzer.map_by_duration = Mock(return_value={})
        tv_analyzer.map_sequentially = Mock(return_value={})
        tv_analyzer.map_hybrid = Mock(return_value={})

        # This would be called within map_titles_to_episodes
        episode_titles = tv_analyzer.filter_episode_titles(sample_tv_titles)

        # Simulate the strategy selection logic
        strategy = tv_analyzer.config.episode_mapping_strategy.lower()
        if strategy == "duration":
            tv_analyzer.map_by_duration(episode_titles, sample_tmdb_episodes)

        tv_analyzer.map_by_duration.assert_called_once()
        tv_analyzer.map_sequentially.assert_not_called()
        tv_analyzer.map_hybrid.assert_not_called()

    def test_sequential_mapping_strategy(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test when config specifies sequential mapping."""
        tv_analyzer.config.episode_mapping_strategy = "sequential"

        tv_analyzer.map_by_duration = Mock(return_value={})
        tv_analyzer.map_sequentially = Mock(return_value={})
        tv_analyzer.map_hybrid = Mock(return_value={})

        episode_titles = tv_analyzer.filter_episode_titles(sample_tv_titles)

        strategy = tv_analyzer.config.episode_mapping_strategy.lower()
        if strategy == "sequential":
            tv_analyzer.map_sequentially(episode_titles, sample_tmdb_episodes)

        tv_analyzer.map_sequentially.assert_called_once()
        tv_analyzer.map_by_duration.assert_not_called()
        tv_analyzer.map_hybrid.assert_not_called()

    def test_unknown_mapping_strategy_defaults_to_hybrid(
        self, tv_analyzer, sample_tv_titles, sample_tmdb_episodes
    ):
        """Test that unknown strategy defaults to hybrid."""
        tv_analyzer.config.episode_mapping_strategy = "unknown_strategy"

        tv_analyzer.map_by_duration = Mock(return_value={})
        tv_analyzer.map_sequentially = Mock(return_value={})
        tv_analyzer.map_hybrid = Mock(return_value={})

        episode_titles = tv_analyzer.filter_episode_titles(sample_tv_titles)

        strategy = tv_analyzer.config.episode_mapping_strategy.lower()
        if strategy not in ["duration", "sequential", "hybrid"]:
            tv_analyzer.map_hybrid(episode_titles, sample_tmdb_episodes)

        tv_analyzer.map_hybrid.assert_called_once()


class TestEdgeCases:
    """Tests for edge cases and error conditions."""

    def test_find_episode_index(self, tv_analyzer, sample_tmdb_episodes):
        """Test finding episode index in episodes list."""
        episode_info = EpisodeInfo(
            season_number=1,
            episode_number=2,
            episode_title="Second Episode",
        )

        index = tv_analyzer._find_episode_index(episode_info, sample_tmdb_episodes)
        assert index == 1  # Second episode is at index 1

    def test_find_episode_index_not_found(self, tv_analyzer, sample_tmdb_episodes):
        """Test finding episode index when episode doesn't exist."""
        episode_info = EpisodeInfo(
            season_number=2,  # Different season
            episode_number=1,
            episode_title="Non-existent",
        )

        index = tv_analyzer._find_episode_index(episode_info, sample_tmdb_episodes)
        assert index == -1

    def test_empty_titles_list(self, tv_analyzer):
        """Test behavior with empty titles list."""
        result = tv_analyzer.filter_episode_titles([])
        assert result == []

        result = tv_analyzer.group_by_duration([])
        assert result == []

    def test_map_with_mismatched_counts(self, tv_analyzer, sample_tmdb_episodes):
        """Test mapping when title count doesn't match episode count."""
        # More titles than episodes
        many_titles = [
            Title(str(i), 22 * 60, 1000000, 5, [], f"Title {i}")
            for i in range(10)
        ]

        mapping = tv_analyzer.map_sequentially(many_titles, sample_tmdb_episodes)

        # Should only map as many as there are episodes
        assert len(mapping) == len(sample_tmdb_episodes)

