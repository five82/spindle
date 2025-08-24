"""Tests for CLI module."""

import os
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from unittest.mock import MagicMock, Mock, call, patch

import pytest
from click.testing import CliRunner

from spindle.cli import (
    check_uv_requirement,
    cli,
    main,
    process_queue_manual,
    setup_logging,
    start_daemon,
    start_foreground,
)
from spindle.config import SpindleConfig
from spindle.queue.manager import QueueItemStatus


class TestUvRequirement:
    """Test UV requirement checking."""

    @patch("spindle.cli.shutil.which")
    def test_check_uv_requirement_not_found(self, mock_which):
        mock_which.return_value = None
        
        with pytest.raises(SystemExit) as exc_info:
            check_uv_requirement()
        
        assert exc_info.value.code == 1
        mock_which.assert_called_once_with("uv")

    @patch("spindle.cli.shutil.which")
    @patch("spindle.cli.os.environ.get")
    @patch("spindle.cli.console")
    def test_check_uv_requirement_dev_warning(self, mock_console, mock_env_get, mock_which):
        mock_which.return_value = "/usr/bin/uv"
        mock_env_get.return_value = None  # Not running through uv
        
        # Mock __file__ to appear in site-packages
        with patch("spindle.cli.Path") as mock_path:
            mock_path.return_value = Path("/path/to/site-packages/spindle/cli.py")
            check_uv_requirement()  # Should not exit, just show warning
        
        mock_which.assert_called_once_with("uv")

    @patch("spindle.cli.shutil.which")
    @patch("spindle.cli.os.environ.get")
    def test_check_uv_requirement_uv_run(self, mock_env_get, mock_which):
        mock_which.return_value = "/usr/bin/uv" 
        mock_env_get.return_value = "1"  # Running through uv run
        
        check_uv_requirement()  # Should pass silently
        
        mock_which.assert_called_once_with("uv")


class TestSetupLogging:
    """Test logging setup."""

    @patch("spindle.cli.logging.basicConfig")
    def test_setup_logging_default(self, mock_basic_config):
        setup_logging()
        
        mock_basic_config.assert_called_once()
        args, kwargs = mock_basic_config.call_args
        assert kwargs["level"] == 20  # logging.INFO

    @patch("spindle.cli.logging.basicConfig")
    def test_setup_logging_verbose(self, mock_basic_config):
        setup_logging(verbose=True)
        
        mock_basic_config.assert_called_once()
        args, kwargs = mock_basic_config.call_args
        assert kwargs["level"] == 10  # logging.DEBUG


class TestCliCommands:
    """Test CLI commands."""

    def setup_method(self):
        self.runner = CliRunner()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    def test_cli_main_group_success(self, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        result = self.runner.invoke(cli, ["--help"])
        
        assert result.exit_code == 0
        assert "Spindle - Automated disc ripping" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    def test_cli_config_load_error(self, mock_load_config, mock_check_uv):
        mock_load_config.side_effect = Exception("Config error")
        
        result = self.runner.invoke(cli, ["status"])
        
        assert result.exit_code == 1
        assert "Error loading configuration" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.create_sample_config")
    def test_init_config_success(self, mock_create_config, mock_check_uv):
        with tempfile.TemporaryDirectory() as temp_dir:
            config_path = Path(temp_dir) / "config.toml"
            
            result = self.runner.invoke(cli, ["init-config", "--path", str(config_path)])
            
            assert result.exit_code == 0
            assert "Created sample configuration" in result.output
            mock_create_config.assert_called_once_with(config_path)

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.create_sample_config")
    def test_init_config_error(self, mock_create_config, mock_check_uv):
        mock_create_config.side_effect = Exception("Creation failed")
        
        result = self.runner.invoke(cli, ["init-config"])
        
        assert result.exit_code == 1
        assert "Error creating configuration" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.detect_disc")
    @patch("spindle.cli.DraptoEncoder")
    @patch("spindle.cli.LibraryOrganizer")
    @patch("spindle.cli.NtfyNotifier")
    @patch("spindle.cli.QueueManager")
    def test_status_command(
        self,
        mock_queue_manager_class,
        mock_notifier_class,
        mock_organizer_class,
        mock_encoder_class,
        mock_detect_disc,
        mock_load_config,
        mock_check_uv,
    ):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.optical_drive = "/dev/cdrom"
        mock_config.ntfy_topic = "test"
        mock_load_config.return_value = mock_config
        
        # Mock disc detection
        mock_detect_disc.return_value = "Test Disc"
        
        # Mock encoder
        mock_encoder = Mock()
        mock_encoder.check_drapto_availability.return_value = True
        mock_encoder.get_drapto_version.return_value = "1.0.0"
        mock_encoder_class.return_value = mock_encoder
        
        # Mock organizer
        mock_organizer = Mock()
        mock_organizer.verify_plex_connection.return_value = True
        mock_organizer_class.return_value = mock_organizer
        
        # Mock queue manager
        mock_queue_manager = Mock()
        mock_queue_manager.get_queue_stats.return_value = {
            QueueItemStatus.PENDING: 2,
            QueueItemStatus.COMPLETED: 5,
        }
        mock_queue_manager_class.return_value = mock_queue_manager
        
        result = self.runner.invoke(cli, ["status"])
        
        assert result.exit_code == 0
        assert "System Status" in result.output
        assert "Test Disc" in result.output
        assert "Drapto: Available" in result.output
        assert "Plex: Connected" in result.output
        assert "Queue Status" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.QueueManager")
    def test_add_file_command_success(self, mock_queue_manager_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_queue_manager = Mock()
        mock_item = Mock()
        mock_item.__str__ = Mock(return_value="test_item")
        mock_queue_manager.add_file.return_value = mock_item
        mock_queue_manager_class.return_value = mock_queue_manager
        
        with tempfile.NamedTemporaryFile(suffix=".mkv", delete=False) as temp_file:
            temp_path = Path(temp_file.name)
            
            try:
                result = self.runner.invoke(cli, ["add-file", str(temp_path)])
                
                assert result.exit_code == 0
                assert "Added to queue" in result.output
                mock_queue_manager.add_file.assert_called_once_with(temp_path)
            finally:
                temp_path.unlink()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    def test_add_file_command_unsupported_type(self, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.log_dir = Path("/tmp")  # Add missing attribute
        mock_load_config.return_value = mock_config
        
        with tempfile.NamedTemporaryFile(suffix=".txt", delete=False) as temp_file:
            temp_path = Path(temp_file.name)
            
            try:
                result = self.runner.invoke(cli, ["add-file", str(temp_path)])
                
                assert result.exit_code == 1
                assert "Unsupported file type" in result.output
            finally:
                temp_path.unlink()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.QueueManager")
    def test_queue_list_empty(self, mock_queue_manager_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_queue_manager = Mock()
        mock_queue_manager.get_all_items.return_value = []
        mock_queue_manager_class.return_value = mock_queue_manager
        
        result = self.runner.invoke(cli, ["queue-list"])
        
        assert result.exit_code == 0
        assert "Queue is empty" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.QueueManager")
    def test_queue_clear_all_confirm(self, mock_queue_manager_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_queue_manager = Mock()
        mock_queue_manager.clear_all.return_value = 3
        mock_queue_manager_class.return_value = mock_queue_manager
        
        result = self.runner.invoke(cli, ["queue-clear"], input="y\n")
        
        assert result.exit_code == 0
        assert "Cleared 3 items" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.QueueManager")
    def test_queue_clear_completed(self, mock_queue_manager_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_queue_manager = Mock()
        mock_queue_manager.clear_completed.return_value = 2
        mock_queue_manager_class.return_value = mock_queue_manager
        
        result = self.runner.invoke(cli, ["queue-clear", "--completed"])
        
        assert result.exit_code == 0
        assert "Cleared 2 completed items" in result.output

    @patch("spindle.cli.check_uv_requirement") 
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.QueueManager")
    def test_queue_clear_runtime_error(self, mock_queue_manager_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_queue_manager = Mock()
        mock_queue_manager.clear_all.side_effect = RuntimeError("Items in progress")
        mock_queue_manager_class.return_value = mock_queue_manager
        
        result = self.runner.invoke(cli, ["queue-clear"], input="y\n")
        
        assert result.exit_code == 0
        assert "Error: Items in progress" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config") 
    @patch("spindle.cli.NtfyNotifier")
    def test_test_notify_success(self, mock_notifier_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_notifier = Mock()
        mock_notifier.test_notification.return_value = True
        mock_notifier_class.return_value = mock_notifier
        
        result = self.runner.invoke(cli, ["test-notify"])
        
        assert result.exit_code == 0
        assert "Test notification sent successfully" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.NtfyNotifier") 
    def test_test_notify_failure(self, mock_notifier_class, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        mock_notifier = Mock()
        mock_notifier.test_notification.return_value = False
        mock_notifier_class.return_value = mock_notifier
        
        result = self.runner.invoke(cli, ["test-notify"])
        
        assert result.exit_code == 0
        assert "Failed to send test notification" in result.output


class TestStartCommand:
    """Test start command logic."""

    def setup_method(self):
        self.runner = CliRunner()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.start_daemon")
    @patch("spindle.cli.os.getenv")
    def test_start_command_daemon_default(self, mock_getenv, mock_start_daemon, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        mock_getenv.return_value = None  # Not systemd
        
        result = self.runner.invoke(cli, ["start"])
        
        assert result.exit_code == 0
        mock_start_daemon.assert_called_once_with(mock_config)

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.start_foreground")
    @patch("spindle.cli.os.getenv")
    def test_start_command_foreground_flag(self, mock_getenv, mock_start_foreground, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        mock_getenv.return_value = None  # Not systemd
        
        result = self.runner.invoke(cli, ["start", "--foreground"])
        
        assert result.exit_code == 0
        mock_start_foreground.assert_called_once_with(mock_config)

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.start_foreground")
    @patch("spindle.cli.os.getenv")
    def test_start_command_systemd_override(self, mock_getenv, mock_start_foreground, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        mock_getenv.return_value = "test-invocation-id"  # systemd service
        
        result = self.runner.invoke(cli, ["start", "--daemon"])
        
        assert result.exit_code == 0
        mock_start_foreground.assert_called_once_with(mock_config)


class TestStopCommand:
    """Test stop command."""

    def setup_method(self):
        self.runner = CliRunner()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    def test_stop_no_pid_file(self, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.log_dir = Path("/tmp/test")
        mock_load_config.return_value = mock_config
        
        result = self.runner.invoke(cli, ["stop"])
        
        assert result.exit_code == 0
        assert "not running" in result.output

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.os.kill")
    def test_stop_process_not_running(self, mock_kill, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            pid_file = Path(temp_dir) / "spindle.pid"
            pid_file.write_text("12345")
            
            # Simulate process not found
            mock_kill.side_effect = ProcessLookupError()
            
            result = self.runner.invoke(cli, ["stop"])
            
            assert result.exit_code == 0
            assert "was not running" in result.output
            assert not pid_file.exists()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.os.kill")
    @patch("spindle.cli.time.sleep")
    def test_stop_graceful_shutdown(self, mock_sleep, mock_kill, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            pid_file = Path(temp_dir) / "spindle.pid"
            pid_file.write_text("12345")
            
            # First call succeeds (process exists), then SIGTERM, then check process gone
            mock_kill.side_effect = [None, None, ProcessLookupError()]
            
            result = self.runner.invoke(cli, ["stop"])
            
            assert result.exit_code == 0
            assert "Spindle stopped" in result.output
            assert not pid_file.exists()

    @patch("spindle.cli.check_uv_requirement")
    @patch("spindle.cli.load_config")
    @patch("spindle.cli.os.kill")
    @patch("spindle.cli.time.sleep")
    def test_stop_force_kill(self, mock_sleep, mock_kill, mock_load_config, mock_check_uv):
        mock_config = Mock(spec=SpindleConfig)
        mock_load_config.return_value = mock_config
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            pid_file = Path(temp_dir) / "spindle.pid"
            pid_file.write_text("12345")
            
            # Process keeps running through initial checks, then gets killed
            kill_effects = [None, None] + [None] * 10 + [None, ProcessLookupError()]  # Initial check, SIGTERM, 10 checks, SIGKILL, then gone
            mock_kill.side_effect = kill_effects
            
            result = self.runner.invoke(cli, ["stop"])
            
            assert result.exit_code == 0
            assert "force killing" in result.output
            assert "Spindle stopped" in result.output
            
            # Verify SIGKILL was sent
            kill_calls = mock_kill.call_args_list
            assert any(call(12345, signal.SIGKILL) == call_obj for call_obj in kill_calls)


class TestMainFunction:
    """Test main entry point."""

    @patch("spindle.cli.cli")
    def test_main_function(self, mock_cli):
        main()
        mock_cli.assert_called_once()


class TestDaemonManagement:
    """Test daemon management functionality."""

    @patch("daemon.DaemonContext")
    @patch("daemon.pidfile.PIDLockFile") 
    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.os.kill")
    def test_start_daemon_already_running(self, mock_kill, mock_processor_class, mock_pidfile, mock_daemon_context):
        mock_config = Mock(spec=SpindleConfig)
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            mock_config.ensure_directories = Mock()
            
            # Create existing PID file
            pid_file = Path(temp_dir) / "spindle.pid"
            pid_file.write_text("12345")
            
            # Mock process exists (os.kill doesn't raise exception)
            mock_kill.return_value = None
            
            with pytest.raises(SystemExit) as exc_info:
                start_daemon(mock_config)
            
            assert exc_info.value.code == 1
            mock_kill.assert_called_once_with(12345, 0)

    @patch("daemon.DaemonContext")
    @patch("daemon.pidfile.PIDLockFile")
    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.os.kill")
    @patch("spindle.cli.time.sleep")
    def test_start_daemon_stale_pid_file(self, mock_sleep, mock_kill, mock_processor_class, mock_pidfile, mock_daemon_context):
        mock_config = Mock(spec=SpindleConfig)
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            mock_config.ensure_directories = Mock()
            mock_config.status_display_interval = 1
            mock_config.optical_drive = "/dev/cdrom"
            
            # Create stale PID file
            pid_file = Path(temp_dir) / "spindle.pid"
            pid_file.write_text("12345")
            
            # Mock process not found
            mock_kill.side_effect = ProcessLookupError()
            
            # Mock daemon context
            mock_ctx = Mock()
            mock_daemon_context.return_value = mock_ctx
            mock_ctx.__enter__ = Mock(return_value=mock_ctx)
            mock_ctx.__exit__ = Mock(return_value=None)
            
            # Mock processor
            mock_processor = Mock()
            mock_processor.is_running = False  # Exit loop immediately
            mock_processor_class.return_value = mock_processor
            
            start_daemon(mock_config)
            
            # Verify stale PID file was removed
            mock_kill.assert_called_once_with(12345, 0)
            assert not pid_file.exists()

    @patch("daemon.DaemonContext")
    @patch("daemon.pidfile.PIDLockFile")
    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.time.sleep")
    def test_start_daemon_success(self, mock_sleep, mock_processor_class, mock_pidfile, mock_daemon_context):
        mock_config = Mock(spec=SpindleConfig)
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            mock_config.ensure_directories = Mock()
            mock_config.status_display_interval = 1
            mock_config.optical_drive = "/dev/cdrom"
            
            # Mock daemon context
            mock_ctx = Mock()
            mock_daemon_context.return_value = mock_ctx
            mock_ctx.__enter__ = Mock(return_value=mock_ctx)
            mock_ctx.__exit__ = Mock(return_value=None)
            
            # Mock processor
            mock_processor = Mock()
            mock_processor.is_running = False  # Exit loop immediately
            mock_processor_class.return_value = mock_processor
            
            start_daemon(mock_config)
            
            mock_processor.start.assert_called_once()

    @patch("daemon.DaemonContext")
    @patch("daemon.pidfile.PIDLockFile")
    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.time.sleep")
    def test_start_daemon_processor_exception(self, mock_sleep, mock_processor_class, mock_pidfile, mock_daemon_context):
        mock_config = Mock(spec=SpindleConfig)
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            mock_config.ensure_directories = Mock()
            mock_config.status_display_interval = 1
            mock_config.optical_drive = "/dev/cdrom"
            
            # Mock daemon context
            mock_ctx = Mock()
            mock_daemon_context.return_value = mock_ctx
            mock_ctx.__enter__ = Mock(return_value=mock_ctx)
            mock_ctx.__exit__ = Mock(return_value=None)
            
            # Mock processor that raises exception
            mock_processor = Mock()
            mock_processor.start.side_effect = Exception("Test error")
            mock_processor_class.return_value = mock_processor
            
            with pytest.raises(SystemExit) as exc_info:
                start_daemon(mock_config)
            
            assert exc_info.value.code == 1
            mock_processor.stop.assert_called_once()

    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.signal.signal")
    @patch("spindle.cli.time.sleep")
    def test_start_foreground_success(self, mock_sleep, mock_signal, mock_processor_class):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.optical_drive = "/dev/cdrom"
        mock_config.status_display_interval = 1
        
        # Mock processor
        mock_processor = Mock()
        mock_processor.is_running = False  # Exit loop immediately
        mock_processor.get_status.return_value = {"total_items": 0, "current_disc": None}
        mock_processor_class.return_value = mock_processor
        
        start_foreground(mock_config)
        
        mock_processor.start.assert_called_once()
        # Verify signal handlers were set
        assert mock_signal.call_count == 2

    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.signal.signal")
    @patch("spindle.cli.time.sleep")
    def test_start_foreground_with_status(self, mock_sleep, mock_signal, mock_processor_class):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.optical_drive = "/dev/cdrom"
        mock_config.status_display_interval = 1
        
        # Mock processor with queue items - make it run once then exit
        mock_processor = Mock()
        mock_processor.is_running = True  # Will run once
        mock_processor.get_status.return_value = {"total_items": 3, "current_disc": "Test Disc"}
        mock_processor_class.return_value = mock_processor
        
        # Make it exit after first iteration
        def side_effect(*args):
            mock_processor.is_running = False
            
        mock_sleep.side_effect = side_effect
        
        start_foreground(mock_config)
        
        mock_processor.start.assert_called_once()
        mock_processor.get_status.assert_called_once()

    @patch("spindle.cli.ContinuousProcessor")
    @patch("spindle.cli.signal.signal")
    def test_start_foreground_processor_exception(self, mock_signal, mock_processor_class):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.optical_drive = "/dev/cdrom"
        
        # Mock processor that raises exception
        mock_processor = Mock()
        mock_processor.start.side_effect = Exception("Test error")
        mock_processor_class.return_value = mock_processor
        
        with pytest.raises(SystemExit) as exc_info:
            start_foreground(mock_config)
        
        assert exc_info.value.code == 1
        mock_processor.stop.assert_called_once()


class TestSignalHandling:
    """Test signal handling in daemon and foreground modes."""

    def test_daemon_signal_handler_creation(self):
        """Test that signal handlers are properly created in daemon mode."""
        # This tests the signal handler creation logic without full daemon setup
        mock_config = Mock(spec=SpindleConfig)
        
        with tempfile.TemporaryDirectory() as temp_dir:
            mock_config.log_dir = Path(temp_dir)
            mock_config.ensure_directories = Mock()
            mock_config.optical_drive = "/dev/cdrom"
            mock_config.status_display_interval = 1
            
            with patch("daemon.DaemonContext"), \
                 patch("daemon.pidfile.PIDLockFile"), \
                 patch("spindle.cli.ContinuousProcessor") as mock_processor_class, \
                 patch("spindle.cli.signal.signal") as mock_signal, \
                 patch("spindle.cli.time.sleep"):
                
                mock_processor = Mock()
                mock_processor.is_running = False
                mock_processor_class.return_value = mock_processor
                
                # Test signal handler setup
                start_daemon(mock_config)
                
                # Verify signal.signal was called for SIGTERM and SIGINT
                assert mock_signal.call_count == 2

    def test_foreground_signal_handler_creation(self):
        """Test that signal handlers are properly created in foreground mode."""
        mock_config = Mock(spec=SpindleConfig)
        mock_config.optical_drive = "/dev/cdrom"
        mock_config.status_display_interval = 1
        
        with patch("spindle.cli.ContinuousProcessor") as mock_processor_class, \
             patch("spindle.cli.signal.signal") as mock_signal, \
             patch("spindle.cli.time.sleep"):
            
            mock_processor = Mock()
            mock_processor.is_running = False
            mock_processor.get_status.return_value = {"total_items": 0, "current_disc": None}
            mock_processor_class.return_value = mock_processor
            
            start_foreground(mock_config)
            
            # Verify signal handlers were set for SIGINT and SIGTERM
            assert mock_signal.call_count == 2
            
            # Verify the signals are SIGINT and SIGTERM
            signal_args = [call[0][0] for call in mock_signal.call_args_list]
            assert signal.SIGINT in signal_args
            assert signal.SIGTERM in signal_args


@pytest.mark.asyncio
class TestProcessQueueManual:
    """Test manual queue processing."""

    @patch("spindle.cli.QueueManager")
    @patch("spindle.cli.MediaIdentifier")
    @patch("spindle.cli.DraptoEncoder")
    @patch("spindle.cli.LibraryOrganizer")
    @patch("spindle.cli.NtfyNotifier")
    @patch("spindle.cli.time.time")
    async def test_process_queue_manual_no_items(
        self, mock_time, mock_notifier_class, mock_organizer_class,
        mock_encoder_class, mock_identifier_class, mock_queue_manager_class
    ):
        mock_config = Mock(spec=SpindleConfig)
        
        mock_queue_manager = Mock()
        mock_queue_manager.get_pending_items.return_value = []
        mock_queue_manager_class.return_value = mock_queue_manager
        
        await process_queue_manual(mock_config)
        
        mock_queue_manager.get_pending_items.assert_called_once()

    @patch("spindle.cli.QueueManager")
    @patch("spindle.cli.MediaIdentifier")
    @patch("spindle.cli.DraptoEncoder")
    @patch("spindle.cli.LibraryOrganizer")
    @patch("spindle.cli.NtfyNotifier")
    @patch("spindle.cli.time.time")
    @patch("spindle.cli.time.strftime")
    async def test_process_queue_manual_with_items(
        self, mock_strftime, mock_time, mock_notifier_class, mock_organizer_class,
        mock_encoder_class, mock_identifier_class, mock_queue_manager_class
    ):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.staging_dir = Path("/staging")
        
        # Mock queue item
        mock_item = Mock()
        mock_item.status = QueueItemStatus.ENCODED
        mock_item.encoded_file = Path("/encoded/file.mkv")
        mock_item.media_info = Mock()
        mock_item.media_info.__str__ = Mock(return_value="Test Movie")
        mock_item.media_info.media_type = "movie"
        
        mock_queue_manager = Mock()
        mock_queue_manager.get_pending_items.return_value = [mock_item]
        mock_queue_manager_class.return_value = mock_queue_manager
        
        mock_organizer = Mock()
        mock_organizer.add_to_plex.return_value = True
        mock_organizer_class.return_value = mock_organizer
        
        mock_notifier = Mock()
        mock_notifier_class.return_value = mock_notifier
        
        mock_time.side_effect = [0, 3600]  # 1 hour duration
        mock_strftime.return_value = "01:00:00"
        
        await process_queue_manual(mock_config)
        
        assert mock_item.status == QueueItemStatus.COMPLETED
        mock_organizer.add_to_plex.assert_called_once_with(mock_item.encoded_file, mock_item.media_info)
        mock_notifier.notify_queue_completed.assert_called_once_with(1, 0, "01:00:00")

    @patch("spindle.cli.QueueManager")
    @patch("spindle.cli.MediaIdentifier")
    @patch("spindle.cli.DraptoEncoder")
    @patch("spindle.cli.LibraryOrganizer")
    @patch("spindle.cli.NtfyNotifier")
    @patch("spindle.cli.time.time")
    @patch("spindle.cli.time.strftime")
    async def test_process_queue_manual_identification_needed(
        self, mock_strftime, mock_time, mock_notifier_class, mock_organizer_class,
        mock_encoder_class, mock_identifier_class, mock_queue_manager_class
    ):
        mock_config = Mock(spec=SpindleConfig)
        
        # Mock queue item needing identification
        mock_item = Mock()
        mock_item.status = QueueItemStatus.RIPPED
        mock_item.media_info = None
        mock_item.ripped_file = Path("/ripped/file.mkv")
        
        mock_queue_manager = Mock()
        mock_queue_manager.get_pending_items.return_value = [mock_item]
        mock_queue_manager_class.return_value = mock_queue_manager
        
        # Mock identifier - make it async
        mock_identifier = Mock()
        mock_media_info = Mock()
        mock_media_info.__str__ = Mock(return_value="Test Movie")
        
        # Create a proper async mock
        async def mock_identify_media(file_path):
            return mock_media_info
            
        mock_identifier.identify_media = mock_identify_media
        mock_identifier_class.return_value = mock_identifier
        
        mock_notifier = Mock()
        mock_notifier_class.return_value = mock_notifier
        
        mock_time.side_effect = [0, 100]
        mock_strftime.return_value = "00:01:40"
        
        await process_queue_manual(mock_config)
        
        assert mock_item.status == QueueItemStatus.IDENTIFIED
        assert mock_item.media_info == mock_media_info

    @patch("spindle.cli.QueueManager")
    @patch("spindle.cli.MediaIdentifier")
    @patch("spindle.cli.DraptoEncoder")
    @patch("spindle.cli.LibraryOrganizer")
    @patch("spindle.cli.NtfyNotifier")
    @patch("spindle.cli.time.time")
    @patch("spindle.cli.time.strftime")
    async def test_process_queue_manual_encoding_needed(
        self, mock_strftime, mock_time, mock_notifier_class, mock_organizer_class,
        mock_encoder_class, mock_identifier_class, mock_queue_manager_class
    ):
        mock_config = Mock(spec=SpindleConfig)
        mock_config.staging_dir = Path("/staging")
        
        # Mock queue item needing encoding
        mock_item = Mock()
        mock_item.status = QueueItemStatus.IDENTIFIED
        mock_item.encoded_file = None
        mock_item.ripped_file = Path("/ripped/file.mkv")
        mock_item.media_info = Mock()
        mock_item.media_info.__str__ = Mock(return_value="Test Movie")
        
        mock_queue_manager = Mock()
        mock_queue_manager.get_pending_items.return_value = [mock_item]
        mock_queue_manager_class.return_value = mock_queue_manager
        
        # Mock encoder
        mock_encoder = Mock()
        mock_result = Mock()
        mock_result.success = True
        mock_result.output_file = Path("/encoded/file.mkv")
        mock_result.size_reduction_percent = 50
        mock_encoder.encode_file.return_value = mock_result
        mock_encoder_class.return_value = mock_encoder
        
        mock_notifier = Mock()
        mock_notifier_class.return_value = mock_notifier
        
        mock_time.side_effect = [0, 200]
        mock_strftime.return_value = "00:03:20"
        
        await process_queue_manual(mock_config)
        
        assert mock_item.status == QueueItemStatus.ENCODED
        assert mock_item.encoded_file == mock_result.output_file
        mock_encoder.encode_file.assert_called_once_with(
            mock_item.ripped_file, mock_config.staging_dir / "encoded"
        )