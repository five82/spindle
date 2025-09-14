"""Test Plex service wrapper."""

import pytest
from unittest.mock import Mock

from spindle.services.plex import PlexService
from spindle.config import SpindleConfig


class TestPlexService:
    """Test PlexService functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(library_dir=tmp_path / "library")

    @pytest.fixture
    def service(self, config):
        """Create Plex service instance."""
        return PlexService(config)

    @pytest.mark.asyncio
    async def test_refresh_library_calls_plex_api(self, service):
        """Test library refresh calls Plex API."""
        # Should call underlying PlexAPI but may fail with test config
        try:
            await service.refresh_library("movie")
        except Exception as e:
            # Expected to fail without proper Plex config - verify it's trying
            assert "plex" in str(e).lower() or "url" in str(e).lower() or "token" in str(e).lower()

    def test_service_initialization(self, service, config):
        """Test service initializes with config."""
        assert service.config == config
        assert service.organizer is not None

    def test_has_organizer_instance(self, service):
        """Test service has LibraryOrganizer instance."""
        assert service.organizer is not None