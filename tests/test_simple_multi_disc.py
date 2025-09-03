"""Tests for simple multi-disc processing with series caching."""

from pathlib import Path
from unittest.mock import MagicMock

import pytest

from spindle.config import SpindleConfig
from spindle.disc.metadata_extractor import EnhancedDiscMetadata
from spindle.disc.monitor import DiscInfo
from spindle.disc.multi_disc import SimpleMultiDiscManager, TVSeriesDiscInfo
from spindle.identify.tmdb import MediaInfo


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
def sample_tv_disc_info():
    """Create sample TV series disc info."""
    return DiscInfo(
        device="/dev/sr0",
        disc_type="BluRay",
        label="BATMAN_TV_S1_DISC_1"
    )


@pytest.fixture
def sample_tv_metadata():
    """Create sample TV series metadata."""
    metadata = EnhancedDiscMetadata()
    metadata.volume_id = "BATMAN_TV_S1_DISC_1" 
    metadata.disc_name = "Batman TV Series - Season 1: Disc 1"
    metadata.disc_type_info = {"is_tv": True, "season": 1, "disc": 1}
    return metadata


@pytest.fixture
def sample_media_info():
    """Create sample TMDB media info."""
    return MediaInfo(
        title="Batman TV Series",
        year=2004,
        media_type="tv",
        tmdb_id=12345
    )


class TestSimpleMultiDiscDetection:
    """Test TV series disc detection."""
    
    def test_detect_tv_series_disc(self, test_config, sample_tv_disc_info, sample_tv_metadata):
        """Test detection of TV series disc."""
        manager = SimpleMultiDiscManager(test_config)
        
        tv_info = manager.detect_tv_series_disc(sample_tv_disc_info, sample_tv_metadata)
        
        assert tv_info is not None
        assert tv_info.series_title == "Batman TV Series"
        assert tv_info.season_number == 1
        assert tv_info.disc_number == 1
        assert tv_info.is_tv_series is True
        
    def test_detect_non_tv_disc(self, test_config):
        """Test that non-TV discs are not detected."""
        manager = SimpleMultiDiscManager(test_config)
        
        disc_info = DiscInfo("/dev/sr0", "BluRay", "SINGLE_MOVIE")
        metadata = EnhancedDiscMetadata()
        metadata.volume_id = "SINGLE_MOVIE"
        metadata.disc_type_info = {"is_tv": False}
        
        tv_info = manager.detect_tv_series_disc(disc_info, metadata)
        
        assert tv_info is None


class TestSeriesCaching:
    """Test series metadata caching functionality."""
    
    def test_cache_new_series_metadata(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test caching new series metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        tv_info = manager.detect_tv_series_disc(sample_tv_disc_info, sample_tv_metadata)
        cached_media_info = manager.get_or_cache_series_metadata(tv_info, sample_media_info)
        
        assert cached_media_info.title == sample_media_info.title
        assert cached_media_info.tmdb_id == sample_media_info.tmdb_id
        
        cached_again = manager.get_or_cache_series_metadata(tv_info, None)
        assert cached_again.title == sample_media_info.title
        assert cached_again.tmdb_id == sample_media_info.tmdb_id
        
    def test_retrieve_cached_series_metadata(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test retrieving cached series metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        tv_info = manager.detect_tv_series_disc(sample_tv_disc_info, sample_tv_metadata)
        manager.get_or_cache_series_metadata(tv_info, sample_media_info)
        
        disc_info_2 = DiscInfo("/dev/sr0", "BluRay", "BATMAN_TV_S1_DISC_2")
        metadata_2 = EnhancedDiscMetadata()
        metadata_2.volume_id = "BATMAN_TV_S1_DISC_2"
        metadata_2.disc_name = "Batman TV Series - Season 1: Disc 2"
        metadata_2.disc_type_info = {"is_tv": True, "season": 1, "disc": 2}
        
        tv_info_2 = manager.detect_tv_series_disc(disc_info_2, metadata_2)
        cached_metadata = manager.get_or_cache_series_metadata(tv_info_2, None)
        
        assert cached_metadata.title == sample_media_info.title
        assert cached_metadata.tmdb_id == sample_media_info.tmdb_id

    def test_different_series_separate_cache(self, test_config):
        """Test that different series maintain separate cache entries."""
        manager = SimpleMultiDiscManager(test_config)
        
        # Cache first series
        disc_info_1 = DiscInfo("/dev/sr0", "BluRay", "BATMAN_TV_S1_DISC_1")
        metadata_1 = EnhancedDiscMetadata()
        metadata_1.volume_id = "BATMAN_TV_S1_DISC_1"
        metadata_1.disc_type_info = {"is_tv": True, "season": 1, "disc": 1}
        
        tv_info_1 = manager.detect_tv_series_disc(disc_info_1, metadata_1)
        media_info_1 = MediaInfo("Batman TV Series", 2004, "tv", 12345)
        manager.get_or_cache_series_metadata(tv_info_1, media_info_1)
        
        # Different series should not get cached data from first
        disc_info_2 = DiscInfo("/dev/sr0", "BluRay", "SUPERMAN_TV_S1_DISC_1")  
        metadata_2 = EnhancedDiscMetadata()
        metadata_2.volume_id = "SUPERMAN_TV_S1_DISC_1"
        metadata_2.disc_type_info = {"is_tv": True, "season": 1, "disc": 1}
        
        tv_info_2 = manager.detect_tv_series_disc(disc_info_2, metadata_2)
        cached_metadata = manager.get_or_cache_series_metadata(tv_info_2, None)
        
        assert cached_metadata is None


class TestWorkflowIntegration:
    """Test multi-disc workflow integration."""
    
    def test_multi_disc_workflow_detection(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test complete multi-disc workflow detection."""
        manager = SimpleMultiDiscManager(test_config)
        
        tv_info = manager.detect_tv_series_disc(sample_tv_disc_info, sample_tv_metadata)
        
        assert tv_info is not None
        assert tv_info.series_title == "Batman TV Series"
        assert tv_info.is_tv_series is True

    def test_single_disc_workflow(self, test_config):
        """Test workflow with single disc (non-multi-disc)."""
        manager = SimpleMultiDiscManager(test_config)
        
        single_disc_info = DiscInfo("/dev/sr0", "BluRay", "SINGLE_MOVIE_DISC")
        single_metadata = EnhancedDiscMetadata()
        single_metadata.volume_id = "SINGLE_MOVIE_DISC"
        single_metadata.disc_type_info = {"is_tv": False}
        
        tv_info = manager.detect_tv_series_disc(single_disc_info, single_metadata)
        
        assert tv_info is None

    def test_error_handling_invalid_metadata(self, test_config):
        """Test error handling with invalid metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        disc_info = DiscInfo("/dev/sr0", "BluRay", "INVALID_DISC")
        invalid_metadata = EnhancedDiscMetadata()
        # Missing disc_type_info should be handled gracefully
        
        tv_info = manager.detect_tv_series_disc(disc_info, invalid_metadata)
        
        assert tv_info is None