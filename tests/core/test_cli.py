"""Essential CLI tests."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest
from click.testing import CliRunner

from spindle.cli import cli
from spindle.config import SpindleConfig


@pytest.fixture
def temp_dir():
    """Create temporary directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield Path(tmpdir)


@pytest.fixture
def test_config(temp_dir):
    """Create test configuration."""
    return SpindleConfig(
        staging_dir=temp_dir / "staging",
        library_dir=temp_dir / "library", 
        log_dir=temp_dir / "logs",
        review_dir=temp_dir / "review",
        tmdb_api_key="test_key",
        plex_url="http://localhost:32400",
        plex_token="test_token"
    )


class TestCLIBasics:
    """Test essential CLI functionality."""
    
    def test_cli_help(self):
        """Test main CLI help displays correctly."""
        runner = CliRunner()
        result = runner.invoke(cli, ["--help"])
        
        assert result.exit_code == 0
        assert "Spindle - Automated disc ripping" in result.output
        assert "start" in result.output
        assert "stop" in result.output
        assert "show" in result.output
        assert "status" in result.output


class TestStartCommand:
    """Test start command (daemon-only operation)."""
    
    def test_start_help(self):
        """Test start command help text."""
        runner = CliRunner()
        result = runner.invoke(cli, ["start", "--help"])
        
        assert result.exit_code == 0
        assert "Start continuous processing daemon" in result.output
        assert "--systemd" in result.output
        # Ensure no foreground mode references
        assert "--foreground" not in result.output
        assert "--daemon" not in result.output
    
    @patch('spindle.cli.check_system_dependencies')
    @patch('spindle.cli.SpindleDaemon')
    def test_start_daemon_mode(self, mock_spindle_daemon, mock_check_deps, test_config):
        """Test start command creates SpindleDaemon and runs in daemon mode by default."""
        runner = CliRunner()

        # Mock daemon instance
        mock_daemon_instance = Mock()
        mock_spindle_daemon.return_value = mock_daemon_instance

        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["start"])

        mock_check_deps.assert_called_once_with(validate_required=True)
        mock_spindle_daemon.assert_called_once_with(test_config)
        mock_daemon_instance.start_daemon.assert_called_once()
        mock_daemon_instance.start_systemd_mode.assert_not_called()
        assert result.exit_code == 0

    @patch('spindle.cli.check_system_dependencies')
    @patch('spindle.cli.SpindleDaemon')
    def test_start_systemd_mode(self, mock_spindle_daemon, mock_check_deps, test_config):
        """Test start command with --systemd flag calls start_systemd_mode."""
        runner = CliRunner()

        # Mock daemon instance
        mock_daemon_instance = Mock()
        mock_spindle_daemon.return_value = mock_daemon_instance

        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["start", "--systemd"])

        mock_check_deps.assert_called_once_with(validate_required=True)
        mock_spindle_daemon.assert_called_once_with(test_config)
        mock_daemon_instance.start_systemd_mode.assert_called_once()
        mock_daemon_instance.start_daemon.assert_not_called()
        assert result.exit_code == 0


class TestShowCommand:
    """Test show command (log monitoring)."""
    
    def test_show_help(self):
        """Test show command help text."""
        runner = CliRunner()
        result = runner.invoke(cli, ["show", "--help"])
        
        assert result.exit_code == 0
        assert "Show Spindle daemon log output with colors" in result.output
        assert "--follow" in result.output
        assert "--lines" in result.output
    
    def test_show_missing_log_file(self, test_config):
        """Test show command behavior when log file doesn't exist."""
        runner = CliRunner()
        
        # Ensure log file doesn't exist by not creating the directory
        with patch('spindle.cli.load_config', return_value=test_config):
            with patch('pathlib.Path.exists', return_value=False):
                with patch('spindle.cli.sys.exit') as mock_exit:
                    runner.invoke(cli, ["show"])
                    # Should call sys.exit(1) for missing log file
                    assert any(call.args == (1,) for call in mock_exit.call_args_list)
    
    @patch('subprocess.Popen')
    def test_show_default_behavior(self, mock_popen, test_config):
        """Test show command default behavior (last 10 lines)."""
        # Create log file
        test_config.log_dir.mkdir(parents=True)
        log_file = test_config.log_dir / "spindle.log"
        log_file.write_text("test log content")
        
        # Mock subprocess
        mock_proc = Mock()
        mock_proc.stdout = ["test log line"]
        mock_proc.returncode = 0
        mock_popen.return_value.__enter__.return_value = mock_proc
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["show"])
            
        assert result.exit_code == 0
        mock_popen.assert_called_once()
        args = mock_popen.call_args[0][0]
        assert args == ["tail", "-n", "10", str(log_file)]
    
    @patch('subprocess.Popen')
    def test_show_follow_mode(self, mock_popen, test_config):
        """Test show command with --follow flag."""
        # Create log file
        test_config.log_dir.mkdir(parents=True)
        log_file = test_config.log_dir / "spindle.log"
        log_file.write_text("test log content")
        
        # Mock subprocess
        mock_proc = Mock()
        mock_proc.stdout = ["test log line"]
        mock_proc.returncode = 0
        mock_popen.return_value.__enter__.return_value = mock_proc
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["show", "--follow"])
            
        assert result.exit_code == 0
        mock_popen.assert_called_once()
        args = mock_popen.call_args[0][0]
        assert args == ["tail", "-f", str(log_file)]
    
    @patch('subprocess.Popen')
    def test_show_custom_lines(self, mock_popen, test_config):
        """Test show command with custom line count."""
        # Create log file
        test_config.log_dir.mkdir(parents=True)
        log_file = test_config.log_dir / "spindle.log"
        log_file.write_text("test log content")
        
        # Mock subprocess
        mock_proc = Mock()
        mock_proc.stdout = ["test log line"]
        mock_proc.returncode = 0
        mock_popen.return_value.__enter__.return_value = mock_proc
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["show", "--lines", "50"])
            
        assert result.exit_code == 0
        mock_popen.assert_called_once()
        args = mock_popen.call_args[0][0]
        assert args == ["tail", "-n", "50", str(log_file)]
    
    def test_show_tail_command_not_found(self, test_config):
        """Test show command when tail command is not available."""
        # Create log file
        test_config.log_dir.mkdir(parents=True)
        log_file = test_config.log_dir / "spindle.log"
        log_file.write_text("test log content")
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            with patch('subprocess.Popen', side_effect=FileNotFoundError):
                result = runner.invoke(cli, ["show"])
                
        assert result.exit_code == 1
        assert "tail command not found" in result.output


class TestStopCommand:
    """Test stop command functionality."""
    
    def test_stop_help(self):
        """Test stop command help text."""
        runner = CliRunner()
        result = runner.invoke(cli, ["stop", "--help"])
        
        assert result.exit_code == 0
        assert "Stop running Spindle process" in result.output
    
    @patch('spindle.cli.ProcessLock.find_spindle_process')
    def test_stop_no_running_process(self, mock_find_process, test_config):
        """Test stop command when no process is running."""
        mock_find_process.return_value = None
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["stop"])
            
        assert result.exit_code == 0
        assert "Spindle is not running" in result.output
    
    @patch('spindle.cli.ProcessLock.find_spindle_process')
    @patch('spindle.cli.ProcessLock.stop_process')
    def test_stop_running_process(self, mock_stop_process, mock_find_process, test_config):
        """Test stop command with running process."""
        mock_find_process.return_value = (1234, "daemon")
        mock_stop_process.return_value = True
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["stop"])
            
        assert result.exit_code == 0
        assert "Spindle stopped" in result.output
        mock_stop_process.assert_called_once_with(1234)


class TestStatusCommand:
    """Test status command functionality."""
    
    def test_status_help(self):
        """Test status command help text."""
        runner = CliRunner()
        result = runner.invoke(cli, ["status", "--help"])
        
        assert result.exit_code == 0
        assert "Show system status and queue information" in result.output
    
    @patch('spindle.cli.QueueManager')
    @patch('spindle.cli.ProcessLock.find_spindle_process')
    def test_status_display(self, mock_find_process, mock_queue_manager, test_config):
        """Test status command displays system information."""
        mock_find_process.return_value = (1234, "daemon")
        
        # Mock queue manager
        mock_queue = Mock()
        mock_queue.get_queue_stats.return_value = {
            "PENDING": 1,
            "RIPPING": 1,
            "COMPLETED": 5
        }
        mock_queue_manager.return_value = mock_queue
        
        runner = CliRunner()
        with patch('spindle.cli.load_config', return_value=test_config):
            result = runner.invoke(cli, ["status"])
            
        assert result.exit_code == 0
        assert "System Status" in result.output
        assert "Queue Status" in result.output


class TestCLIIntegration:
    """Test CLI integration scenarios."""
    
    def test_daemon_only_operation(self):
        """Test that CLI only supports daemon-only operation (no foreground mode)."""
        runner = CliRunner()
        
        # Test that foreground options don't exist
        result = runner.invoke(cli, ["start", "--foreground"])
        assert result.exit_code != 0  # Should fail - option doesn't exist
        
        result = runner.invoke(cli, ["start", "--daemon"])
        assert result.exit_code != 0  # Should fail - option doesn't exist
    
    @patch('spindle.cli.load_config')
    def test_config_loading_integration(self, mock_load_config):
        """Test that commands properly load configuration."""
        test_config = SpindleConfig()
        mock_load_config.return_value = test_config
        
        runner = CliRunner()
        
        # Test that status command loads config
        with patch('spindle.cli.QueueManager') as mock_queue_manager:
            with patch('spindle.cli.ProcessLock.find_spindle_process', return_value=None):
                # Mock queue manager properly
                mock_queue = Mock()
                mock_queue.get_queue_stats.return_value = {"PENDING": 0}
                mock_queue_manager.return_value = mock_queue
                
                result = runner.invoke(cli, ["status"])
            
        mock_load_config.assert_called_once()
        assert result.exit_code == 0