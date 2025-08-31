"""Essential queue management tests - core workflow engine."""

import tempfile
from pathlib import Path

import pytest

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaInfo
from spindle.queue.manager import QueueItemStatus, QueueManager


@pytest.fixture
def temp_config():
    """Create temporary config for testing."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
        )


@pytest.fixture
def queue_manager(temp_config):
    """Create queue manager instance."""
    return QueueManager(temp_config)


@pytest.fixture
def sample_media_info():
    """Sample media info for testing."""
    return MediaInfo(
        title="Test Movie",
        year=2023,
        media_type="movie",
        tmdb_id=12345,
        overview="A test movie",
        genres=["Action"]
    )


class TestQueueBasics:
    """Test essential queue operations."""
    
    def test_queue_initialization(self, queue_manager):
        """Test queue manager initializes properly."""
        assert queue_manager.db_path.exists()
        
        stats = queue_manager.get_queue_stats()
        assert len(stats) == 0  # No items initially

    def test_add_disc_to_queue(self, queue_manager):
        """Test adding disc creates queue item."""
        item = queue_manager.add_disc("TEST_DISC")
        
        assert item.item_id > 0
        assert item.disc_title == "TEST_DISC"
        assert item.status == QueueItemStatus.PENDING
        
        stats = queue_manager.get_queue_stats()
        assert stats["pending"] == 1

    def test_status_transitions(self, queue_manager):
        """Test queue item progresses through workflow."""
        item = queue_manager.add_disc("TEST_DISC")
        
        # PENDING -> RIPPED
        item.status = QueueItemStatus.RIPPED
        item.ripped_file = Path("/staging/movie.mkv")
        queue_manager.update_item(item)
        
        retrieved = queue_manager.get_item(item.item_id)
        assert retrieved.status == QueueItemStatus.RIPPED
        assert retrieved.ripped_file == Path("/staging/movie.mkv")
        
        # RIPPED -> IDENTIFIED
        item.status = QueueItemStatus.IDENTIFIED
        item.media_info = MediaInfo(title="Test Movie", year=2023, media_type="movie", tmdb_id=12345)
        queue_manager.update_item(item)
        
        retrieved = queue_manager.get_item(item.item_id)
        assert retrieved.status == QueueItemStatus.IDENTIFIED
        assert retrieved.media_info.title == "Test Movie"
        
        # IDENTIFIED -> ENCODED -> COMPLETED  
        for status in [QueueItemStatus.ENCODED, QueueItemStatus.COMPLETED]:
            item.status = status
            queue_manager.update_item(item)
            retrieved = queue_manager.get_item(item.item_id)
            assert retrieved.status == status

    def test_progress_tracking(self, queue_manager):
        """Test progress fields update correctly."""
        item = queue_manager.add_disc("TEST_DISC")
        
        # Update progress
        item.progress_stage = "encoding"
        item.progress_percent = 75.0
        item.progress_message = "Processing at 1.2x speed"
        queue_manager.update_item(item)
        
        retrieved = queue_manager.get_item(item.item_id)
        assert retrieved.progress_stage == "encoding"
        assert retrieved.progress_percent == 75.0
        assert retrieved.progress_message == "Processing at 1.2x speed"


class TestQueueFiltering:
    """Test queue filtering and retrieval."""
    
    def test_get_items_by_status(self, queue_manager):
        """Test filtering items by status."""
        # Add items in different states
        item1 = queue_manager.add_disc("DISC_1")
        item2 = queue_manager.add_disc("DISC_2")
        
        item2.status = QueueItemStatus.RIPPED
        queue_manager.update_item(item2)
        
        pending_items = queue_manager.get_items_by_status(QueueItemStatus.PENDING)
        ripped_items = queue_manager.get_items_by_status(QueueItemStatus.RIPPED)
        
        assert len(pending_items) == 1
        assert len(ripped_items) == 1
        assert pending_items[0].disc_title == "DISC_1"
        assert ripped_items[0].disc_title == "DISC_2"

    def test_queue_stats(self, queue_manager):
        """Test queue statistics calculation."""
        # Add items in various states
        items = []
        for i in range(3):
            items.append(queue_manager.add_disc(f"DISC_{i}"))
        
        # Set different statuses
        items[1].status = QueueItemStatus.COMPLETED
        items[2].status = QueueItemStatus.FAILED
        queue_manager.update_item(items[1])
        queue_manager.update_item(items[2])
        
        stats = queue_manager.get_queue_stats()
        assert stats["pending"] == 1
        assert stats["completed"] == 1
        assert stats["failed"] == 1


class TestQueueMaintenance:
    """Test queue cleanup and maintenance."""
    
    def test_clear_completed(self, queue_manager):
        """Test clearing completed items."""
        # Add and complete items
        for i in range(3):
            item = queue_manager.add_disc(f"DISC_{i}")
            if i < 2:  # Complete first 2
                item.status = QueueItemStatus.COMPLETED
                queue_manager.update_item(item)
        
        removed_count = queue_manager.clear_completed()
        assert removed_count == 2
        
        stats = queue_manager.get_queue_stats()
        assert stats.get("pending", 0) == 1  # Only pending item remains
        assert "completed" not in stats  # No completed items left

    def test_media_info_serialization(self, queue_manager, sample_media_info):
        """Test media info serializes/deserializes correctly."""
        item = queue_manager.add_disc("TEST_DISC")
        item.media_info = sample_media_info
        queue_manager.update_item(item)
        
        retrieved = queue_manager.get_item(item.item_id)
        assert retrieved.media_info.title == "Test Movie"
        assert retrieved.media_info.year == 2023
        assert retrieved.media_info.media_type == "movie"
        assert retrieved.media_info.tmdb_id == 12345
        assert "Action" in retrieved.media_info.genres