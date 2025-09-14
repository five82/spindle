"""Test disc processing coordination."""

import json
import pytest
from pathlib import Path
from unittest.mock import Mock, AsyncMock, patch

from spindle.components.disc_handler import DiscHandler
from spindle.storage.queue import QueueItem, QueueItemStatus
from spindle.config import SpindleConfig


class TestDiscHandler:
    """Test DiscHandler functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(staging_dir=tmp_path / "staging")

    @pytest.fixture
    def handler(self, config):
        """Create disc handler instance."""
        with patch('spindle.components.disc_handler.IntelligentDiscAnalyzer') as mock_analyzer, \
             patch('spindle.components.disc_handler.TVSeriesDiscAnalyzer') as mock_tv, \
             patch('spindle.components.disc_handler.SimpleMultiDiscManager') as mock_multi, \
             patch('spindle.components.disc_handler.MakeMKVService') as mock_ripper, \
             patch('spindle.components.disc_handler.TMDBService') as mock_tmdb:

            handler = DiscHandler(config)
            handler.queue_manager = Mock()

            # Configure the mock instances
            handler.disc_analyzer.analyze_disc = AsyncMock()
            handler.tv_analyzer.analyze_tv_disc = AsyncMock()
            handler.multi_disc_manager.detect_multi_disc_series = AsyncMock()
            handler.ripper.rip_disc = AsyncMock()
            handler.tmdb_service.identify_media = AsyncMock()

            return handler

    @pytest.mark.asyncio
    async def test_identify_disc_calls_components(self, handler):
        """Test disc identification calls required components."""
        item = QueueItem(item_id=1, disc_title="Test Movie")
        disc_info = Mock(device="/dev/sr0", label="MOVIE_DISC")

        # Mock analysis to return None (analysis failed)
        handler.disc_analyzer.analyze_disc.return_value = None

        # This should cause the function to raise an exception
        with pytest.raises(Exception):
            await handler.identify_disc(item, disc_info)

        # Verify the analyzer was called
        handler.disc_analyzer.analyze_disc.assert_called_once_with("/dev/sr0")

        # Verify item status was set to failed
        assert item.status == QueueItemStatus.FAILED

    @pytest.mark.asyncio
    async def test_identify_disc_failure(self, handler):
        """Test disc identification failure handling."""
        item = QueueItem(item_id=1, disc_title="Test Movie")
        disc_info = Mock(device="/dev/sr0")

        handler.disc_analyzer.analyze_disc.side_effect = Exception("Analysis failed")

        with pytest.raises(Exception):
            await handler.identify_disc(item, disc_info)

        assert item.status == QueueItemStatus.FAILED
        assert "Analysis failed" in item.error_message

    @pytest.mark.asyncio
    async def test_rip_calls_ripper(self, handler):
        """Test rip calls MakeMKV ripper."""
        # Create simple valid JSON for rip spec
        rip_spec_data = {
            "disc_info": {"device": "/dev/sr0", "label": "TEST"},
            "analysis_result": {"titles_to_rip": [{"index": 1}], "episode_mappings": {}},
            "media_info": {"title": "Test Movie"},
        }
        item = QueueItem(
            item_id=1,
            disc_title="Test Movie",
            rip_spec_data=json.dumps(rip_spec_data)
        )
        item.media_info = Mock()

        mock_files = [Path("/tmp/test.mkv")]
        handler.ripper.rip_disc.return_value = mock_files

        with patch('spindle.components.disc_handler.eject_disc') as mock_eject:
            await handler.rip_identified_item(item)

        # Verify ripper was called
        handler.ripper.rip_disc.assert_called_once()
        mock_eject.assert_called_once_with("/dev/sr0")

        # Verify final status
        assert item.status == QueueItemStatus.RIPPED
        assert item.ripped_file == mock_files[0]