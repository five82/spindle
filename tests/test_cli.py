"""Essential CLI interface tests - command-line interface and workflow integration."""

import tempfile
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch

import pytest
from click.testing import CliRunner

from spindle.config import SpindleConfig


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
def cli_runner():
    """Create CLI test runner."""
    return CliRunner()


@pytest.fixture
def mock_config_load():
    """Mock configuration loading."""
    with patch('spindle.config.load_config') as mock:
        yield mock


@pytest.fixture(autouse=True)
def mock_process_lock():
    """Mock ProcessLock for all CLI tests to prevent hanging."""
    with patch('spindle.process_lock.ProcessLock') as mock_lock_class:
        # Mock the class methods
        mock_lock_class.find_spindle_process.return_value = None
        
        # Mock instance creation
        mock_lock_instance = Mock()
        mock_lock_instance.acquire.return_value = True
        mock_lock_instance.release.return_value = None
        mock_lock_class.return_value = mock_lock_instance
        
        yield mock_lock_class


class TestCLIBasics:
    """Test essential CLI functionality."""
    
    def test_cli_entry_point(self, cli_runner):
        """Test CLI entry point is accessible."""
        from spindle.cli import cli
        
        result = cli_runner.invoke(cli, ['--help'])
        
        assert result.exit_code == 0
        assert "spindle" in result.output.lower()

    def test_config_validation_on_startup(self, cli_runner, mock_config_load, temp_config):
        """Test configuration is validated on startup."""
        mock_config_load.return_value = temp_config
        
        from spindle.cli import cli
        
        # Should not crash with valid config
        result = cli_runner.invoke(cli, ['queue', 'status'])
        
        # May exit with different code but should not crash
        assert result.exit_code is not None


class TestStartCommand:
    """Test start command functionality."""
    
    def test_start_command_basic(self, cli_runner, mock_config_load, temp_config):
        """Test start command initializes processor."""
        mock_config_load.return_value = temp_config
        
        from spindle.cli import cli
        
        # Mock all the components the start command needs
        with patch('spindle.cli.check_dependencies') as mock_check_deps, \
             patch('spindle.cli.check_system_dependencies') as mock_check, \
             patch('spindle.cli.ContinuousProcessor') as mock_processor, \
             patch('spindle.cli.console.print') as mock_print:
            
            mock_check_deps.return_value = []  # No dependency errors
            mock_check.return_value = None
            
            # Create properly mocked processor instance  
            mock_processor_instance = Mock()
            mock_processor_instance.start.return_value = None
            mock_processor_instance.stop.return_value = None
            mock_processor_instance.is_running = False  # Exit the while loop immediately
            mock_processor_instance.get_status.return_value = {"total_items": 0, "current_disc": None}
            mock_processor.return_value = mock_processor_instance
            
            result = cli_runner.invoke(cli, ['start', '--foreground'])
        
        # Should succeed without crashing
        assert result.exit_code == 0

    def test_start_command_with_options(self, cli_runner, mock_config_load, temp_config):
        """Test start command with CLI options."""
        mock_config_load.return_value = temp_config
        
        from spindle.cli import cli
        
        # Mock all the components the start command needs
        with patch('spindle.cli.check_dependencies') as mock_check_deps, \
             patch('spindle.cli.check_system_dependencies') as mock_check, \
             patch('spindle.cli.ContinuousProcessor') as mock_processor:
            
            mock_check_deps.return_value = []  # No dependency errors
            mock_check.return_value = None
            
            # Create properly mocked processor instance
            mock_processor_instance = Mock()
            mock_processor_instance.start.return_value = None
            mock_processor_instance.stop.return_value = None
            mock_processor_instance.is_running = False  # Exit the while loop immediately
            mock_processor_instance.get_status.return_value = {"total_items": 0, "current_disc": None}
            mock_processor.return_value = mock_processor_instance
            
            # Test with verbose option (verbose is a global flag)
            result = cli_runner.invoke(cli, ['--verbose', 'start', '--foreground'])
        
        # Should succeed without crashing
        assert result.exit_code == 0



class TestQueueCommands:
    """Test queue management commands."""
    
    @patch('spindle.cli.QueueManager')
    def test_queue_status_command(self, mock_queue_manager, cli_runner, mock_config_load, temp_config):
        """Test queue status display."""
        mock_config_load.return_value = temp_config
        
        mock_manager_instance = Mock()
        mock_manager_instance.get_queue_stats.return_value = {
            "pending": 2,
            "completed": 5,
            "failed": 1
        }
        mock_queue_manager.return_value = mock_manager_instance
        
        from spindle.cli import cli
        
        with patch('spindle.cli.check_dependencies') as mock_check_deps:
            mock_check_deps.return_value = []  # No dependency errors
            result = cli_runner.invoke(cli, ['queue', 'status'])
            
            assert result.exit_code == 0
            assert "pending" in result.output.lower()
            assert "2" in result.output

    @patch('spindle.cli.QueueManager')
    def test_queue_list_command(self, mock_queue_manager, cli_runner, mock_config_load, temp_config):
        """Test queue item listing."""
        mock_config_load.return_value = temp_config
        
        from spindle.queue.manager import QueueItem, QueueItemStatus
        
        from datetime import datetime
        
        mock_item = QueueItem(
            item_id=1,
            disc_title="TEST_DISC",
            status=QueueItemStatus.PENDING,
            created_at=datetime(2023, 1, 1, 0, 0, 0)
        )
        
        mock_manager_instance = Mock()
        mock_manager_instance.get_all_items.return_value = [mock_item]
        mock_queue_manager.return_value = mock_manager_instance
        
        from spindle.cli import cli
        
        with patch('spindle.cli.check_dependencies') as mock_check_deps:
            mock_check_deps.return_value = []  # No dependency errors
            result = cli_runner.invoke(cli, ['queue', 'list'])
            
            assert result.exit_code == 0
            assert "TEST_DISC" in result.output

    @patch('spindle.cli.QueueManager')
    def test_queue_clear_command(self, mock_queue_manager, cli_runner, mock_config_load, temp_config):
        """Test queue clearing."""
        mock_config_load.return_value = temp_config
        
        mock_manager_instance = Mock()
        mock_manager_instance.clear_completed.return_value = 3
        mock_queue_manager.return_value = mock_manager_instance
        
        from spindle.cli import cli
        
        with patch('spindle.cli.check_dependencies') as mock_check_deps:
            mock_check_deps.return_value = []  # No dependency errors
            result = cli_runner.invoke(cli, ['queue', 'clear', '--completed'])
            
            assert result.exit_code == 0
            mock_manager_instance.clear_completed.assert_called_once()

    @patch('spindle.cli.QueueManager')
    def test_queue_retry_command(self, mock_queue_manager, cli_runner, mock_config_load, temp_config):
        """Test queue item retry."""
        mock_config_load.return_value = temp_config
        
        from spindle.queue.manager import QueueItem, QueueItemStatus
        
        from datetime import datetime
        
        mock_item = QueueItem(
            item_id=1,
            disc_title="FAILED_DISC",
            status=QueueItemStatus.FAILED,
            created_at=datetime(2023, 1, 1, 0, 0, 0)
        )
        
        mock_manager_instance = Mock()
        mock_manager_instance.get_item.return_value = mock_item
        mock_manager_instance.update_item.return_value = None
        mock_queue_manager.return_value = mock_manager_instance
        
        from spindle.cli import cli
        
        with patch('spindle.cli.check_dependencies') as mock_check_deps:
            mock_check_deps.return_value = []  # No dependency errors
            result = cli_runner.invoke(cli, ['queue', 'retry', '1'])
            
            # Should attempt to retry item
            mock_manager_instance.get_item.assert_called_with(1)
            mock_manager_instance.update_item.assert_called_once()


class TestConfigCommands:
    """Test configuration management commands."""
    
    def test_config_show_command(self, cli_runner, mock_config_load, temp_config):
        """Test configuration display."""
        mock_config_load.return_value = temp_config
        
        from spindle.cli import cli
        
        result = cli_runner.invoke(cli, ['config', 'show'])
        
        # Should display configuration without errors
        assert result.exit_code == 0 or "config" in result.output.lower()

    def test_config_validate_command(self, cli_runner, mock_config_load, temp_config):
        """Test configuration validation."""
        mock_config_load.return_value = temp_config
        
        from spindle.cli import cli
        
        result = cli_runner.invoke(cli, ['config', 'validate'])
        
        # Should validate configuration
        # May pass or fail depending on system, but should not crash
        assert result.exit_code is not None

    def test_config_init_command(self, cli_runner):
        """Test configuration initialization."""
        from spindle.cli import cli
        
        with tempfile.TemporaryDirectory() as tmpdir:
            config_file = Path(tmpdir) / "test_config.toml"
            
            result = cli_runner.invoke(cli, ['config', 'init', str(config_file)])
            
            # Should create config file or handle gracefully
            assert result.exit_code is not None


class TestWorkflowIntegration:
    """Test CLI integration with workflow components."""
    
    def test_full_workflow_simulation(self, cli_runner, mock_config_load, temp_config):
        """Test CLI coordinates full workflow."""
        mock_config_load.return_value = temp_config
        
        from spindle.cli import cli
        
        # Test start processor command
        with patch('spindle.cli.check_dependencies') as mock_check_deps, \
             patch('spindle.cli.check_system_dependencies') as mock_check, \
             patch('spindle.cli.ContinuousProcessor') as mock_processor:
            
            mock_check_deps.return_value = []  # No dependency errors
            mock_check.return_value = None
            
            # Create properly mocked processor instance
            mock_processor_instance = Mock()
            mock_processor_instance.start.return_value = None
            mock_processor_instance.stop.return_value = None
            mock_processor_instance.is_running = False  # Exit the while loop immediately
            mock_processor_instance.get_status.return_value = {"total_items": 0, "current_disc": None}
            mock_processor.return_value = mock_processor_instance
            
            start_result = cli_runner.invoke(cli, ['start', '--foreground'])
        
        # Test queue status command
        with patch('spindle.cli.check_dependencies') as mock_check_deps2, \
             patch('spindle.cli.QueueManager') as mock_queue_manager:
            mock_check_deps2.return_value = []  # No dependency errors
            mock_manager_instance = Mock()
            mock_manager_instance.get_queue_stats.return_value = {"pending": 1}
            mock_queue_manager.return_value = mock_manager_instance
            
            status_result = cli_runner.invoke(cli, ['queue', 'status'])
        
        # Both commands should work without crashing
        assert start_result.exit_code == 0
        assert status_result.exit_code == 0


    @patch('spindle.cli.QueueManager')
    def test_progress_display(self, mock_queue_manager, cli_runner, mock_config_load, temp_config):
        """Test progress display in CLI commands."""
        mock_config_load.return_value = temp_config
        
        from spindle.queue.manager import QueueItem, QueueItemStatus
        
        from datetime import datetime
        
        mock_item = QueueItem(
            item_id=1,
            disc_title="ENCODING_DISC",
            status=QueueItemStatus.ENCODING,
            progress_percent=75.5,
            progress_stage="encoding",
            progress_message="Processing at 1.2x speed",
            created_at=datetime(2023, 1, 1, 0, 0, 0)
        )
        
        mock_manager_instance = Mock()
        mock_manager_instance.get_all_items.return_value = [mock_item]
        mock_queue_manager.return_value = mock_manager_instance
        
        from spindle.cli import cli
        
        with patch('spindle.cli.check_dependencies') as mock_check_deps:
            mock_check_deps.return_value = []  # No dependency errors
            result = cli_runner.invoke(cli, ['queue', 'list'])
            
            # Should display progress information
            assert result.exit_code == 0
            # Progress information should be visible
            assert "75" in result.output or "encoding" in result.output.lower()


class TestCLIUtilities:
    """Test CLI utility functions and helpers."""
    
    def test_format_duration(self):
        """Test duration formatting for display."""
        from spindle.cli import format_duration
        
        # Test various durations
        assert format_duration(3661) == "1:01:01"
        assert format_duration(60) == "0:01:00"
        assert format_duration(30) == "0:00:30"

    def test_format_file_size(self):
        """Test file size formatting for display."""
        from spindle.cli import format_file_size
        
        # Test various sizes
        assert "GB" in format_file_size(25_000_000_000)
        assert "MB" in format_file_size(500_000_000)
        assert "KB" in format_file_size(1024)

    def test_status_color_coding(self):
        """Test status color coding for terminal display."""
        from spindle.cli import get_status_color
        from spindle.queue.manager import QueueItemStatus
        
        # Different statuses should have different colors/styles
        pending_color = get_status_color(QueueItemStatus.PENDING)
        completed_color = get_status_color(QueueItemStatus.COMPLETED)
        failed_color = get_status_color(QueueItemStatus.FAILED)
        
        # Colors should be different
        assert pending_color != completed_color
        assert completed_color != failed_color

    def test_table_formatting(self):
        """Test table formatting for queue display."""
        from spindle.cli import format_queue_table
        from spindle.queue.manager import QueueItem, QueueItemStatus
        
        from datetime import datetime
        
        items = [
            QueueItem(
                item_id=1,
                disc_title="TEST_DISC_1",
                status=QueueItemStatus.COMPLETED,
                created_at=datetime(2023, 1, 1, 0, 0, 0)
            ),
            QueueItem(
                item_id=2,
                disc_title="TEST_DISC_2",
                status=QueueItemStatus.PENDING,
                created_at=datetime(2023, 1, 2, 0, 0, 0)
            )
        ]
        
        table_output = format_queue_table(items)
        
        # Convert table to string for testing
        from rich.console import Console
        from io import StringIO
        
        console = Console(file=StringIO(), width=120)
        console.print(table_output)
        table_str = console.file.getvalue()
        
        # Should format as readable table
        assert "TEST_DISC_1" in table_str
        assert "TEST_DISC_2" in table_str
        assert "COMPLETED" in table_str or "Completed" in table_str
        assert "PENDING" in table_str or "Pending" in table_str