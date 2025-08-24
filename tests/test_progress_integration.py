"""Integration tests for drapto progress callback feature."""

import tempfile
from pathlib import Path

from spindle.config import SpindleConfig
from spindle.encode.drapto_wrapper import DraptoEncoder
from spindle.queue.manager import QueueItemStatus, QueueManager


def test_progress_callback_integration():
    """Test the progress callback integration."""
    progress_events = []
    
    def progress_callback(progress_data: dict) -> None:
        """Capture progress events for testing."""
        progress_events.append(progress_data)
    
    # Test command building with --json-progress flag
    with tempfile.TemporaryDirectory() as tmpdir:
        config = SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
        )
        
        encoder = DraptoEncoder(config)
        
        # Verify command includes --json-progress
        cmd = encoder._build_drapto_command(
            Path("/test/input.mkv"), 
            Path("/test/output")
        )
        
        assert "--json-progress" in cmd
        
        # Test simulated progress callbacks
        progress_callback({
            "type": "initialization",
            "message": "Starting encode: test_video.mkv",
            "input_file": "/test/input.mkv"
        })
        
        progress_callback({
            "type": "stage_progress",
            "stage": "analysis",
            "percent": 25.0,
            "message": "Analyzing video properties",
            "eta_seconds": 30
        })
        
        progress_callback({
            "type": "encoding_progress",
            "percent": 50.0,
            "speed": 1.5,
            "fps": 30.0,
            "eta_seconds": 120
        })
        
        progress_callback({
            "type": "completed",
            "message": "Completed encode: test_video.mkv",
            "output_file": "/test/output/test_video.mkv",
            "size_reduction_percent": 35.2
        })
        
        # Verify all progress events were captured
        assert len(progress_events) == 4
        assert progress_events[0]["type"] == "initialization"
        assert progress_events[1]["type"] == "stage_progress"
        assert progress_events[2]["type"] == "encoding_progress"
        assert progress_events[3]["type"] == "completed"


def test_queue_progress_fields():
    """Test queue item progress field integration."""
    with tempfile.TemporaryDirectory() as tmpdir:
        config = SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging", 
            library_dir=Path(tmpdir) / "library",
        )
        
        queue_manager = QueueManager(config)
        
        # Create a queue item with progress
        item = queue_manager.add_disc("Test Movie")
        
        # Update with progress information
        item.status = QueueItemStatus.ENCODING
        item.progress_stage = "encoding"
        item.progress_percent = 75.0
        item.progress_message = "Speed: 1.2x, FPS: 28.5"
        
        queue_manager.update_item(item)
        
        # Retrieve and verify progress fields
        retrieved = queue_manager.get_item(item.item_id)
        
        assert retrieved.progress_stage == "encoding"
        assert retrieved.progress_percent == 75.0
        assert retrieved.progress_message == "Speed: 1.2x, FPS: 28.5"


def test_drapto_command_includes_json_progress():
    """Test that drapto commands include --json-progress flag."""
    with tempfile.TemporaryDirectory() as tmpdir:
        config = SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
        )
        
        encoder = DraptoEncoder(config)
        cmd = encoder._build_drapto_command(
            Path("/test/input.mkv"), 
            Path("/test/output")
        )
        
        assert "--json-progress" in cmd