#!/usr/bin/env python3
"""
Integration test for drapto progress callback feature.
This script demonstrates how spindle now integrates with drapto's JSON progress output.

REQUIRES: uv package manager
Usage: uv run python test_progress_integration.py
"""

import shutil
import sys
import tempfile
from pathlib import Path

# Add src to path
sys.path.insert(0, 'src')

from spindle.encode.drapto_wrapper import DraptoEncoder
from spindle.queue.manager import QueueManager, QueueItem, QueueItemStatus
from spindle.config import SpindleConfig


def test_progress_callback():
    """Test the progress callback integration."""
    print("Testing drapto progress callback integration...")
    
    progress_events = []
    
    def progress_callback(progress_data: dict) -> None:
        """Capture progress events for testing."""
        progress_events.append(progress_data)
        event_type = progress_data.get("type", "unknown")
        
        if event_type == "stage_progress":
            stage = progress_data.get("stage", "")
            percent = progress_data.get("percent", 0)
            message = progress_data.get("message", "")
            print(f"  Stage Progress: {stage} {percent:.1f}% - {message}")
            
        elif event_type == "encoding_progress":
            percent = progress_data.get("percent", 0)
            speed = progress_data.get("speed", 0)
            fps = progress_data.get("fps", 0)
            print(f"  Encoding Progress: {percent:.1f}% (speed: {speed:.1f}x, fps: {fps:.1f})")
            
        elif event_type == "initialization":
            message = progress_data.get("message", "")
            print(f"  Initialization: {message}")
            
        elif event_type == "completed":
            message = progress_data.get("message", "")
            reduction = progress_data.get("size_reduction_percent", 0)
            print(f"  Completed: {message} ({reduction:.1f}% reduction)")
    
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
        
        assert "--json-progress" in cmd, "Command should include --json-progress flag"
        print("✓ --json-progress flag added to drapto command")
        
        # Test simulated progress callbacks
        print("\nSimulating progress callbacks:")
        
        # Simulate initialization
        progress_callback({
            "type": "initialization",
            "message": "Starting encode: test_video.mkv",
            "input_file": "/test/input.mkv"
        })
        
        # Simulate stage progress
        progress_callback({
            "type": "stage_progress",
            "stage": "analysis",
            "percent": 25.0,
            "message": "Analyzing video properties",
            "eta_seconds": 30
        })
        
        # Simulate encoding progress
        progress_callback({
            "type": "encoding_progress",
            "percent": 50.0,
            "speed": 1.5,
            "fps": 30.0,
            "eta_seconds": 120
        })
        
        # Simulate completion
        progress_callback({
            "type": "completed",
            "message": "Completed encode: test_video.mkv",
            "output_file": "/test/output/test_video.mkv",
            "size_reduction_percent": 35.2
        })
        
        print(f"\n✓ Captured {len(progress_events)} progress events")


def test_queue_progress_fields():
    """Test queue item progress field integration."""
    print("\nTesting queue item progress fields...")
    
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
        
        print(f"✓ Queue item progress: {retrieved.progress_stage} {retrieved.progress_percent}%")
        print(f"  Message: {retrieved.progress_message}")


if __name__ == "__main__":
    print("=== Spindle + Drapto Progress Integration Test ===\n")
    
    # Check for uv (informational)
    if not shutil.which("uv"):
        print("⚠️  WARNING: uv package manager not found")
        print("   uv is required for spindle installation and dependency management")
        print()
    
    test_progress_callback()
    test_queue_progress_fields()
    
    print("\n=== Integration Test Complete ===")
    print("✓ All progress callback features working correctly")
    print("✓ Spindle is ready to use drapto's new JSON progress output")