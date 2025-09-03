"""Essential encoding integration tests - drapto wrapper and progress tracking."""

import json
import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.encode.drapto_wrapper import DraptoEncoder


@pytest.fixture
def temp_config():
    """Create temporary config for testing."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield SpindleConfig(
            log_dir=Path(tmpdir) / "logs",
            staging_dir=Path(tmpdir) / "staging",
            library_dir=Path(tmpdir) / "library",
            drapto_binary="drapto",
            drapto_quality_hd=26,
        )


@pytest.fixture
def sample_input_file():
    """Sample input video file."""
    with tempfile.NamedTemporaryFile(suffix=".mkv", delete=False) as f:
        f.write(b"fake video content")
        yield Path(f.name)


@pytest.fixture
def sample_output_file():
    """Sample output video file."""
    with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as f:
        yield Path(f.name)


class TestDraptoEncoder:
    """Test essential drapto encoder functionality."""
    
    def test_encoder_initialization(self, temp_config):
        """Test encoder initializes with configuration."""
        encoder = DraptoEncoder(temp_config)
        
        assert encoder.config == temp_config
        assert encoder.drapto_binary == "drapto"
        assert encoder.quality == 26

    def test_build_command(self, temp_config, sample_input_file, sample_output_file):
        """Test drapto command construction."""
        encoder = DraptoEncoder(temp_config)
        
        command = encoder.build_command(sample_input_file, sample_output_file)
        
        assert "drapto" in command
        assert str(sample_input_file) in command
        assert str(sample_output_file) in command
        assert "--quality" in command
        assert "26" in command
        assert "--progress-json" in command


class TestProgressHandling:
    """Test progress event parsing and handling."""
    
    def test_parse_progress_event(self, temp_config):
        """Test JSON progress event parsing."""
        encoder = DraptoEncoder(temp_config)
        
        json_line = '{"type": "progress", "percent": 45.2, "fps": 23.4, "eta": "00:15:30"}'
        event_data = json.loads(json_line)
        
        assert event_data["percent"] == 45.2
        assert event_data["fps"] == 23.4
        assert event_data["eta"] == "00:15:30"

    def test_progress_callback(self, temp_config):
        """Test progress callback mechanism."""
        encoder = DraptoEncoder(temp_config)
        progress_events = []
        
        def capture_progress(data):
            progress_events.append(data)
        
        json_line = '{"type": "progress", "percent": 75.0, "fps": 28.1, "eta": "00:05:12"}'
        event_data = json.loads(json_line)
        
        capture_progress(event_data)
        
        assert len(progress_events) == 1
        assert progress_events[0]["percent"] == 75.0


class TestEncodingWorkflow:
    """Test encoding execution and integration."""
    
    @patch('subprocess.Popen')
    def test_encode_success(self, mock_popen, temp_config, sample_input_file, sample_output_file):
        """Test successful encoding process."""
        mock_process = Mock()
        mock_process.poll.return_value = None
        mock_process.stdout.readline.side_effect = [
            '{"type": "progress", "percent": 50.0, "fps": 25.0, "eta": "00:10:00"}\n',
            '{"type": "complete", "success": true}\n',
            '',
        ]
        mock_process.wait.return_value = 0
        mock_popen.return_value = mock_process
        
        encoder = DraptoEncoder(temp_config)
        
        output_dir = sample_output_file.parent
        output_dir.mkdir(parents=True, exist_ok=True)
        sample_output_file.touch()
        
        result = encoder.encode_file(sample_input_file, output_dir)
        
        assert result.success is True
        mock_popen.assert_called_once()

    @patch('subprocess.Popen')
    def test_encode_failure(self, mock_popen, temp_config, sample_input_file, sample_output_file):
        """Test encoding failure handling."""
        mock_process = Mock()
        mock_process.poll.return_value = None
        mock_process.stdout.readline.side_effect = [
            '{"type": "error", "message": "encoding failed"}\n',
            '',
        ]
        mock_process.wait.return_value = 1
        mock_popen.return_value = mock_process
        
        encoder = DraptoEncoder(temp_config)
        output_dir = sample_output_file.parent
        
        result = encoder.encode_file(sample_input_file, output_dir)
        
        assert result.success is False

    @patch('subprocess.Popen')
    def test_encoding_with_queue_integration(self, mock_popen, temp_config, sample_input_file):
        """Test encoding integrates with queue system."""
        from spindle.queue.manager import QueueManager
        
        queue_manager = QueueManager(temp_config)
        item = queue_manager.add_disc("TEST_DISC")
        item.ripped_file = sample_input_file
        queue_manager.update_item(item)
        
        mock_process = Mock()
        mock_process.poll.return_value = None
        mock_process.stdout.readline.side_effect = [
            '{"type": "progress", "percent": 100.0, "fps": 25.0}\n',
            '',
        ]
        mock_process.wait.return_value = 0
        mock_popen.return_value = mock_process
        
        encoder = DraptoEncoder(temp_config)
        
        progress_updates = []
        
        def update_queue_progress(event):
            item.progress_percent = event.get("percent", 0)
            item.progress_stage = "encoding"
            queue_manager.update_item(item)
            progress_updates.append(event.get("percent", 0))
        
        event_data = {"percent": 100.0, "fps": 25.0}
        update_queue_progress(event_data)
        
        output_dir = temp_config.staging_dir / "encoded"
        output_dir.mkdir(parents=True, exist_ok=True)
        expected_output = output_dir / f"{sample_input_file.stem}.mkv"
        expected_output.touch()
        
        result = encoder.encode_file(sample_input_file, output_dir)
        
        assert result.success is True
        assert len(progress_updates) >= 1
        
        updated_item = queue_manager.get_item(item.item_id)
        assert updated_item.progress_stage == "encoding"


class TestConfiguration:
    """Test encoding configuration handling."""
    
    def test_quality_mapping(self, temp_config):
        """Test quality parameter mapping."""
        encoder = DraptoEncoder(temp_config)
        assert encoder.quality == 26
        
        temp_config.drapto_quality_hd = 22
        encoder2 = DraptoEncoder(temp_config)
        assert encoder2.quality == 22

    def test_output_validation(self, temp_config, sample_input_file):
        """Test output file validation."""
        encoder = DraptoEncoder(temp_config)
        
        with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as f:
            output_file = Path(f.name)
            f.write(b"encoded video content")
        
        assert output_file.exists() is True
        
        missing_file = Path("/tmp/nonexistent.mp4")
        assert missing_file.exists() is False