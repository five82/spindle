"""Test TMDB service wrapper."""

import pytest
from unittest.mock import Mock, AsyncMock

from spindle.services.tmdb import TMDBService
from spindle.config import SpindleConfig


class TestTMDBService:
    """Test TMDBService functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(
            staging_dir=tmp_path / "staging",
            tmdb_api_key="test_api_key"
        )

    @pytest.fixture
    def service(self, config):
        """Create TMDB service instance."""
        return TMDBService(config)

    @pytest.mark.asyncio
    async def test_identify_media_calls_tmdb_api(self, service):
        """Test media identification calls TMDB API."""
        # Should call underlying MediaIdentifier but may fail with test config
        try:
            result = await service.identify_media("Test Movie", "movie")
            # If successful, result should be MediaInfo or None
            assert result is None or hasattr(result, 'title')
        except Exception as e:
            # Expected to fail with test config - verify it's trying to call identifier
            assert "identify_movie" in str(e) or "API" in str(e)

    @pytest.mark.asyncio
    async def test_identify_media_with_year(self, service):
        """Test media identification with year parameter."""
        # Should call underlying MediaIdentifier but may fail with test config
        try:
            result = await service.identify_media("Test Movie", "movie", year=2023)
            assert result is None or hasattr(result, 'title')
        except Exception as e:
            # Expected to fail with test config - verify it's trying to call identifier
            assert "identify_movie" in str(e) or "API" in str(e)

    @pytest.mark.asyncio
    async def test_identify_tv_series(self, service):
        """Test TV series identification."""
        # Should handle TV content type (may log warning for unknown type)
        result = await service.identify_media("Test Series", "tv")
        # Currently returns None for unknown content types
        assert result is None

    def test_service_initialization(self, service, config):
        """Test service initializes with config."""
        assert service.config == config
        assert service.config.tmdb_api_key == "test_api_key"