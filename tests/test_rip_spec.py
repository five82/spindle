"""Tests for RipSpec data structure."""

import time
from pathlib import Path
from unittest.mock import MagicMock

import pytest

from spindle.disc.monitor import DiscInfo
from spindle.disc.ripper import Title
from spindle.disc.rip_spec import RipSpec
from spindle.queue.manager import QueueItem, QueueItemStatus


class TestRipSpec:
    """Test RipSpec data structure functionality."""

    def test_basic_creation(self):
        """Test basic RipSpec creation."""
        disc_info = DiscInfo(
            device="/dev/sr0",
            disc_type="BR",
            label="TEST_DISC"
        )
        
        title = Title(
            title_id="1",
            duration=7200,
            size=4500,
            chapters=24,
            tracks=[],
            name="Main Feature"
        )
        
        queue_item = QueueItem(
            disc_title="TEST_DISC",
            status=QueueItemStatus.PENDING
        )
        
        rip_spec = RipSpec(
            disc_info=disc_info,
            titles=[title],
            queue_item=queue_item
        )
        
        assert rip_spec.disc_info == disc_info
        assert rip_spec.titles == [title]
        assert rip_spec.queue_item == queue_item
        assert rip_spec.disc_label == "TEST_DISC"
        assert not rip_spec.has_analysis
        assert not rip_spec.has_media_info
        assert not rip_spec.is_tv_series
        assert not rip_spec.is_multi_disc

    def test_progress_tracking(self):
        """Test progress tracking functionality."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING)
        )
        
        # Test progress update
        rip_spec.update_progress("Testing", 50.0, "Half complete")
        
        assert rip_spec.queue_item.progress_stage == "Testing"
        assert rip_spec.queue_item.progress_percent == 50.0
        assert rip_spec.queue_item.progress_message == "Half complete"

    def test_progress_callback(self):
        """Test progress callback functionality."""
        callback = MagicMock()
        
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING),
            progress_callback=callback
        )
        
        rip_spec.update_progress("Testing", 25.0, "Quarter done")
        
        callback.assert_called_once()
        call_args = callback.call_args[0][0]
        assert call_args["stage"] == "Testing"
        assert call_args["percent"] == 25.0
        assert call_args["message"] == "Quarter done"
        assert "duration" in call_args

    def test_title_selection(self):
        """Test title selection functionality."""
        title1 = Title("1", 3600, 2000, 12, [], "Title 1")
        title2 = Title("2", 7200, 4000, 24, [], "Title 2")
        
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[title1, title2],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING)
        )
        
        rip_spec.select_title(title1)
        assert title1 in rip_spec.selected_titles
        assert len(rip_spec.selected_titles) == 1
        
        # Test duplicate selection
        rip_spec.select_title(title1)
        assert len(rip_spec.selected_titles) == 1

    def test_ripped_file_tracking(self):
        """Test ripped file tracking."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING)
        )
        
        file1 = Path("/tmp/movie1.mkv")
        file2 = Path("/tmp/movie2.mkv")
        
        rip_spec.add_ripped_file(file1)
        assert file1 in rip_spec.ripped_files
        
        rip_spec.add_ripped_file(file2)
        assert len(rip_spec.ripped_files) == 2
        
        # Test duplicate file
        rip_spec.add_ripped_file(file1)
        assert len(rip_spec.ripped_files) == 2

    def test_processing_duration(self):
        """Test processing duration calculation."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING)
        )
        
        # Duration should be close to 0 for new RipSpec
        assert rip_spec.processing_duration < 1.0
        
        # Test with custom start time
        rip_spec.start_time = time.time() - 300  # 5 minutes ago
        assert rip_spec.processing_duration >= 299

    def test_title_candidates(self):
        """Test title candidate extraction."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST_DISC"),
            titles=[
                Title("1", 7200, 4000, 24, [], "Main Feature"),
                Title("2", 1800, 500, 6, [], "Bonus Content")
            ],
            queue_item=QueueItem(disc_title="TEST_DISC", status=QueueItemStatus.PENDING)
        )
        
        candidates = rip_spec.get_title_candidates()
        
        assert "TEST_DISC" in candidates
        assert "Main Feature" in candidates
        assert "Bonus Content" in candidates

    def test_string_representation(self):
        """Test string representation."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST_MOVIE"),
            titles=[Title("1", 7200, 4000, 24, [], "Feature")],
            queue_item=QueueItem(disc_title="TEST_MOVIE", status=QueueItemStatus.PENDING)
        )
        
        str_repr = str(rip_spec)
        assert "TEST_MOVIE" in str_repr
        assert "titles=1" in str_repr
        assert "Movie" in str_repr  # Default assumption


class TestRipSpecProperties:
    """Test RipSpec property methods."""

    def test_has_analysis_property(self):
        """Test has_analysis property."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING)
        )
        
        assert not rip_spec.has_analysis
        
        # Mock analysis result
        rip_spec.analysis_result = MagicMock()
        assert rip_spec.has_analysis

    def test_has_media_info_property(self):
        """Test has_media_info property."""
        rip_spec = RipSpec(
            disc_info=DiscInfo(device="/dev/sr0", disc_type="BR", label="TEST"),
            titles=[],
            queue_item=QueueItem(disc_title="TEST", status=QueueItemStatus.PENDING)
        )
        
        assert not rip_spec.has_media_info
        
        # Mock media info
        rip_spec.media_info = MagicMock()
        assert rip_spec.has_media_info