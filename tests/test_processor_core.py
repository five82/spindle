"""Tests for core processor lifecycle and initialization."""

import asyncio
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.processor import ContinuousProcessor
from conftest_processor import temp_config, mock_dependencies, processor


class TestProcessorCore:
    """Test core processor functionality - lifecycle, start, stop."""

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
        with patch('spindle.processor.detect_disc', return_value=None) as mock_detect, \
             patch('asyncio.get_event_loop') as mock_get_loop:
            mock_loop = Mock()
            mock_task = Mock()
            
            # Mock create_task to avoid creating real coroutines
            def mock_create_task(coro):
                # Close the coroutine immediately to prevent warnings
                if hasattr(coro, 'close'):
                    try:
                        coro.close()
                    except Exception:
                        pass
                return mock_task
                
            mock_loop.create_task.side_effect = mock_create_task
            mock_get_loop.return_value = mock_loop

            processor.start()

            assert processor.is_running is True
            assert processor.disc_monitor is not None
            assert processor.processing_task == mock_task
            
            processor.disc_monitor.start_monitoring.assert_called_once()
            mock_loop.create_task.assert_called_once()
            processor.queue_manager.reset_stuck_processing_items.assert_called_once()
    
    @patch("spindle.processor.asyncio.get_event_loop")
    @patch("spindle.processor.detect_disc")
    @patch("spindle.processor.DiscMonitor")
    def test_start_processor_resets_stuck_items(self, mock_disc_monitor_class, mock_detect_disc, mock_get_loop, processor):
        """Test that starting processor resets stuck processing items."""
        mock_detect_disc.return_value = None
        mock_disc_monitor = Mock()
        mock_disc_monitor_class.return_value = mock_disc_monitor
        
        # Mock stuck items being reset
        processor.queue_manager.reset_stuck_processing_items.return_value = 3
        
        mock_loop = Mock()
        mock_task = Mock()
        
        # Mock create_task to avoid creating real coroutines
        def mock_create_task(coro):
            # Close the coroutine immediately to prevent warnings
            if hasattr(coro, 'close'):
                try:
                    coro.close()
                except Exception:
                    pass
            return mock_task
            
        mock_loop.create_task.side_effect = mock_create_task
        mock_get_loop.return_value = mock_loop
        
        processor.start()
        
        # Verify reset was called
        processor.queue_manager.reset_stuck_processing_items.assert_called_once()

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
        
        # Ensure any existing processing_task coroutine is properly cleaned up
        if processor.processing_task is not None and hasattr(processor.processing_task, 'close'):
            processor.processing_task.close()
        processor.processing_task = Mock()

        processor.stop()

        assert processor.is_running is False
        processor.disc_monitor.stop_monitoring.assert_called_once()
        processor.processing_task.cancel.assert_called_once()

    def test_stop_processor_not_running(self, processor):
        """Test stopping processor when not running."""
        processor.is_running = False
        
        processor.stop()
        
        # Should not try to stop anything
        assert processor.disc_monitor is None
        assert processor.processing_task is None

    def test_get_status(self, processor):
        """Test getting processor status."""
        # Setup a running state with stats
        processor.is_running = True
        processor.disc_monitor = Mock()
        processor.disc_monitor.get_current_disc.return_value = "MOVIE_DISC"
        processor.queue_manager.get_queue_stats.return_value = {
            "pending": 2,
            "completed": 5,
            "failed": 1,
        }
        
        with patch('spindle.processor.detect_disc') as mock_detect:
            mock_detect.return_value = Mock(__str__=lambda x: "MOVIE_DISC")
            
            status = processor.get_status()
            
            assert status["running"] is True
            assert status["current_disc"] == "MOVIE_DISC"
            assert status["queue_stats"]["pending"] == 2
            assert status["queue_stats"]["completed"] == 5
            assert status["queue_stats"]["failed"] == 1

    def test_get_status_no_disc(self, processor):
        """Test getting processor status without disc."""
        processor.is_running = True
        processor.disc_monitor = Mock()
        processor.disc_monitor.get_current_disc.return_value = None
        processor.queue_manager.get_queue_stats.return_value = {}
        
        with patch('spindle.processor.detect_disc') as mock_detect:
            mock_detect.return_value = None
            
            status = processor.get_status()
            
            assert status["running"] is True
        assert status["current_disc"] is None
        assert status["queue_stats"] == {}