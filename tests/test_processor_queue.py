"""Tests for queue processing operations."""

import asyncio
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch

import pytest

from spindle.encode.drapto_wrapper import EncodeResult
from spindle.identify.tmdb import MediaInfo
from spindle.queue.manager import QueueItem, QueueItemStatus
from conftest_processor import temp_config, mock_dependencies, processor


@pytest.fixture
def sample_media_info():
    """Create sample media info for testing."""
    return MediaInfo(
        title="Test Movie",
        year=2023,
        media_type="movie",
        tmdb_id=12345,
        overview="A test movie",
        genres=["Action", "Drama"],
    )


class TestProcessorQueueOperations:
    """Test queue processing and item workflow."""

    @pytest.mark.asyncio
    async def test_process_queue_continuously(self, processor):
        """Test continuous queue processing loop."""
        # Mock a sequence of items to process
        mock_items = [
            Mock(status=QueueItemStatus.RIPPED),
            Mock(status=QueueItemStatus.IDENTIFIED),
            None,  # No more items
        ]
        
        call_count = 0
        def get_next_item():
            nonlocal call_count
            if call_count < len(mock_items):
                item = mock_items[call_count]
                call_count += 1
                return item
            else:
                processor.is_running = False  # Stop the loop
                return None

        processor._get_next_processable_item = Mock(side_effect=get_next_item)
        processor._process_single_item = AsyncMock()
        processor.is_running = True

        await processor._process_queue_continuously()

        # Should have processed 2 items
        assert processor._process_single_item.call_count == 2
        processor._process_single_item.assert_any_call(mock_items[0])
        processor._process_single_item.assert_any_call(mock_items[1])

    @pytest.mark.asyncio 
    async def test_process_queue_with_exception(self, processor):
        """Test queue processing handles exceptions gracefully."""
        processor._get_next_processable_item = Mock(side_effect=Exception("Test error"))
        processor.is_running = True
        
        # Mock sleep to avoid actual delays
        with patch('asyncio.sleep', new_callable=AsyncMock) as mock_sleep:
            # Let it run one iteration then stop
            def stop_after_sleep(*args):
                processor.is_running = False
                
            mock_sleep.side_effect = stop_after_sleep
            
            await processor._process_queue_continuously()
            
            mock_sleep.assert_called_once_with(processor.config.error_retry_interval)

    def test_get_next_processable_item(self, processor):
        """Test getting next processable item from queue."""
        # Mock queue manager to return items for each status
        ripped_item = Mock(status=QueueItemStatus.RIPPED)
        identified_item = Mock(status=QueueItemStatus.IDENTIFIED)
        encoded_item = Mock(status=QueueItemStatus.ENCODED)
        
        def mock_get_by_status(status):
            if status == QueueItemStatus.RIPPED:
                return [ripped_item]
            elif status == QueueItemStatus.IDENTIFIED:
                return [identified_item] 
            elif status == QueueItemStatus.ENCODED:
                return [encoded_item]
            else:
                return []

        processor.queue_manager.get_items_by_status.side_effect = mock_get_by_status

        # Should return ripped item first (highest priority)
        item = processor._get_next_processable_item()
        assert item == ripped_item

    def test_get_next_processable_item_none(self, processor):
        """Test getting next item when queue is empty."""
        processor.queue_manager.get_items_by_status.return_value = []

        item = processor._get_next_processable_item()
        assert item is None

    @pytest.mark.asyncio
    async def test_process_single_item_ripped(self, processor):
        """Test processing a ripped item (identification stage)."""
        mock_item = Mock(status=QueueItemStatus.RIPPED)
        
        with patch.object(processor, '_identify_item', new_callable=AsyncMock) as mock_identify:
            await processor._process_single_item(mock_item)
            mock_identify.assert_called_once_with(mock_item)

    @pytest.mark.asyncio
    async def test_process_single_item_identified(self, processor):
        """Test processing an identified item (encoding stage)."""
        mock_item = Mock(status=QueueItemStatus.IDENTIFIED)
        
        with patch.object(processor, '_encode_item', new_callable=AsyncMock) as mock_encode:
            await processor._process_single_item(mock_item)
            mock_encode.assert_called_once_with(mock_item)

    @pytest.mark.asyncio
    async def test_process_single_item_encoded(self, processor):
        """Test processing an encoded item (organization stage)."""
        mock_item = Mock(status=QueueItemStatus.ENCODED)
        
        with patch.object(processor, '_organize_item', new_callable=AsyncMock) as mock_organize:
            await processor._process_single_item(mock_item)
            mock_organize.assert_called_once_with(mock_item)

    @pytest.mark.asyncio
    async def test_process_single_item_error(self, processor):
        """Test processing item with error handling."""
        mock_item = Mock(status=QueueItemStatus.RIPPED)
        
        with patch.object(processor, '_identify_item', new_callable=AsyncMock) as mock_identify:
            mock_identify.side_effect = Exception("Processing failed")
            
            await processor._process_single_item(mock_item)
            
            assert mock_item.status == QueueItemStatus.FAILED
            assert mock_item.error_message == "Processing failed"
            processor.queue_manager.update_item.assert_called_with(mock_item)
            processor.notifier.notify_error.assert_called_once()

    @pytest.mark.asyncio
    async def test_identify_item_success(self, processor, sample_media_info):
        """Test successful media identification."""
        mock_item = Mock(
            ripped_file=Path("/test/movie.mkv"),
            status=QueueItemStatus.RIPPED,
        )
        
        processor.identifier.identify_media.return_value = sample_media_info
        
        await processor._identify_item(mock_item)
        
        processor.identifier.identify_media.assert_called_once_with(Path("/test/movie.mkv"))
        assert mock_item.media_info == sample_media_info
        assert mock_item.status == QueueItemStatus.IDENTIFIED
        processor.queue_manager.update_item.assert_called_with(mock_item)

    @pytest.mark.asyncio
    async def test_identify_item_failure(self, processor):
        """Test media identification failure."""
        mock_item = Mock(
            ripped_file=Path("/test/movie.mkv"),
            status=QueueItemStatus.RIPPED,
            media_info=None,  # Initialize as None
        )
        
        processor.identifier.identify_media.return_value = None
        
        await processor._identify_item(mock_item)
        
        assert mock_item.media_info is None
        assert mock_item.status == QueueItemStatus.REVIEW
        processor.notifier.notify_unidentified_media.assert_called_once()
        processor.organizer.create_review_directory.assert_called_once_with(
            Path("/test/movie.mkv"), "unidentified"
        )

    @pytest.mark.asyncio
    async def test_encode_item_success(self, processor):
        """Test successful encoding."""
        mock_item = Mock(
            ripped_file=Path("/test/movie.mkv"),
            status=QueueItemStatus.IDENTIFIED,
        )
        
        mock_result = EncodeResult(
            success=True,
            input_file=Path("/test/movie.mkv"),
            output_file=Path("/test/movie_encoded.mkv"),
            duration=120,
            error_message=None,
        )
        processor.encoder.encode_file.return_value = mock_result
        
        await processor._encode_item(mock_item)
        
        processor.encoder.encode_file.assert_called_once()
        assert mock_item.encoded_file == Path("/test/movie_encoded.mkv")
        assert mock_item.status == QueueItemStatus.ENCODED
        processor.queue_manager.update_item.assert_called_with(mock_item)

    @pytest.mark.asyncio
    async def test_encode_item_failure(self, processor):
        """Test encoding failure."""
        mock_item = Mock(
            ripped_file=Path("/test/movie.mkv"),
            status=QueueItemStatus.IDENTIFIED,
        )
        
        mock_result = EncodeResult(
            success=False,
            input_file=Path("/test/movie.mkv"),
            output_file=None,
            duration=0,
            error_message="Encoding failed: invalid format",
        )
        processor.encoder.encode_file.return_value = mock_result
        
        await processor._encode_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.FAILED
        assert mock_item.error_message == "Encoding failed: invalid format"
        processor.queue_manager.update_item.assert_called_with(mock_item)

    @pytest.mark.asyncio
    async def test_encode_item_progress_callback(self, processor):
        """Test encoding with progress callback."""
        mock_item = Mock(
            ripped_file=Path("/test/movie.mkv"),
            status=QueueItemStatus.IDENTIFIED,
        )
        
        # Capture the progress callback
        captured_callback = None
        def capture_callback(file, output_dir, progress_callback=None):
            nonlocal captured_callback
            captured_callback = progress_callback
            return EncodeResult(
                success=True,
                input_file=file,
                output_file=Path("/test/movie_encoded.mkv"),
                duration=120,
                error_message=None,
            )
        
        processor.encoder.encode_file.side_effect = capture_callback
        
        await processor._encode_item(mock_item)
        
        # Test the progress callback
        assert captured_callback is not None
        captured_callback({"percentage": 50, "eta": "2:30", "speed": "2.5x"})
        
        # Should have updated item progress (the progress callback gets called when we call it manually)
        # We need to initialize the mock attributes
        mock_item.progress_stage = None
        mock_item.progress_percent = None
        
        # The progress callback should have been captured
        captured_callback({"type": "encoding_progress", "percent": 50, "speed": 2.5, "fps": 30, "eta_seconds": 150})
        
        # Now check that the mock item was updated
        assert mock_item.progress_stage == "encoding"
        assert mock_item.progress_percent == 50
        processor.queue_manager.update_item.assert_called()

    @pytest.mark.asyncio
    async def test_organize_item_success(self, processor, sample_media_info):
        """Test successful file organization."""
        mock_item = Mock(
            encoded_file=Path("/test/movie_encoded.mkv"),
            media_info=sample_media_info,
            status=QueueItemStatus.ENCODED,
        )
        
        processor.organizer.add_to_plex.return_value = True  # Return success
        
        await processor._organize_item(mock_item)
        
        processor.organizer.add_to_plex.assert_called_once_with(
            Path("/test/movie_encoded.mkv"),
            sample_media_info,
        )
        assert mock_item.status == QueueItemStatus.COMPLETED
        processor.notifier.notify_media_added.assert_called_once()

    @pytest.mark.asyncio
    async def test_organize_item_failure(self, processor, sample_media_info):
        """Test file organization failure."""
        mock_item = Mock(
            encoded_file=Path("/test/movie_encoded.mkv"),
            media_info=sample_media_info,
            status=QueueItemStatus.ENCODED,
        )
        
        processor.organizer.add_to_plex.return_value = False  # Return failure
        
        await processor._organize_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.FAILED
        assert mock_item.error_message == "Failed to organize/import to Plex"
        processor.queue_manager.update_item.assert_called_with(mock_item)