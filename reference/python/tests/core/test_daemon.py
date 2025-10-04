"""Test daemon lifecycle management."""

import pytest
from unittest.mock import Mock, patch
from pathlib import Path

from spindle.core.daemon import SpindleDaemon
from spindle.config import SpindleConfig


class TestSpindleDaemon:
    """Test SpindleDaemon functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(
            log_dir=tmp_path / "logs",
            staging_dir=tmp_path / "staging",
            library_dir=tmp_path / "library",
        )

    @pytest.fixture
    def daemon(self, config):
        """Create daemon instance."""
        return SpindleDaemon(config)

    def test_daemon_initialization(self, daemon, config):
        """Test daemon initializes correctly."""
        assert daemon.config == config
        assert daemon.orchestrator is None

    @patch('spindle.core.daemon.daemon')
    @patch('spindle.core.daemon.ProcessManager')
    def test_start_daemon_creates_context(self, mock_process_manager, mock_daemon_module, daemon):
        """Test daemon creates proper daemon context."""
        mock_process_manager.find_spindle_process.return_value = None

        mock_daemon_context = Mock()
        mock_daemon_context.__enter__ = Mock(return_value=mock_daemon_context)
        mock_daemon_context.__exit__ = Mock(return_value=None)
        mock_daemon_module.DaemonContext.return_value = mock_daemon_context

        with patch.object(daemon, '_run_daemon') as mock_run:
            daemon.start_daemon()

        mock_daemon_module.DaemonContext.assert_called_once()
        mock_daemon_context.__enter__.assert_called_once()

    def test_start_systemd_mode(self, daemon):
        """Test systemd mode starts without daemon context."""
        with patch.object(daemon, '_run_daemon') as mock_run:
            daemon.start_systemd_mode()

        mock_run.assert_called_once_with(None)

    def test_stop_cleans_up_resources(self, daemon):
        """Test stop method cleans up orchestrator."""
        mock_orchestrator = Mock()
        daemon.orchestrator = mock_orchestrator

        daemon.stop()

        mock_orchestrator.stop.assert_called_once()