"""Test queue management."""

import pytest
from pathlib import Path

from spindle.storage.queue import QueueManager, QueueItem, QueueItemStatus
from spindle.config import SpindleConfig


class TestQueueManager:
    """Test QueueManager functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(log_dir=tmp_path / "logs")

    @pytest.fixture
    def manager(self, config):
        """Create queue manager instance."""
        return QueueManager(config)

    def test_add_disc_creates_item(self, manager):
        """Test adding disc creates queue item."""
        item = manager.add_disc("Test Movie")
        assert item.item_id is not None
        assert item.disc_title == "Test Movie"
        assert item.status == QueueItemStatus.PENDING

    def test_update_item_persists_changes(self, manager):
        """Test updating item persists to database."""
        item = manager.add_disc("Test Movie")
        item.status = QueueItemStatus.COMPLETED
        manager.update_item(item)

        retrieved_items = manager.get_items_by_status(QueueItemStatus.COMPLETED)
        assert len(retrieved_items) == 1
        assert retrieved_items[0].disc_title == "Test Movie"

    def test_get_queue_stats_returns_counts(self, manager):
        """Test queue statistics calculation."""
        manager.add_disc("Movie 1")
        item2 = manager.add_disc("Movie 2")
        item2.status = QueueItemStatus.COMPLETED
        manager.update_item(item2)

        stats = manager.get_queue_stats()
        assert stats["pending"] == 1
        assert stats["completed"] == 1

    def test_reset_stuck_items(self, manager):
        """Test resetting stuck processing items."""
        item = manager.add_disc("Test Movie")
        item.status = QueueItemStatus.RIPPING
        manager.update_item(item)

        reset_count = manager.reset_stuck_processing_items()
        assert reset_count == 1

        updated_items = manager.get_items_by_status(QueueItemStatus.PENDING)
        assert len(updated_items) == 1