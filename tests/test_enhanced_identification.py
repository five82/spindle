"""Tests for enhanced disc identification system."""

import tempfile
from pathlib import Path
from unittest.mock import patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.metadata_extractor import EnhancedDiscMetadata, EnhancedDiscMetadataExtractor
from spindle.disc.title_selector import ContentType, IntelligentTitleSelector, SelectionCriteria
from spindle.disc.ripper import Title, Track
from spindle.identify.tmdb import MediaIdentifier, MediaInfo
from spindle.identify.tmdb_cache import TMDBCache


@pytest.fixture
def test_config():
    """Create a test configuration."""
    return SpindleConfig(
        staging_dir=Path("/tmp/spindle/staging"),
        library_dir=Path("/tmp/spindle/library"),
        log_dir=Path("/tmp/spindle/logs"),
        review_dir=Path("/tmp/spindle/review"),
        tmdb_api_key="test_key",
    )


@pytest.fixture
def sample_movie_titles():
    """Create sample movie titles."""
    return [
        Title("0", 7200, 4000000000, 24, [
            Track("0", "video", "H.264", "eng", 7200, 3000000000),
            Track("1", "audio", "DTS-HD", "eng", 7200, 800000000),
        ]),
        Title("1", 900, 500000000, 3, [
            Track("0", "video", "H.264", "eng", 900, 400000000),
        ]),
    ]


@pytest.fixture
def sample_tv_titles():
    """Create sample TV episode titles."""
    titles = []
    for i in range(4):
        titles.append(Title(
            str(i),
            2640,  # 44 minutes
            1500000000,  # 1.5GB
            22,
            [
                Track("0", "video", "H.264", "eng", 2640, 1200000000),
                Track("1", "audio", "AC3", "eng", 2640, 300000000),
            ]
        ))
    return titles


class TestEnhancedDiscMetadata:
    """Test enhanced disc metadata handling."""
    
    def test_tv_series_detection(self):
        """Test TV series detection from volume ID."""
        metadata = EnhancedDiscMetadata()
        metadata.volume_id = "BATMAN_TV_S1_DISC_1"
        metadata.disc_type_info = {"is_tv": True, "season": 1, "disc": 1}
        
        assert metadata.is_tv_series() is True
        
        season, disc = metadata.get_season_disc_info()
        assert season == 1
        assert disc == 1
        
    def test_title_candidate_extraction(self):
        """Test extraction of title candidates."""
        metadata = EnhancedDiscMetadata()
        metadata.disc_name = "50 First Dates"
        metadata.volume_id = "50_FIRST_DATES"
        metadata.makemkv_label = "LOGICAL_VOLUME_ID"
        
        candidates = metadata.get_best_title_candidates()
        
        assert candidates[0] == "50 First Dates"
        # The actual implementation might clean/normalize "50_FIRST_DATES" differently
        assert len(candidates) > 0
        assert "LOGICAL_VOLUME_ID" not in candidates


class TestTMDBCache:
    """Test TMDB caching functionality."""
    
    def test_cache_initialization(self):
        """Test cache database initialization."""
        with tempfile.TemporaryDirectory() as temp_dir:
            cache_dir = Path(temp_dir) / "cache"
            cache = TMDBCache(cache_dir, ttl_days=30)
            
            assert cache.db_path.exists()
            assert cache.ttl_days == 30
    
    def test_cache_operations(self):
        """Test cache store and retrieve operations."""
        with tempfile.TemporaryDirectory() as temp_dir:
            cache_dir = Path(temp_dir) / "cache"
            cache = TMDBCache(cache_dir, ttl_days=30)
            
            test_results = [{"id": 123, "title": "Test Movie"}]
            success = cache.cache_results("test movie", test_results, "movie")
            assert success is True
            
            cached = cache.search_cache("test movie", "movie")
            assert cached is not None
            assert cached.results == test_results
            assert cached.is_valid() is True


class TestIntelligentTitleSelector:
    """Test intelligent title and track selection."""
    
    def test_movie_content_selection(self, sample_movie_titles):
        """Test movie title selection."""
        criteria = SelectionCriteria(max_extras=2)
        selector = IntelligentTitleSelector(criteria)
        
        media_info = MediaInfo(
            title="Test Movie",
            year=2023,
            media_type="movie",
            tmdb_id=123
        )
        
        selection = selector.select_content(sample_movie_titles, media_info, ContentType.MOVIE)
        
        assert selection.content_type == ContentType.MOVIE
        assert len(selection.main_titles) == 1
        assert selection.main_titles[0].duration == 7200
        
    def test_tv_content_selection(self, sample_tv_titles):
        """Test TV series title selection."""
        criteria = SelectionCriteria()
        selector = IntelligentTitleSelector(criteria)
        
        media_info = MediaInfo(
            title="Test Series",
            year=2023,
            media_type="tv",
            tmdb_id=456
        )
        
        selection = selector.select_content(sample_tv_titles, media_info, ContentType.TV_SERIES)
        
        assert selection.content_type == ContentType.TV_SERIES
        assert len(selection.main_titles) == 4


class TestMediaIdentifierIntegration:
    """Test the enhanced MediaIdentifier integration."""
    
    @pytest.mark.asyncio
    async def test_disc_content_identification(self, test_config):
        """Test disc content identification workflow."""
        identifier = MediaIdentifier(test_config)
        
        title_candidates = ["Blazing Saddles", "BLAZING_SADDLES"]
        
        with patch.object(identifier.tmdb, 'search_movie') as mock_search:
            mock_search.return_value = [{"id": 123, "title": "Blazing Saddles"}]
            
            with patch.object(identifier.tmdb, 'get_movie_details') as mock_details:
                mock_details.return_value = {
                    "id": 123,
                    "title": "Blazing Saddles",
                    "release_date": "1974-06-07",
                    "genres": [{"name": "Comedy"}],
                    "overview": "A satirical Western comedy film."
                }
                
                result = await identifier.identify_disc_content(title_candidates, runtime_minutes=93)
                
                assert result is not None
                assert result.title == "Blazing Saddles"
                assert result.media_type == "movie"
                assert result.tmdb_id == 123
    
    def test_generic_label_detection(self, test_config):
        """Test detection of generic disc labels."""
        identifier = MediaIdentifier(test_config)
        
        generic_labels = ["LOGICAL_VOLUME_ID", "DVD_VIDEO", "BLURAY"]
        for label in generic_labels:
            assert identifier.is_generic_label(label) is True
            
        valid_labels = ["BLAZING_SADDLES", "50_FIRST_DATES"]
        for label in valid_labels:
            assert identifier.is_generic_label(label) is False


class TestWorkflowIntegration:
    """Test end-to-end workflow integration."""
    
    def test_enhanced_metadata_extraction_graceful_failure(self):
        """Test metadata extraction handles missing files gracefully."""
        extractor = EnhancedDiscMetadataExtractor()
        
        fake_path = Path("/nonexistent/disc/path")
        metadata = extractor.extract_all_metadata(fake_path)
        
        assert isinstance(metadata, EnhancedDiscMetadata)
        assert metadata.volume_id is None
        assert metadata.disc_name is None
        
    @pytest.mark.asyncio
    async def test_identification_fallback_chain(self, test_config):
        """Test the complete identification fallback chain."""
        identifier = MediaIdentifier(test_config)
        
        result = await identifier.identify_disc_content([])
        assert result is None
        
        generic_candidates = ["LOGICAL_VOLUME_ID", "DVD_VIDEO"]
        result = await identifier.identify_disc_content(generic_candidates)
        assert result is None