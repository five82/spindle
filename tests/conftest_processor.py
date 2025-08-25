"""Shared fixtures for processor tests."""

import asyncio
import tempfile
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.analyzer import ContentType, ContentPattern
from spindle.disc.monitor import DiscInfo
from spindle.disc.ripper import Title
from spindle.processor import ContinuousProcessor


@pytest.fixture
def temp_config():
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
def mock_dependencies():
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


@pytest.fixture(scope="function")  
def processor(temp_config, mock_dependencies):
    """Create a processor instance with mocked dependencies.""" 
    processor = ContinuousProcessor(temp_config)
    
    # Ensure clean state for testing
    processor.is_running = False
    processor.disc_monitor = None
    processor.processing_task = None
    
    # Configure all the mock methods that are commonly used
    processor.queue_manager.add_disc = Mock()
    processor.queue_manager.update_item = Mock()
    processor.queue_manager.get_items_by_status = Mock(return_value=[])
    processor.queue_manager.get_queue_stats = Mock(return_value={})
    processor.queue_manager.reset_stuck_processing_items = Mock(return_value=0)
    
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
    
    yield processor
    
    # Clean up any coroutines that may have been created during testing
    if hasattr(processor, 'processing_task') and processor.processing_task is not None:
        if hasattr(processor.processing_task, 'close'):
            try:
                processor.processing_task.close()
            except Exception:
                pass  # Ignore cleanup errors in tests


@pytest.fixture
def sample_disc_info():
    """Create sample disc info for testing."""
    return DiscInfo(
        device="/dev/sr0",
        disc_type="BD",
        label="TEST_MOVIE",
    )


@pytest.fixture
def sample_titles():
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