"""Tests for queue management."""

import tempfile
from pathlib import Path

import pytest

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaInfo
from spindle.queue.manager import QueueItemStatus, QueueManager


@pytest.fixture
def temp_config():
    """Create a temporary configuration for testing."""
    with tempfile.TemporaryDirectory() as tmpdir:
        config = SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
        )
        config.ensure_directories()
        yield config


@pytest.fixture
def queue_manager(temp_config):
    """Create a queue manager for testing."""
    return QueueManager(temp_config)


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


def test_queue_manager_initialization(queue_manager):
    """Test that queue manager initializes correctly."""
    assert queue_manager.db_path.exists()

    # Should be able to get empty stats
    stats = queue_manager.get_queue_stats()
    assert stats == {}


def test_add_disc_to_queue(queue_manager):
    """Test adding a disc to the queue."""
    item = queue_manager.add_disc("TEST_DISC")

    assert item.item_id is not None
    assert item.disc_title == "TEST_DISC"
    assert item.status == QueueItemStatus.PENDING
    assert item.created_at is not None

    # Should be retrievable
    retrieved = queue_manager.get_item(item.item_id)
    assert retrieved is not None
    assert retrieved.disc_title == "TEST_DISC"


def test_add_file_to_queue(queue_manager, temp_config):
    """Test adding a file to the queue."""
    test_file = temp_config.staging_dir / "test.mkv"
    test_file.touch()  # Create empty file

    item = queue_manager.add_file(test_file)

    assert item.item_id is not None
    assert item.source_path == test_file
    assert item.status == QueueItemStatus.RIPPED
    assert item.ripped_file == test_file

    # Should be retrievable
    retrieved = queue_manager.get_item(item.item_id)
    assert retrieved is not None
    assert retrieved.source_path == test_file


def test_update_queue_item(queue_manager, sample_media_info):
    """Test updating a queue item."""
    item = queue_manager.add_disc("TEST_DISC")
    original_updated_at = item.updated_at

    # Update the item
    item.status = QueueItemStatus.IDENTIFIED
    item.media_info = sample_media_info
    item.error_message = "Test error"

    queue_manager.update_item(item)

    # Retrieve and verify
    updated = queue_manager.get_item(item.item_id)
    assert updated.status == QueueItemStatus.IDENTIFIED
    assert updated.media_info is not None
    assert updated.media_info.title == "Test Movie"
    assert updated.error_message == "Test error"
    assert updated.updated_at > original_updated_at


def test_get_items_by_status(queue_manager):
    """Test getting items by status."""
    # Add multiple items with different statuses
    item1 = queue_manager.add_disc("DISC_1")
    item2 = queue_manager.add_disc("DISC_2")
    item3 = queue_manager.add_disc("DISC_3")

    # Update statuses
    item2.status = QueueItemStatus.ENCODING
    queue_manager.update_item(item2)

    item3.status = QueueItemStatus.COMPLETED
    queue_manager.update_item(item3)

    # Test filtering
    pending = queue_manager.get_items_by_status(QueueItemStatus.PENDING)
    assert len(pending) == 1
    assert pending[0].disc_title == "DISC_1"

    encoding = queue_manager.get_items_by_status(QueueItemStatus.ENCODING)
    assert len(encoding) == 1
    assert encoding[0].disc_title == "DISC_2"

    completed = queue_manager.get_items_by_status(QueueItemStatus.COMPLETED)
    assert len(completed) == 1
    assert completed[0].disc_title == "DISC_3"


def test_get_pending_items(queue_manager):
    """Test getting items ready for processing."""
    # Add items with various statuses
    item1 = queue_manager.add_disc("PENDING")

    item2 = queue_manager.add_disc("RIPPED")
    item2.status = QueueItemStatus.RIPPED
    queue_manager.update_item(item2)

    item3 = queue_manager.add_disc("ENCODING")
    item3.status = QueueItemStatus.ENCODING
    queue_manager.update_item(item3)

    item4 = queue_manager.add_disc("COMPLETED")
    item4.status = QueueItemStatus.COMPLETED
    queue_manager.update_item(item4)

    # Get pending items
    pending = queue_manager.get_pending_items()

    # Should include PENDING and RIPPED, but not ENCODING or COMPLETED
    assert len(pending) == 2
    disc_titles = [item.disc_title for item in pending]
    assert "PENDING" in disc_titles
    assert "RIPPED" in disc_titles


def test_remove_item(queue_manager):
    """Test removing an item from the queue."""
    item = queue_manager.add_disc("TO_REMOVE")
    item_id = item.item_id

    # Should exist
    assert queue_manager.get_item(item_id) is not None

    # Remove it
    success = queue_manager.remove_item(item_id)
    assert success is True

    # Should no longer exist
    assert queue_manager.get_item(item_id) is None

    # Removing non-existent item should return False
    assert queue_manager.remove_item(999999) is False


def test_clear_completed(queue_manager):
    """Test clearing completed items."""
    # Add various items
    item1 = queue_manager.add_disc("PENDING")

    item2 = queue_manager.add_disc("COMPLETED")
    item2.status = QueueItemStatus.COMPLETED
    queue_manager.update_item(item2)

    item3 = queue_manager.add_disc("FAILED")
    item3.status = QueueItemStatus.FAILED
    queue_manager.update_item(item3)

    item4 = queue_manager.add_disc("ENCODING")
    item4.status = QueueItemStatus.ENCODING
    queue_manager.update_item(item4)

    # Clear completed
    count = queue_manager.clear_completed()
    assert count == 2  # COMPLETED and FAILED

    # Verify remaining items
    all_items = queue_manager.get_all_items()
    assert len(all_items) == 2

    disc_titles = [item.disc_title for item in all_items]
    assert "PENDING" in disc_titles
    assert "ENCODING" in disc_titles


def test_queue_stats(queue_manager):
    """Test getting queue statistics."""
    # Add items with various statuses
    queue_manager.add_disc("PENDING_1")
    queue_manager.add_disc("PENDING_2")

    item = queue_manager.add_disc("COMPLETED")
    item.status = QueueItemStatus.COMPLETED
    queue_manager.update_item(item)

    item = queue_manager.add_disc("FAILED")
    item.status = QueueItemStatus.FAILED
    queue_manager.update_item(item)

    stats = queue_manager.get_queue_stats()

    assert stats["pending"] == 2
    assert stats["completed"] == 1
    assert stats["failed"] == 1


def test_media_info_serialization(queue_manager, sample_media_info):
    """Test that media info is properly serialized and deserialized."""
    item = queue_manager.add_disc("TEST_DISC")
    item.media_info = sample_media_info
    queue_manager.update_item(item)

    # Retrieve and verify media info
    retrieved = queue_manager.get_item(item.item_id)
    assert retrieved.media_info is not None
    assert retrieved.media_info.title == "Test Movie"
    assert retrieved.media_info.year == 2023
    assert retrieved.media_info.media_type == "movie"
    assert retrieved.media_info.tmdb_id == 12345
    assert retrieved.media_info.overview == "A test movie"
    assert retrieved.media_info.genres == ["Action", "Drama"]


def test_clear_all_with_force(queue_manager):
    """Test force clearing all items including processing ones."""
    # Add items in various states
    item1 = queue_manager.add_disc("PENDING")
    
    item2 = queue_manager.add_disc("RIPPING")
    item2.status = QueueItemStatus.RIPPING
    queue_manager.update_item(item2)
    
    item3 = queue_manager.add_disc("COMPLETED")
    item3.status = QueueItemStatus.COMPLETED
    queue_manager.update_item(item3)
    
    # Try normal clear - should fail
    with pytest.raises(RuntimeError, match="Cannot clear queue"):
        queue_manager.clear_all()
    
    # Force clear should work
    count = queue_manager.clear_all(force=True)
    assert count == 3
    assert len(queue_manager.get_all_items()) == 0


def test_reset_stuck_processing_items(queue_manager):
    """Test resetting stuck processing items to pending."""
    # Add items in various states
    item1 = queue_manager.add_disc("PENDING")
    
    item2 = queue_manager.add_disc("RIPPING") 
    item2.status = QueueItemStatus.RIPPING
    queue_manager.update_item(item2)
    
    item3 = queue_manager.add_disc("ENCODING")
    item3.status = QueueItemStatus.ENCODING
    queue_manager.update_item(item3)
    
    item4 = queue_manager.add_disc("COMPLETED")
    item4.status = QueueItemStatus.COMPLETED
    queue_manager.update_item(item4)
    
    # Reset stuck items
    count = queue_manager.reset_stuck_processing_items()
    assert count == 2  # Should reset RIPPING and ENCODING items
    
    # Check items are now pending
    item2_updated = queue_manager.get_item(item2.item_id)
    assert item2_updated.status == QueueItemStatus.PENDING
    assert item2_updated.progress_stage == "Reset from stuck processing"
    
    item3_updated = queue_manager.get_item(item3.item_id)
    assert item3_updated.status == QueueItemStatus.PENDING
    
    # Completed item should be unchanged
    item4_updated = queue_manager.get_item(item4.item_id)
    assert item4_updated.status == QueueItemStatus.COMPLETED
