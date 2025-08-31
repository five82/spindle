"""Essential media identification tests - TMDB integration and metadata."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaInfo, MediaIdentifier


@pytest.fixture
def temp_config():
    """Create temporary config for testing."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
            tmdb_api_key="test_api_key_12345",
        )


@pytest.fixture
def sample_movie_response():
    """Sample TMDB movie API response."""
    return {
        "id": 12345,
        "title": "Test Movie",
        "release_date": "2023-05-15",
        "overview": "A test movie for validation",
        "genres": [
            {"id": 28, "name": "Action"},
            {"id": 12, "name": "Adventure"}
        ],
        "runtime": 120,
        "poster_path": "/poster.jpg",
        "backdrop_path": "/backdrop.jpg",
        "vote_average": 7.5
    }


@pytest.fixture
def sample_tv_response():
    """Sample TMDB TV show API response."""
    return {
        "id": 67890,
        "name": "Test TV Show",
        "first_air_date": "2023-01-10",
        "overview": "A test TV show for validation",
        "genres": [
            {"id": 18, "name": "Drama"},
            {"id": 80, "name": "Crime"}
        ],
        "number_of_seasons": 3,
        "poster_path": "/tv_poster.jpg",
        "backdrop_path": "/tv_backdrop.jpg",
        "vote_average": 8.2
    }


class TestMediaInfo:
    """Test media information data structure."""
    
    def test_movie_media_info_creation(self):
        """Test creating MediaInfo for movies."""
        media_info = MediaInfo(
            title="Test Movie",
            year=2023,
            media_type="movie",
            tmdb_id=12345,
            overview="A test movie",
            genres=["Action", "Adventure"]
        )
        
        assert media_info.title == "Test Movie"
        assert media_info.year == 2023
        assert media_info.media_type == "movie"
        assert media_info.tmdb_id == 12345
        assert "Action" in media_info.genres

    def test_tv_media_info_creation(self):
        """Test creating MediaInfo for TV shows."""
        media_info = MediaInfo(
            title="Test TV Show",
            year=2023,
            media_type="tv",
            tmdb_id=67890,
            overview="A test TV show",
            genres=["Drama"],
            seasons=3
        )
        
        assert media_info.title == "Test TV Show"
        assert media_info.media_type == "tv"
        assert media_info.seasons == 3

    def test_media_info_validation(self):
        """Test MediaInfo field validation."""
        # Required fields
        media_info = MediaInfo(
            title="Required Fields Only",
            year=2023,
            media_type="movie",
            tmdb_id=99999
        )
        
        assert media_info.title == "Required Fields Only"
        assert media_info.overview == ""  # Default empty string
        assert media_info.genres == []  # Default empty list


class TestMediaIdentifier:
    """Test TMDB API integration."""
    
    def test_identifier_initialization(self, temp_config):
        """Test TMDB identifier initializes with API key."""
        identifier = MediaIdentifier(temp_config)
        
        assert identifier.tmdb.api_key == "test_api_key_12345"
        assert identifier.tmdb.base_url == "https://api.themoviedb.org/3"

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_search_movie_success(self, mock_get, temp_config, sample_movie_response):
        """Test successful movie search."""
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.json.return_value = {
            "results": [sample_movie_response]
        }
        mock_response.raise_for_status.return_value = None
        mock_get.return_value = mock_response
        
        identifier = MediaIdentifier(temp_config)
        results = await identifier.tmdb.search_movie("Test Movie")
        
        assert len(results) == 1
        assert results[0]["title"] == "Test Movie"
        assert results[0]["id"] == 12345

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_search_tv_success(self, mock_get, temp_config, sample_tv_response):
        """Test successful TV show search."""
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.json.return_value = {
            "results": [sample_tv_response]
        }
        mock_response.raise_for_status.return_value = None
        mock_get.return_value = mock_response
        
        identifier = MediaIdentifier(temp_config)
        results = await identifier.tmdb.search_tv("Test TV Show")
        
        assert len(results) == 1
        assert results[0]["name"] == "Test TV Show"
        assert results[0]["id"] == 67890

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_search_failure(self, mock_get, temp_config):
        """Test API search failure handling."""
        import httpx
        mock_response = Mock()
        mock_response.status_code = 401
        mock_response.text = "Invalid API key"
        mock_get.side_effect = httpx.HTTPStatusError("Invalid API key", request=Mock(), response=mock_response)
        
        identifier = MediaIdentifier(temp_config)
        results = await identifier.tmdb.search_movie("Test Movie")
        
        assert results == []

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_get_movie_details(self, mock_get, temp_config, sample_movie_response):
        """Test fetching detailed movie information."""
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.json.return_value = sample_movie_response
        mock_response.raise_for_status.return_value = None
        mock_get.return_value = mock_response
        
        identifier = MediaIdentifier(temp_config)
        movie_details = await identifier.tmdb.get_movie_details(12345)
        
        assert movie_details["title"] == "Test Movie"
        assert movie_details["runtime"] == 120
        assert len(movie_details["genres"]) == 2

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_get_tv_details(self, mock_get, temp_config, sample_tv_response):
        """Test fetching detailed TV show information."""
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.json.return_value = sample_tv_response
        mock_response.raise_for_status.return_value = None
        mock_get.return_value = mock_response
        
        identifier = MediaIdentifier(temp_config)
        tv_details = await identifier.tmdb.get_tv_details(67890)
        
        assert tv_details["name"] == "Test TV Show"
        assert tv_details["number_of_seasons"] == 3
        assert len(tv_details["genres"]) == 2


class TestIdentificationWorkflow:
    """Test complete identification workflow."""
    
    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_identify_movie_workflow(self, mock_get, temp_config, sample_movie_response):
        """Test complete movie identification workflow."""
        # Mock search response
        mock_search_response = Mock()
        mock_search_response.status_code = 200
        mock_search_response.json.return_value = {
            "results": [sample_movie_response]
        }
        mock_search_response.raise_for_status.return_value = None
        
        # Mock details response
        mock_details_response = Mock()
        mock_details_response.status_code = 200
        mock_details_response.json.return_value = sample_movie_response
        mock_details_response.raise_for_status.return_value = None
        
        mock_get.side_effect = [mock_search_response, mock_details_response]
        
        identifier = MediaIdentifier(temp_config)
        media_info = await identifier.identify_media("/path/Test Movie (2023).mkv")
        
        assert media_info is not None
        assert media_info.title == "Test Movie"
        assert media_info.year == 2023
        assert media_info.media_type == "movie"
        assert media_info.tmdb_id == 12345
        assert "Action" in media_info.genres

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_identify_tv_workflow(self, mock_get, temp_config, sample_tv_response):
        """Test complete TV show identification workflow."""
        # Mock search response
        mock_search_response = Mock()
        mock_search_response.status_code = 200
        mock_search_response.json.return_value = {
            "results": [sample_tv_response]
        }
        mock_search_response.raise_for_status.return_value = None
        
        # Mock details response
        mock_details_response = Mock()
        mock_details_response.status_code = 200
        mock_details_response.json.return_value = sample_tv_response
        mock_details_response.raise_for_status.return_value = None
        
        # Mock episode details response
        mock_episode_response = Mock()
        mock_episode_response.status_code = 200
        mock_episode_response.json.return_value = {
            "name": "Test Episode",
            "episode_number": 1,
            "season_number": 1,
            "overview": "A test episode"
        }
        mock_episode_response.raise_for_status.return_value = None
        
        mock_get.side_effect = [mock_search_response, mock_details_response, mock_episode_response]
        
        identifier = MediaIdentifier(temp_config)
        media_info = await identifier.identify_media("/path/Test TV Show S01E01.mkv")
        
        assert media_info is not None
        assert media_info.title == "Test TV Show"
        assert media_info.year == 2023
        assert media_info.media_type == "tv"
        assert media_info.seasons == 3

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_identify_no_results(self, mock_get, temp_config):
        """Test identification with no search results."""
        mock_response = Mock()
        mock_response.status_code = 200
        mock_response.json.return_value = {"results": []}
        mock_response.raise_for_status.return_value = None
        mock_get.return_value = mock_response
        
        identifier = MediaIdentifier(temp_config)
        media_info = await identifier.identify_media("/path/Unknown Movie (2023).mkv")
        
        assert media_info is None

    def test_title_cleaning(self, temp_config):
        """Test title cleaning and normalization."""
        identifier = MediaIdentifier(temp_config)
        
        # Test various title formats
        cleaned = identifier.clean_title("The.Movie.Title.2023.BluRay.x264")
        assert "The Movie Title" in cleaned
        assert "2023" in cleaned  # clean_title doesn't remove years
        assert "BluRay" not in cleaned  # Should remove disc indicators
        
        cleaned = identifier.clean_title("TV_SHOW_S01E01_HDTV")
        assert "TV SHOW" in cleaned
        assert "S01E01" in cleaned  # clean_title doesn't remove season/episode info


class TestMetadataProcessing:
    """Test metadata processing and transformation."""
    
    def test_genre_extraction(self, sample_movie_response):
        """Test genre list extraction from API response."""
        from spindle.identify.tmdb import extract_genres
        
        genres = extract_genres(sample_movie_response["genres"])
        
        assert "Action" in genres
        assert "Adventure" in genres
        assert len(genres) == 2

    def test_year_extraction(self):
        """Test year extraction from release dates."""
        from spindle.identify.tmdb import extract_year
        
        # Movie release date
        year = extract_year("2023-05-15")
        assert year == 2023
        
        # TV first air date
        year = extract_year("2023-01-10")
        assert year == 2023
        
        # Invalid date
        year = extract_year("")
        assert year is None

    def test_poster_url_construction(self, temp_config):
        """Test poster URL construction."""
        identifier = MediaIdentifier(temp_config)
        
        poster_url = identifier.build_poster_url("/poster.jpg")
        assert poster_url.startswith("https://image.tmdb.org/t/p/")
        assert poster_url.endswith("/poster.jpg")
        
        # Handle None poster path
        poster_url = identifier.build_poster_url(None)
        assert poster_url is None


class TestQueueIntegration:
    """Test identification integration with queue system."""
    
    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_identification_updates_queue(self, mock_get, temp_config, sample_movie_response):
        """Test identification updates queue item."""
        from spindle.queue.manager import QueueManager, QueueItemStatus
        
        # Setup queue with ripped item
        queue_manager = QueueManager(temp_config)
        item = queue_manager.add_disc("TEST_MOVIE_DISC")
        item.status = QueueItemStatus.RIPPED
        item.ripped_file = Path("/staging/Test Movie (2023).mkv")
        queue_manager.update_item(item)
        
        # Mock successful identification
        mock_search_response = Mock()
        mock_search_response.status_code = 200
        mock_search_response.json.return_value = {
            "results": [sample_movie_response]
        }
        mock_search_response.raise_for_status.return_value = None
        
        mock_details_response = Mock()
        mock_details_response.status_code = 200
        mock_details_response.json.return_value = sample_movie_response
        mock_details_response.raise_for_status.return_value = None
        
        mock_get.side_effect = [mock_search_response, mock_details_response]
        
        identifier = MediaIdentifier(temp_config)
        media_info = await identifier.identify_media(str(item.ripped_file))
        
        # Update queue item with identification
        item.media_info = media_info
        item.status = QueueItemStatus.IDENTIFIED
        queue_manager.update_item(item)
        
        # Verify queue item updated
        updated_item = queue_manager.get_item(item.item_id)
        assert updated_item.status == QueueItemStatus.IDENTIFIED
        assert updated_item.media_info.title == "Test Movie"
        assert updated_item.media_info.tmdb_id == 12345

    @pytest.mark.asyncio
    @patch('httpx.Client.get')
    async def test_identification_error_handling(self, mock_get, temp_config):
        """Test handling of identification errors."""
        from spindle.queue.manager import QueueManager, QueueItemStatus
        
        queue_manager = QueueManager(temp_config)
        item = queue_manager.add_disc("UNKNOWN_DISC")
        item.ripped_file = Path("/staging/Unknown Movie (2023).mkv")
        
        # Simulate identification failure
        identifier = MediaIdentifier(temp_config)
        
        # Mock failed API call
        import httpx
        mock_get.side_effect = httpx.RequestError("Network error")
        
        media_info = await identifier.identify_media(str(item.ripped_file))
        assert media_info is None
        
        # Queue item should handle missing identification gracefully
        item.media_info = media_info  # None
        queue_manager.update_item(item)
        
        retrieved = queue_manager.get_item(item.item_id)
        assert retrieved.media_info is None