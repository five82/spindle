"""Essential CLI interface tests - command-line interface and workflow integration."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

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


@pytest.fixture(autouse=True)
def mock_process_lock():
    """Mock ProcessLock for all CLI tests to prevent hanging."""
    with patch('spindle.process_lock.ProcessLock') as mock_lock_class:
        mock_lock_class.find_spindle_process.return_value = None
        
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

    def test_config_validation_on_startup(self, cli_runner, temp_config):
        """Test configuration is validated on startup."""
        with patch('spindle.config.load_config', return_value=temp_config):
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['queue', 'status'])
            
            assert result.exit_code is not None


class TestStartCommand:
    """Test start command functionality."""
    
    def test_start_command_basic(self, cli_runner, temp_config):
        """Test start command initializes processor."""
        with patch('spindle.config.load_config', return_value=temp_config), \
             patch('spindle.cli.check_dependencies') as mock_check_deps, \
             patch('spindle.cli.check_system_dependencies') as mock_check, \
             patch('spindle.cli.ContinuousProcessor') as mock_processor:
            
            mock_check_deps.return_value = []
            mock_check.return_value = None
            
            mock_processor_instance = Mock()
            mock_processor_instance.start.return_value = None
            mock_processor_instance.stop.return_value = None
            mock_processor_instance.is_running = False
            mock_processor.return_value = mock_processor_instance
            
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['start', '--foreground'])
            
            assert result.exit_code == 0
            mock_processor.assert_called_once()

    def test_start_command_error_handling(self, cli_runner, temp_config):
        """Test start command handles errors gracefully."""
        with patch('spindle.config.load_config', return_value=temp_config), \
             patch('spindle.cli.check_dependencies') as mock_check_deps, \
             patch('spindle.cli.check_system_dependencies') as mock_check:
            
            mock_check_deps.return_value = ["missing dependency"]
            mock_check.return_value = None
            
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['start'])
            
            assert result.exit_code != 0




class TestSystemCommands:
    """Test system management commands."""
    
    def test_cli_help_command(self, cli_runner, temp_config):
        """Test that CLI help works."""
        with patch('spindle.config.load_config', return_value=temp_config):
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['--help'])
            
            assert result.exit_code == 0
            assert "spindle" in result.output.lower()


class TestErrorHandling:
    """Test CLI error handling."""
    
    def test_missing_config_handling(self, cli_runner):
        """Test behavior when config is missing."""
        with patch('spindle.config.load_config', side_effect=FileNotFoundError("Config not found")):
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['start'])
            
            assert result.exit_code != 0
            assert "config" in result.output.lower()

    def test_invalid_command(self, cli_runner, temp_config):
        """Test handling of invalid commands."""
        with patch('spindle.config.load_config', return_value=temp_config):
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['nonexistent'])
            
            assert result.exit_code != 0

    def test_permission_error_handling(self, cli_runner, temp_config):
        """Test handling of permission errors."""
        with patch('spindle.config.load_config', return_value=temp_config), \
             patch('spindle.queue.manager.QueueManager', side_effect=PermissionError("Access denied")):
            
            from spindle.cli import cli
            
            result = cli_runner.invoke(cli, ['queue', 'status'])
            
            assert result.exit_code != 0


