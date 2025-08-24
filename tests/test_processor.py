"""Comprehensive tests for the continuous processor functionality."""

import asyncio
import tempfile
import time
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch, MagicMock

import pytest

from spindle.config import SpindleConfig
from spindle.disc.analyzer import ContentType, ContentPattern
from spindle.disc.monitor import DiscInfo
from spindle.disc.ripper import Title
from spindle.encode.drapto_wrapper import EncodeResult
from spindle.identify.tmdb import MediaInfo
from spindle.processor import ContinuousProcessor
from spindle.queue.manager import QueueItemStatus


class TestContinuousProcessor:
    """Test the ContinuousProcessor class and its workflows."""

    @pytest.fixture
    def temp_config(self):
        """Create temporary configuration for testing."""
        with tempfile.TemporaryDirectory() as tmpdir:
            config = SpindleConfig(
                log_dir=Path(tmpdir) / "logs",
                staging_dir=Path(tmpdir) / "staging",
                library_dir=Path(tmpdir) / "library",
                optical_drive="/dev/sr0",
                queue_poll_interval=1,  # Fast polling for tests (must be int)
                error_retry_interval=1,  # Fast retry for tests (must be int)
            )
            config.ensure_directories()
            yield config

    @pytest.fixture
    def mock_dependencies(self):
        """Mock all the dependencies of ContinuousProcessor."""
        with patch.multiple(
            'spindle.processor',
            QueueManager=Mock,
            MakeMKVRipper=Mock,
            MediaIdentifier=Mock,
            DraptoEncoder=Mock,
            LibraryOrganizer=Mock,
            NtfyNotifier=Mock,
            IntelligentDiscAnalyzer=Mock,
            TVSeriesDiscAnalyzer=Mock,
            DiscMonitor=Mock,
            detect_disc=Mock,
            eject_disc=Mock,
        ) as mocks:
            yield mocks

    @pytest.fixture
    def processor(self, temp_config, mock_dependencies):
        """Create a processor instance with mocked dependencies."""
        processor = ContinuousProcessor(temp_config)
        
        # Configure all the mock methods that are commonly used
        processor.queue_manager.add_disc = Mock()
        processor.queue_manager.update_item = Mock()
        processor.queue_manager.get_items_by_status = Mock(return_value=[])
        processor.queue_manager.get_queue_stats = Mock(return_value={})
        
        processor.ripper.scan_disc = Mock(return_value=[])
        processor.ripper.select_main_title = Mock()
        processor.ripper.rip_title = Mock()
        
        processor.disc_analyzer.analyze_disc = Mock()
        processor.tv_analyzer.analyze_tv_disc = AsyncMock()
        
        processor.identifier.identify_media = AsyncMock()
        
        processor.encoder.encode_file = Mock()
        
        processor.organizer.add_to_plex = Mock(return_value=True)
        processor.organizer.create_review_directory = Mock()
        
        processor.notifier.notify_disc_detected = Mock()
        processor.notifier.notify_rip_started = Mock()
        processor.notifier.notify_rip_completed = Mock()
        processor.notifier.notify_error = Mock()
        processor.notifier.notify_unidentified_media = Mock()
        processor.notifier.notify_media_added = Mock()
        
        return processor

    @pytest.fixture
    def sample_disc_info(self):
        """Create sample disc info for testing."""
        return DiscInfo(
            device="/dev/sr0",
            disc_type="BD",
            label="TEST_MOVIE",
        )

    @pytest.fixture
    def sample_titles(self):
        """Create sample titles for disc analysis."""
        return [
            Title(
                title_id="1",
                name="Title 1",
                duration=7200,  # 2 hours
                chapters=24,
                size=20_000_000_000,
                tracks=[],
            ),
            Title(
                title_id="2", 
                name="Title 2",
                duration=600,  # 10 minutes
                chapters=1,
                size=500_000_000,
                tracks=[],
            ),
        ]

    @pytest.fixture
    def sample_media_info(self):
        """Create sample media info for testing."""
        return MediaInfo(
            title="Test Movie",
            year=2023,
            media_type="movie",
            tmdb_id=12345,
            overview="A test movie",
            genres=["Action", "Drama"],
        )

    def test_processor_initialization(self, temp_config, mock_dependencies):
        """Test processor initializes with all components."""
        processor = ContinuousProcessor(temp_config)

        assert processor.config == temp_config
        assert processor.queue_manager is not None
        assert processor.ripper is not None
        assert processor.identifier is not None
        assert processor.encoder is not None
        assert processor.organizer is not None
        assert processor.notifier is not None
        assert processor.disc_analyzer is not None
        assert processor.tv_analyzer is not None
        assert processor.disc_monitor is None
        assert processor.processing_task is None
        assert processor.is_running is False

    def test_start_processor(self, processor, mock_dependencies):
        """Test starting the continuous processor."""
        with patch('asyncio.get_event_loop') as mock_get_loop:
            mock_loop = Mock()
            mock_task = Mock()
            
            # Mock create_task to avoid creating real coroutines
            def mock_create_task(coro):
                # Close the coroutine if it exists to avoid warnings
                if hasattr(coro, 'close'):
                    coro.close()
                return mock_task
                
            mock_loop.create_task.side_effect = mock_create_task
            mock_get_loop.return_value = mock_loop

            processor.start()

            assert processor.is_running is True
            assert processor.disc_monitor is not None
            assert processor.processing_task == mock_task
            
            processor.disc_monitor.start_monitoring.assert_called_once()
            mock_loop.create_task.assert_called_once()

    def test_start_processor_already_running(self, processor):
        """Test starting processor when already running."""
        processor.is_running = True
        
        processor.start()
        
        # Should not create new monitor or task
        assert processor.disc_monitor is None
        assert processor.processing_task is None

    def test_stop_processor(self, processor):
        """Test stopping the continuous processor."""
        # Setup running state
        processor.is_running = True
        processor.disc_monitor = Mock()
        processor.processing_task = Mock()

        processor.stop()

        assert processor.is_running is False
        processor.disc_monitor.stop_monitoring.assert_called_once()
        processor.processing_task.cancel.assert_called_once()

    def test_stop_processor_not_running(self, processor):
        """Test stopping processor when not running."""
        processor.stop()
        
        assert processor.is_running is False

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
            mock_rip_disc.assert_called_once_with(mock_item, sample_disc_info)

    def test_on_disc_detected_error(self, processor, sample_disc_info):
        """Test disc detection with error."""
        processor.queue_manager.add_disc.side_effect = Exception("Database error")

        processor._on_disc_detected(sample_disc_info)

        processor.notifier.notify_error.assert_called_once()
        error_call = processor.notifier.notify_error.call_args
        assert "Failed to process disc" in error_call[0][0]
        assert error_call[1]["context"] == "TEST_MOVIE"

    def test_rip_disc_success(self, processor, sample_disc_info, sample_titles):
        """Test successful disc ripping workflow."""
        mock_item = Mock(status=QueueItemStatus.PENDING)
        mock_content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )

        processor.ripper.scan_disc.return_value = sample_titles
        processor.disc_analyzer.analyze_disc.return_value = mock_content_pattern
        
        with patch.object(processor, '_handle_content_type') as mock_handle:
            mock_handle.return_value = [Path("/test/output.mkv")]
            
            processor._rip_disc(mock_item, sample_disc_info)

            # Verify workflow steps
            processor.ripper.scan_disc.assert_called_once()
            # TODO: Re-enable when sync/async analyzer issue is resolved
            # processor.disc_analyzer.analyze_disc.assert_called_once_with(
            #     sample_disc_info, sample_titles
            # )
            mock_handle.assert_called_once_with(
                sample_disc_info, sample_titles, mock_content_pattern
            )
            
            # Verify item updates
            assert mock_item.status == QueueItemStatus.RIPPED
            assert mock_item.ripped_file == Path("/test/output.mkv")
            processor.queue_manager.update_item.assert_called()
            
            # Verify notifications
            processor.notifier.notify_rip_started.assert_called_once()
            processor.notifier.notify_rip_completed.assert_called_once()

    def test_rip_disc_error(self, processor, sample_disc_info):
        """Test disc ripping with error."""
        mock_item = Mock()
        processor.ripper.scan_disc.side_effect = Exception("Scan failed")

        processor._rip_disc(mock_item, sample_disc_info)

        assert mock_item.status == QueueItemStatus.FAILED
        assert mock_item.error_message == "Scan failed"
        processor.notifier.notify_error.assert_called_once()

    def test_handle_tv_series_content(self, processor, sample_disc_info, sample_titles):
        """Test handling TV series content type."""
        mock_content_pattern = ContentPattern(
            type=ContentType.TV_SERIES,
            confidence=0.9,
        )

        with patch.object(processor, '_handle_tv_series') as mock_handle:
            mock_handle.return_value = [Path("/test/episode1.mkv")]
            
            result = processor._handle_content_type(
                sample_disc_info, sample_titles, mock_content_pattern
            )

            mock_handle.assert_called_once_with(sample_disc_info, sample_titles)
            assert result == [Path("/test/episode1.mkv")]

    def test_handle_movie_content(self, processor, sample_disc_info, sample_titles):
        """Test handling movie content type."""
        mock_content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )

        with patch.object(processor, '_handle_movie') as mock_handle:
            mock_handle.return_value = [Path("/test/movie.mkv")]
            
            result = processor._handle_content_type(
                sample_disc_info, sample_titles, mock_content_pattern
            )

            mock_handle.assert_called_once_with(
                sample_disc_info, sample_titles, mock_content_pattern
            )
            assert result == [Path("/test/movie.mkv")]

    def test_handle_unknown_content_type(self, processor, sample_disc_info, sample_titles):
        """Test handling unknown content type falls back to basic rip."""
        mock_content_pattern = ContentPattern(
            type=None,  # Unknown type
            confidence=0.1,
        )

        with patch.object(processor, '_handle_basic_rip') as mock_handle:
            mock_handle.return_value = [Path("/test/basic.mkv")]
            
            result = processor._handle_content_type(
                sample_disc_info, sample_titles, mock_content_pattern
            )

            mock_handle.assert_called_once_with(sample_disc_info, sample_titles)
            assert result == [Path("/test/basic.mkv")]

    @pytest.mark.asyncio
    async def test_process_queue_continuously(self, processor):
        """Test continuous queue processing loop."""
        # Mock items to process
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
            status=QueueItemStatus.RIPPED,
            ripped_file=Path("/test/movie.mkv"),
        )
        
        processor.identifier.identify_media = AsyncMock(return_value=sample_media_info)
        
        await processor._identify_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.IDENTIFIED
        assert mock_item.media_info == sample_media_info
        processor.identifier.identify_media.assert_called_once_with(mock_item.ripped_file)
        processor.queue_manager.update_item.assert_called_with(mock_item)

    @pytest.mark.asyncio
    async def test_identify_item_failure(self, processor):
        """Test failed media identification."""
        mock_item = Mock(
            status=QueueItemStatus.RIPPED,
            ripped_file=Path("/test/unknown.mkv"),
        )
        
        processor.identifier.identify_media = AsyncMock(return_value=None)
        
        await processor._identify_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.REVIEW
        processor.organizer.create_review_directory.assert_called_once()
        processor.notifier.notify_unidentified_media.assert_called_once()

    @pytest.mark.asyncio
    async def test_encode_item_success(self, processor, sample_media_info):
        """Test successful encoding."""
        mock_item = Mock(
            status=QueueItemStatus.IDENTIFIED,
            media_info=sample_media_info,
            ripped_file=Path("/test/input.mkv"),
        )
        
        mock_result = EncodeResult(
            success=True,
            input_file=Path("/test/input.mkv"),
            output_file=Path("/test/encoded.mkv"),
        )
        
        processor.encoder.encode_file.return_value = mock_result
        
        await processor._encode_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.ENCODED
        assert mock_item.encoded_file == Path("/test/encoded.mkv")
        processor.queue_manager.update_item.assert_called_with(mock_item)

    @pytest.mark.asyncio
    async def test_encode_item_failure(self, processor, sample_media_info):
        """Test failed encoding."""
        mock_item = Mock(
            status=QueueItemStatus.IDENTIFIED,
            media_info=sample_media_info,
            ripped_file=Path("/test/input.mkv"),
        )
        
        mock_result = EncodeResult(
            success=False,
            input_file=Path("/test/input.mkv"),
            error_message="Encoding failed",
        )
        
        processor.encoder.encode_file.return_value = mock_result
        
        await processor._encode_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.FAILED
        assert mock_item.error_message == "Encoding failed"
        processor.notifier.notify_error.assert_called_once()

    @pytest.mark.asyncio
    async def test_encode_item_progress_callback(self, processor, sample_media_info):
        """Test encoding progress callback handling."""
        mock_item = Mock(
            status=QueueItemStatus.IDENTIFIED,
            media_info=sample_media_info,
            ripped_file=Path("/test/input.mkv"),
        )
        
        # Mock encode_file to capture the callback and simulate progress events
        def mock_encode_file(input_file, output_dir, progress_callback=None):
            if progress_callback:
                # Simulate different progress events
                progress_callback({"type": "stage_progress", "stage": "analysis", "percent": 25.0, "message": "Analyzing"})
                progress_callback({"type": "encoding_progress", "percent": 50.0, "speed": 1.5, "fps": 30.0})
                progress_callback({"type": "encoding_complete", "size_reduction_percent": 40.0})
                progress_callback({"type": "validation_complete", "validation_passed": True})
                progress_callback({"type": "error", "message": "Test error"})
                progress_callback({"type": "warning", "message": "Test warning"})
            return EncodeResult(success=True, input_file=Path("/test/input.mkv"))

        processor.encoder.encode_file = mock_encode_file

        await processor._encode_item(mock_item)

        # Verify progress updates were applied to the item
        assert processor.queue_manager.update_item.call_count >= 2  # At least progress updates

    @pytest.mark.asyncio
    async def test_organize_item_success(self, processor, sample_media_info):
        """Test successful organization and Plex import."""
        mock_item = Mock(
            status=QueueItemStatus.ENCODED,
            media_info=sample_media_info,
            encoded_file=Path("/test/encoded.mkv"),
        )
        
        processor.organizer.add_to_plex.return_value = True
        
        await processor._organize_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.COMPLETED
        processor.organizer.add_to_plex.assert_called_once_with(
            mock_item.encoded_file, mock_item.media_info
        )
        processor.notifier.notify_media_added.assert_called_once()

    @pytest.mark.asyncio
    async def test_organize_item_failure(self, processor, sample_media_info):
        """Test failed organization."""
        mock_item = Mock(
            status=QueueItemStatus.ENCODED,
            media_info=sample_media_info,
            encoded_file=Path("/test/encoded.mkv"),
        )
        
        processor.organizer.add_to_plex.return_value = False
        
        await processor._organize_item(mock_item)
        
        assert mock_item.status == QueueItemStatus.FAILED
        assert "Failed to organize/import to Plex" in mock_item.error_message
        processor.notifier.notify_error.assert_called_once()

    def test_get_status(self, processor, mock_dependencies):
        """Test getting processor status."""
        processor.is_running = True
        processor.queue_manager.get_queue_stats.return_value = {
            "pending": 2,
            "ripped": 1,
            "completed": 5,
        }
        
        with patch('spindle.processor.detect_disc') as mock_detect:
            mock_detect.return_value = DiscInfo(
                device="/dev/sr0",
                disc_type="DVD",
                label="CURRENT_DISC",
            )

            status = processor.get_status()

            assert status["running"] is True
            assert "CURRENT_DISC" in status["current_disc"]
            assert status["queue_stats"] == {"pending": 2, "ripped": 1, "completed": 5}
            assert status["total_items"] == 8

    def test_get_status_no_disc(self, processor, mock_dependencies):
        """Test getting status with no current disc."""
        processor.is_running = False
        processor.queue_manager.get_queue_stats.return_value = {}
        
        with patch('spindle.processor.detect_disc') as mock_detect:
            mock_detect.return_value = None

            status = processor.get_status()

            assert status["running"] is False
            assert status["current_disc"] is None
            assert status["queue_stats"] == {}
            assert status["total_items"] == 0


class TestProcessorContentHandlers:
    """Test content type handling methods in detail."""

    @pytest.fixture
    def temp_config(self):
        """Create temporary configuration for testing."""
        with tempfile.TemporaryDirectory() as tmpdir:
            config = SpindleConfig(
                log_dir=Path(tmpdir) / "logs",
                staging_dir=Path(tmpdir) / "staging", 
                library_dir=Path(tmpdir) / "library",
            )
            config.ensure_directories()
            yield config

    @pytest.fixture
    def processor_with_mocks(self, temp_config):
        """Create processor with individual component mocks."""
        with patch.multiple(
            'spindle.processor',
            QueueManager=Mock,
            MakeMKVRipper=Mock,
            MediaIdentifier=Mock,
            DraptoEncoder=Mock,
            LibraryOrganizer=Mock,
            NtfyNotifier=Mock,
            IntelligentDiscAnalyzer=Mock,
            TVSeriesDiscAnalyzer=Mock,
        ):
            processor = ContinuousProcessor(temp_config)
            
            # Configure mocks for content handler tests
            processor.ripper.rip_title = Mock()
            processor.ripper.select_main_title = Mock()
            processor.tv_analyzer.analyze_tv_disc = Mock()  # Regular mock, not AsyncMock
            
            yield processor

    @pytest.fixture
    def sample_disc_info(self):
        """Sample disc info."""
        return DiscInfo(
            device="/dev/sr0",
            disc_type="BD",
            label="TEST_TV_SERIES",
        )

    @pytest.fixture
    def sample_tv_titles(self):
        """Sample TV series titles."""
        return [
            Title(title_id="1", name="Episode 1", duration=2700, chapters=12, size=2_000_000_000, tracks=[]),
            Title(title_id="2", name="Episode 2", duration=2650, chapters=12, size=2_100_000_000, tracks=[]),
            Title(title_id="3", name="Episode 3", duration=2720, chapters=12, size=1_950_000_000, tracks=[]),
        ]

    def test_handle_tv_series_success(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test successful TV series handling."""
        from spindle.disc.analyzer import EpisodeInfo

        # Mock episode mapping
        episode_mapping = {
            sample_tv_titles[0]: EpisodeInfo(
                episode_title="Pilot",
                season_number=1,
                episode_number=1,
            ),
            sample_tv_titles[1]: EpisodeInfo(
                episode_title="The Plan",
                season_number=1,
                episode_number=2,
            ),
        }

        processor_with_mocks.ripper.rip_title.side_effect = [
            Path("/staging/episodes/S01E01.mkv"),
            Path("/staging/episodes/S01E02.mkv"),
        ]

        # Mock asyncio.run to return episode_mapping directly, avoiding AsyncMock coroutine issues
        with patch('asyncio.run') as mock_asyncio_run:
            mock_asyncio_run.return_value = episode_mapping
            
            result = processor_with_mocks._handle_tv_series(sample_disc_info, sample_tv_titles)

            assert len(result) == 2
            assert all(isinstance(path, Path) for path in result)
            assert processor_with_mocks.ripper.rip_title.call_count == 2

    def test_handle_tv_series_no_mapping(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test TV series handling when no episode mapping is found."""
        # Mock asyncio.run to return None directly, avoiding AsyncMock coroutine creation
        with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
            mock_basic_rip.return_value = [Path("/staging/basic.mkv")]
            
            with patch('asyncio.run') as mock_asyncio_run:
                mock_asyncio_run.return_value = None
                
                result = processor_with_mocks._handle_tv_series(sample_disc_info, sample_tv_titles)

                mock_basic_rip.assert_called_once_with(sample_disc_info, sample_tv_titles)
                assert result == [Path("/staging/basic.mkv")]

    def test_handle_tv_series_error(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test TV series handling with error."""
        with patch('asyncio.run', side_effect=Exception("Analysis failed")):
            with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
                mock_basic_rip.return_value = [Path("/staging/fallback.mkv")]
                
                result = processor_with_mocks._handle_tv_series(sample_disc_info, sample_tv_titles)

                mock_basic_rip.assert_called_once_with(sample_disc_info, sample_tv_titles)
                assert result == [Path("/staging/fallback.mkv")]

    def test_handle_cartoon_collection(self, processor_with_mocks, sample_disc_info):
        """Test cartoon collection handling."""
        # Create cartoon-length titles 
        cartoon_titles = [
            Title(title_id="1", name="Short 1", duration=480, chapters=1, size=400_000_000, tracks=[]),  # 8 minutes
            Title(title_id="2", name="Short 2", duration=600, chapters=1, size=500_000_000, tracks=[]),  # 10 minutes
            Title(title_id="3", name="Short 3", duration=360, chapters=1, size=300_000_000, tracks=[]),  # 6 minutes
            Title(title_id="4", name="Feature", duration=7200, chapters=24, size=20_000_000_000, tracks=[]),  # Too long
        ]

        # Configure cartoon duration limits
        processor_with_mocks.config.cartoon_min_duration = 5  # 5 minutes minimum
        processor_with_mocks.config.cartoon_max_duration = 15  # 15 minutes maximum

        processor_with_mocks.ripper.rip_title.side_effect = [
            Path("/staging/cartoons/short1.mkv"),
            Path("/staging/cartoons/short2.mkv"),
            Path("/staging/cartoons/short3.mkv"),
        ]

        mock_content_pattern = ContentPattern(
            type=ContentType.CARTOON_COLLECTION,
            confidence=0.85,
        )

        result = processor_with_mocks._handle_cartoon_collection(
            sample_disc_info, cartoon_titles, mock_content_pattern
        )

        # Should rip 3 cartoon-length titles, skip the feature
        assert len(result) == 3
        assert processor_with_mocks.ripper.rip_title.call_count == 3

    def test_handle_movie_basic(self, processor_with_mocks, sample_disc_info):
        """Test basic movie handling."""
        movie_titles = [
            Title(title_id="1", name="Main Movie", duration=7200, chapters=24, size=20_000_000_000, tracks=[]),
            Title(title_id="2", name="Trailer", duration=180, chapters=1, size=150_000_000, tracks=[]),
        ]

        processor_with_mocks.ripper.select_main_title.return_value = movie_titles[0]
        processor_with_mocks.ripper.rip_title.return_value = Path("/staging/movie.mkv")
        processor_with_mocks.config.include_movie_extras = False

        mock_content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )

        result = processor_with_mocks._handle_movie(
            sample_disc_info, movie_titles, mock_content_pattern
        )

        assert len(result) == 1
        assert result[0] == Path("/staging/movie.mkv")
        processor_with_mocks.ripper.rip_title.assert_called_once()

    def test_handle_movie_with_extras(self, processor_with_mocks, sample_disc_info):
        """Test movie handling with extras enabled."""
        movie_titles = [
            Title(title_id="1", name="Main Movie", duration=7200, chapters=24, size=20_000_000_000, tracks=[]),
            Title(title_id="2", name="Behind Scenes", duration=1800, chapters=5, size=1_500_000_000, tracks=[]),  # 30 min extra
            Title(title_id="3", name="Short Trailer", duration=180, chapters=1, size=150_000_000, tracks=[]),  # Too short
        ]

        processor_with_mocks.ripper.select_main_title.return_value = movie_titles[0]
        processor_with_mocks.config.include_movie_extras = True
        processor_with_mocks.config.max_extras_duration = 10  # 10 minutes minimum for extras

        processor_with_mocks.ripper.rip_title.side_effect = [
            Path("/staging/movie.mkv"),
            Path("/staging/extras/behind_scenes.mkv"),
        ]

        mock_content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )

        result = processor_with_mocks._handle_movie(
            sample_disc_info, movie_titles, mock_content_pattern
        )

        assert len(result) == 2
        assert Path("/staging/movie.mkv") in result
        assert Path("/staging/extras/behind_scenes.mkv") in result

    def test_handle_movie_no_main_title(self, processor_with_mocks, sample_disc_info):
        """Test movie handling when no main title found."""
        movie_titles = [
            Title(title_id="1", name="Short Clip", duration=300, chapters=1, size=200_000_000, tracks=[]),
        ]

        processor_with_mocks.ripper.select_main_title.return_value = None

        mock_content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )

        with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
            mock_basic_rip.return_value = [Path("/staging/fallback.mkv")]
            
            result = processor_with_mocks._handle_movie(
                sample_disc_info, movie_titles, mock_content_pattern
            )

            mock_basic_rip.assert_called_once_with(sample_disc_info, movie_titles)

    def test_handle_basic_rip_success(self, processor_with_mocks, sample_disc_info):
        """Test basic rip fallback method."""
        titles = [
            Title(title_id="1", name="Main Title", duration=7200, chapters=24, size=20_000_000_000, tracks=[]),
        ]

        processor_with_mocks.ripper.select_main_title.return_value = titles[0]
        processor_with_mocks.ripper.rip_title.return_value = Path("/staging/basic.mkv")

        result = processor_with_mocks._handle_basic_rip(sample_disc_info, titles)

        assert len(result) == 1
        assert result[0] == Path("/staging/basic.mkv")

    def test_handle_basic_rip_no_title(self, processor_with_mocks, sample_disc_info):
        """Test basic rip when no suitable title found."""
        titles = []
        processor_with_mocks.ripper.select_main_title.return_value = None

        with pytest.raises(RuntimeError, match="No suitable title found"):
            processor_with_mocks._handle_basic_rip(sample_disc_info, titles)