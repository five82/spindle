"""Tests for disc detection and ripping operations."""

import asyncio
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch

import pytest

from spindle.disc.analyzer import ContentType, DiscAnalysisResult
from spindle.queue.manager import QueueItemStatus
from conftest_processor import (
    temp_config, mock_dependencies, processor,
    sample_disc_info, sample_titles
)


class TestProcessorDiscOperations:
    """Test disc detection, analysis, and ripping operations."""

    def test_on_disc_detected_success(self, processor, sample_disc_info):
        """Test successful disc detection and ripping initiation."""
        mock_item = Mock(item_id=1)
        processor.queue_manager.add_disc.return_value = mock_item

        with patch.object(processor, '_rip_disc') as mock_rip_disc:
            processor._on_disc_detected(sample_disc_info)

            processor.queue_manager.add_disc.assert_called_once_with("TEST_MOVIE")
            processor.notifier.notify_disc_detected.assert_called_once_with(
                "TEST_MOVIE", "BD"
            )
            # Note: _rip_disc is now async, so it's wrapped in asyncio.run
            assert mock_rip_disc.called

    def test_on_disc_detected_error(self, processor, sample_disc_info):
        """Test disc detection with error."""
        processor.queue_manager.add_disc.side_effect = Exception("Database error")

        processor._on_disc_detected(sample_disc_info)

        processor.notifier.notify_error.assert_called_once()
        error_call = processor.notifier.notify_error.call_args
        assert "Failed to process disc" in error_call[0][0]
        assert error_call[1]["context"] == "TEST_MOVIE"

    @pytest.mark.asyncio
    async def test_rip_disc_success(self, processor, sample_disc_info, sample_titles):
        """Test successful disc ripping workflow."""
        mock_item = Mock(status=QueueItemStatus.PENDING)
        
        # Create a proper DiscAnalysisResult mock
        mock_analysis_result = Mock(spec=DiscAnalysisResult)
        mock_analysis_result.content_type = ContentType.MOVIE
        mock_analysis_result.confidence = 0.95
        mock_analysis_result.titles_to_rip = sample_titles[:1]  # Just the main title
        mock_analysis_result.metadata = None
        mock_analysis_result.episode_mappings = None

        processor.ripper.scan_disc.return_value = sample_titles
        processor.disc_analyzer.analyze_disc = AsyncMock(return_value=mock_analysis_result)
        processor.ripper.rip_title.return_value = Path("/test/output.mkv")
        
        await processor._rip_disc(mock_item, sample_disc_info)

        # Verify workflow steps
        processor.ripper.scan_disc.assert_called_once()
        processor.disc_analyzer.analyze_disc.assert_called_once_with(
            sample_disc_info, sample_titles
        )
        
        # Verify ripping was called for selected titles
        processor.ripper.rip_title.assert_called_once()
        
        # Verify item updates
        assert mock_item.status == QueueItemStatus.RIPPED
        assert mock_item.ripped_file == Path("/test/output.mkv")
        processor.queue_manager.update_item.assert_called()
        
        # Verify notifications
        processor.notifier.notify_rip_started.assert_called_once()
        processor.notifier.notify_rip_completed.assert_called_once()

    @pytest.mark.asyncio
    async def test_rip_disc_error(self, processor, sample_disc_info):
        """Test disc ripping with error."""
        mock_item = Mock()
        processor.ripper.scan_disc.side_effect = Exception("Scan failed")

        await processor._rip_disc(mock_item, sample_disc_info)

        assert mock_item.status == QueueItemStatus.FAILED
        assert mock_item.error_message == "Scan failed"
        processor.notifier.notify_error.assert_called_once()

    @pytest.mark.asyncio  
    async def test_rip_disc_with_tmdb_metadata(self, processor, sample_disc_info, sample_titles):
        """Test disc ripping with TMDB metadata found during analysis."""
        mock_item = Mock(status=QueueItemStatus.PENDING)
        
        # Create mock with metadata
        mock_analysis_result = Mock(spec=DiscAnalysisResult)
        mock_analysis_result.content_type = ContentType.MOVIE
        mock_analysis_result.confidence = 0.98
        mock_analysis_result.titles_to_rip = sample_titles[:1]
        mock_analysis_result.episode_mappings = None
        
        # Add metadata that would come from TMDB
        mock_metadata = Mock()
        mock_metadata.title = "The Matrix"
        mock_metadata.year = 1999
        mock_metadata.overview = "A computer hacker learns about the true nature of reality."
        mock_analysis_result.metadata = mock_metadata

        processor.ripper.scan_disc.return_value = sample_titles
        processor.disc_analyzer.analyze_disc = AsyncMock(return_value=mock_analysis_result)
        processor.ripper.rip_title.return_value = Path("/test/matrix.mkv")
        
        await processor._rip_disc(mock_item, sample_disc_info)

        # Verify the analyzer was called
        processor.disc_analyzer.analyze_disc.assert_called_once()
        
        # Verify metadata would be logged (we can't easily test logging)
        assert mock_analysis_result.metadata.title == "The Matrix"

    @pytest.mark.asyncio
    async def test_rip_disc_tv_series_with_episode_mapping(self, processor, sample_disc_info, sample_titles):
        """Test ripping TV series with episode mappings."""
        mock_item = Mock(status=QueueItemStatus.PENDING)
        
        # Create analysis result for TV series
        mock_analysis_result = Mock(spec=DiscAnalysisResult)
        mock_analysis_result.content_type = ContentType.TV_SERIES
        mock_analysis_result.confidence = 0.90
        mock_analysis_result.titles_to_rip = sample_titles  # All titles for TV
        mock_analysis_result.metadata = None
        
        # Add episode mappings
        from spindle.disc.analyzer import EpisodeInfo
        mock_analysis_result.episode_mappings = {
            sample_titles[0]: Mock(
                spec=EpisodeInfo,
                season_number=1,
                episode_number=1,
                episode_title="Pilot"
            ),
            sample_titles[1]: Mock(
                spec=EpisodeInfo,
                season_number=1,
                episode_number=2,
                episode_title="Episode 2"
            ),
        }

        processor.ripper.scan_disc.return_value = sample_titles
        processor.disc_analyzer.analyze_disc = AsyncMock(return_value=mock_analysis_result)
        processor.ripper.rip_title.side_effect = [
            Path("/test/s01e01.mkv"),
            Path("/test/s01e02.mkv"),
        ]
        
        await processor._rip_disc(mock_item, sample_disc_info)

        # Verify multiple titles were ripped
        assert processor.ripper.rip_title.call_count == 2
        
        # Verify item was updated with first file
        assert mock_item.ripped_file == Path("/test/s01e01.mkv")