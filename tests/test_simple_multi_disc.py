"""Tests for simple individual multi-disc processing with series caching."""

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
    """Test simple TV series disc detection."""
    
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


class TestSimpleSeriesCaching:
    """Test series metadata caching functionality."""
    
    def test_cache_new_series_metadata(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test caching new series metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        tv_info = manager.detect_tv_series_disc(sample_tv_disc_info, sample_tv_metadata)
        cached_media_info = manager.get_or_cache_series_metadata(tv_info, sample_media_info)
        
        # Should return the provided media info and cache it
        assert cached_media_info.title == sample_media_info.title
        assert cached_media_info.tmdb_id == sample_media_info.tmdb_id
        
        # Verify it was cached by trying to retrieve without providing media_info
        cached_again = manager.get_or_cache_series_metadata(tv_info, None)
        assert cached_again.title == sample_media_info.title
        assert cached_again.tmdb_id == sample_media_info.tmdb_id
        
    def test_retrieve_cached_series_metadata(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test retrieving cached series metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        # First disc - cache the metadata
        tv_info = manager.detect_tv_series_disc(sample_tv_disc_info, sample_tv_metadata)
        manager.get_or_cache_series_metadata(tv_info, sample_media_info)
        
        # Second disc from same series - should get cached metadata
        disc_info_2 = DiscInfo("/dev/sr0", "BluRay", "BATMAN_TV_S1_DISC_2")
        metadata_2 = EnhancedDiscMetadata()
        metadata_2.volume_id = "BATMAN_TV_S1_DISC_2"
        metadata_2.disc_name = "Batman TV Series - Season 1: Disc 2"
        metadata_2.disc_type_info = {"is_tv": True, "season": 1, "disc": 2}
        
        tv_info_2 = manager.detect_tv_series_disc(disc_info_2, metadata_2)
        cached_media_info = manager.get_or_cache_series_metadata(tv_info_2, None)
        
        assert cached_media_info.title == sample_media_info.title
        assert cached_media_info.tmdb_id == sample_media_info.tmdb_id


class TestSimpleDiscProcessing:
    """Test complete individual disc processing workflow."""
    
    def test_process_tv_series_disc_with_new_metadata(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test processing TV series disc with new metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        tv_info, media_info = manager.process_tv_series_disc(
            sample_tv_disc_info, sample_tv_metadata, sample_media_info
        )
        
        assert tv_info is not None
        assert tv_info.series_title == "Batman TV Series"
        assert tv_info.season_number == 1
        assert media_info.title == sample_media_info.title
        assert media_info.tmdb_id == sample_media_info.tmdb_id
        
    def test_process_tv_series_disc_with_cached_metadata(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test processing TV series disc that uses cached metadata."""
        manager = SimpleMultiDiscManager(test_config)
        
        # Process first disc to cache metadata
        manager.process_tv_series_disc(sample_tv_disc_info, sample_tv_metadata, sample_media_info)
        
        # Process second disc - should use cached metadata
        disc_info_2 = DiscInfo("/dev/sr0", "BluRay", "BATMAN_TV_S1_DISC_2")
        metadata_2 = EnhancedDiscMetadata()
        metadata_2.volume_id = "BATMAN_TV_S1_DISC_2"
        metadata_2.disc_name = "Batman TV Series - Season 1: Disc 2"
        metadata_2.disc_type_info = {"is_tv": True, "season": 1, "disc": 2}
        
        tv_info_2, media_info_2 = manager.process_tv_series_disc(
            disc_info_2, metadata_2, None  # No new metadata provided
        )
        
        assert tv_info_2 is not None
        assert tv_info_2.series_title == "Batman TV Series"
        assert tv_info_2.season_number == 1
        assert tv_info_2.disc_number == 2
        assert media_info_2.title == sample_media_info.title  # Should get cached metadata
        assert media_info_2.tmdb_id == sample_media_info.tmdb_id
        
    def test_process_non_tv_disc(self, test_config):
        """Test processing non-TV disc returns None."""
        manager = SimpleMultiDiscManager(test_config)
        
        disc_info = DiscInfo("/dev/sr0", "BluRay", "SINGLE_MOVIE")
        metadata = EnhancedDiscMetadata()
        metadata.volume_id = "SINGLE_MOVIE"
        metadata.disc_type_info = {"is_tv": False}
        
        tv_info, media_info = manager.process_tv_series_disc(disc_info, metadata, None)
        
        assert tv_info is None
        assert media_info is None


class TestSeriesTitleExtraction:
    """Test extraction of clean series titles."""
    
    def test_extract_series_title_from_disc_name(self, test_config):
        """Test extracting series title from disc name."""
        manager = SimpleMultiDiscManager(test_config)
        
        metadata = EnhancedDiscMetadata()
        metadata.disc_name = "Breaking Bad - Season 1: Disc 1"
        disc_info = DiscInfo("/dev/sr0", "BluRay", "BREAKING_BAD_S1_DISC_1")
        
        title = manager._extract_series_title(metadata, disc_info)
        assert title == "Breaking Bad"
        
    def test_extract_series_title_from_volume_id(self, test_config):
        """Test extracting series title from volume ID."""
        manager = SimpleMultiDiscManager(test_config)
        
        metadata = EnhancedDiscMetadata()
        metadata.volume_id = "SHERLOCK_S1_DISC_1"
        disc_info = DiscInfo("/dev/sr0", "BluRay", "SHERLOCK_S1_DISC_1")
        
        title = manager._extract_series_title(metadata, disc_info)
        assert title == "SHERLOCK"
        
    def test_clean_generic_labels(self, test_config):
        """Test filtering out generic labels."""
        manager = SimpleMultiDiscManager(test_config)
        
        # Generic labels should be detected
        assert manager._is_generic_label("LOGICAL_VOLUME_ID") is True
        assert manager._is_generic_label("DVD_VIDEO") is True
        assert manager._is_generic_label("123") is True
        assert manager._is_generic_label("AB") is True
        
        # Valid labels should pass
        assert manager._is_generic_label("BATMAN_TV_SERIES") is False
        assert manager._is_generic_label("BREAKING_BAD") is False


class TestCacheManagement:
    """Test cache management functionality."""
    
    def test_get_cache_stats(self, test_config, sample_tv_disc_info, sample_tv_metadata, sample_media_info):
        """Test getting cache statistics."""
        manager = SimpleMultiDiscManager(test_config)
        
        # Process a disc to create cache entry
        manager.process_tv_series_disc(sample_tv_disc_info, sample_tv_metadata, sample_media_info)
        
        stats = manager.get_cache_stats()
        assert isinstance(stats, dict)
        assert 'total_entries' in stats
        assert stats['total_entries'] >= 1
        
    def test_cleanup_expired_cache(self, test_config):
        """Test cleaning up expired cache entries."""
        manager = SimpleMultiDiscManager(test_config)
        
        # This should not error even with empty cache
        deleted_count = manager.cleanup_expired_cache()
        assert isinstance(deleted_count, int)
        assert deleted_count >= 0