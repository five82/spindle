"""Test workflow orchestration."""

import pytest
from unittest.mock import Mock, patch

from spindle.core.orchestrator import SpindleOrchestrator
from spindle.config import SpindleConfig


class TestSpindleOrchestrator:
    """Test SpindleOrchestrator functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(
            log_dir=tmp_path / "logs",
            staging_dir=tmp_path / "staging",
            library_dir=tmp_path / "library",
        )

    @pytest.fixture
    def orchestrator(self, config):
        """Create orchestrator instance."""
        with patch('spindle.core.orchestrator.QueueManager'), \
             patch('spindle.core.orchestrator.DiscHandler'), \
             patch('spindle.core.orchestrator.EncoderComponent'), \
             patch('spindle.core.orchestrator.OrganizerComponent'), \
             patch('spindle.core.orchestrator.NotificationService'):
            return SpindleOrchestrator(config)

    def test_orchestrator_initialization(self, orchestrator, config):
        """Test orchestrator initializes with all components."""
        assert orchestrator.config == config
        assert orchestrator.queue_manager is not None
        assert orchestrator.disc_handler is not None
        assert orchestrator.encoder is not None
        assert orchestrator.organizer is not None
        assert orchestrator.notifier is not None
        assert not orchestrator.is_running

    @patch('spindle.core.orchestrator.DiscMonitor')
    @patch('spindle.core.orchestrator.detect_disc')
    @patch('spindle.core.orchestrator.threading.Thread')
    def test_start_initializes_monitoring(self, mock_thread, mock_detect, mock_monitor, orchestrator):
        """Test start method initializes disc monitoring."""
        mock_detect.return_value = None
        orchestrator.queue_manager.reset_stuck_processing_items.return_value = 0

        thread_instance = Mock()
        mock_thread.return_value = thread_instance

        orchestrator.start()

        assert orchestrator.is_running
        mock_monitor.assert_called_once()
        thread_instance.start.assert_called_once()

    def test_stop_cleans_up_monitoring(self, orchestrator):
        """Test stop method cleans up resources."""
        mock_monitor = Mock()
        mock_thread = Mock()
        mock_thread.is_alive.return_value = True
        orchestrator.disc_monitor = mock_monitor
        orchestrator.processing_thread = mock_thread
        orchestrator.is_running = True

        orchestrator.stop()

        assert not orchestrator.is_running
        mock_monitor.stop_monitoring.assert_called_once()
        mock_thread.join.assert_called_once()

    def test_get_status_returns_comprehensive_info(self, orchestrator):
        """Test get_status returns complete status information."""
        orchestrator.queue_manager.get_queue_stats.return_value = {"pending": 2, "completed": 1}

        with patch('spindle.core.orchestrator.detect_disc') as mock_detect:
            mock_detect.return_value = None
            status = orchestrator.get_status()

        assert "running" in status
        assert "current_disc" in status
        assert "queue_stats" in status
        assert "total_items" in status
        assert status["total_items"] == 3
