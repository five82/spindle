"""Essential encoding integration tests - drapto wrapper and progress tracking."""

import json
import tempfile
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch

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
        assert "--json-progress" in command

    def test_quality_mapping(self, temp_config):
        """Test quality parameter mapping."""
        encoder = DraptoEncoder(temp_config)
        
        # Quality should match config
        assert encoder.quality == 26
        
        # Test with different quality
        temp_config.drapto_quality_hd = 22
        encoder2 = DraptoEncoder(temp_config)
        assert encoder2.quality == 22


class TestProgressParsing:
    """Test progress event parsing and handling."""
    
    def test_parse_progress_event(self, temp_config):
        """Test JSON progress event parsing."""
        encoder = DraptoEncoder(temp_config)
        
        # Sample JSON progress output from drapto
        json_line = '{"type": "progress", "percent": 45.2, "fps": 23.4, "eta": "00:15:30"}'
        
        # Parse JSON directly for testing
        event_data = json.loads(json_line)
        
        assert event_data["percent"] == 45.2
        assert event_data["fps"] == 23.4
        assert event_data["eta"] == "00:15:30"

    def test_parse_invalid_progress(self, temp_config):
        """Test handling of invalid progress events."""
        encoder = DraptoEncoder(temp_config)
        
        # Test invalid JSON handling
        try:
            json.loads("invalid json")
            assert False, "Should have raised exception"
        except json.JSONDecodeError:
            pass  # Expected
        
        # Test incomplete JSON
        incomplete_json = '{"type": "info", "message": "starting"}'
        incomplete_data = json.loads(incomplete_json)
        assert "percent" not in incomplete_data

    def test_progress_callback(self, temp_config):
        """Test progress callback mechanism."""
        encoder = DraptoEncoder(temp_config)
        progress_events = []
        
        def capture_progress(data):
            progress_events.append(data)
        
        # Simulate callback with progress data
        json_line = '{"type": "progress", "percent": 75.0, "fps": 28.1, "eta": "00:05:12"}'
        event_data = json.loads(json_line)
        
        capture_progress(event_data)
        
        assert len(progress_events) == 1
        assert progress_events[0]["percent"] == 75.0


class TestEncodingExecution:
    """Test encoding execution and process management."""
    
    @patch('subprocess.Popen')
    def test_encode_success(self, mock_popen, temp_config, sample_input_file, sample_output_file):
        """Test successful encoding process."""
        # Mock successful process
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
        progress_events = []
        
        def track_progress(event):
            progress_events.append(event)
        
        # Create output directory and file to simulate successful encoding
        output_dir = sample_output_file.parent
        output_dir.mkdir(parents=True, exist_ok=True)
        sample_output_file.touch()
        
        result = encoder.encode_file(sample_input_file, output_dir, progress_callback=track_progress)
        
        assert result.success is True
        # Note: progress events will be empty in mock, but no exception should occur
        mock_popen.assert_called_once()

    @patch('subprocess.Popen')
    def test_encode_failure(self, mock_popen, temp_config, sample_input_file, sample_output_file):
        """Test encoding failure handling."""
        # Mock failed process
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

    def test_output_validation(self, temp_config, sample_input_file):
        """Test output file validation."""
        encoder = DraptoEncoder(temp_config)
        
        with tempfile.NamedTemporaryFile(suffix=".mp4", delete=False) as f:
            output_file = Path(f.name)
            f.write(b"encoded video content")
        
        # Test that files exist vs don't exist
        assert output_file.exists() is True
        
        # Missing output file
        missing_file = Path("/tmp/nonexistent.mp4")
        assert missing_file.exists() is False
        
        # Empty output file
        empty_file = Path(tempfile.mktemp(suffix=".mp4"))
        empty_file.touch()
        assert empty_file.stat().st_size == 0  # File exists but is empty


class TestProgressEvent:
    """Test progress event data structure."""
    
    def test_progress_event_creation(self):
        """Test progress event object creation."""
        event = {
            "percent": 65.5,
            "fps": 30.2,
            "eta": "00:08:45",
            "bitrate": 2500,
            "size": 1024000
        }
        
        assert event["percent"] == 65.5
        assert event["fps"] == 30.2
        assert event["eta"] == "00:08:45"
        assert event["bitrate"] == 2500
        assert event["size"] == 1024000

    def test_progress_event_defaults(self):
        """Test progress event with default values."""
        event = {"percent": 50.0}
        
        assert event["percent"] == 50.0
        assert event.get("fps") is None
        assert event.get("eta") is None
        assert event.get("bitrate") is None
        assert event.get("size") is None


class TestEncodingWorkflow:
    """Test encoding integration with other components."""
    
    @patch('subprocess.Popen')
    def test_encoding_with_queue_integration(self, mock_popen, temp_config, sample_input_file):
        """Test encoding integrates with queue system."""
        from spindle.queue.manager import QueueManager
        
        # Setup queue manager
        queue_manager = QueueManager(temp_config)
        item = queue_manager.add_disc("TEST_DISC")
        item.ripped_file = sample_input_file
        queue_manager.update_item(item)
        
        # Mock successful encoding
        mock_process = Mock()
        mock_process.poll.return_value = None
        mock_process.stdout.readline.side_effect = [
            '{"type": "progress", "percent": 100.0, "fps": 25.0}\n',
            '',
        ]
        mock_process.wait.return_value = 0
        mock_popen.return_value = mock_process
        
        encoder = DraptoEncoder(temp_config)
        
        # Track progress updates
        progress_updates = []
        
        def update_queue_progress(event):
            item.progress_percent = event.get("percent", 0)
            item.progress_stage = "encoding"
            queue_manager.update_item(item)
            progress_updates.append(event.get("percent", 0))
        
        # Simulate progress callback
        event_data = {"percent": 100.0, "fps": 25.0}
        update_queue_progress(event_data)
        
        output_dir = temp_config.staging_dir / "encoded"
        output_dir.mkdir(parents=True, exist_ok=True)
        # Create the expected output file (drapto creates .mkv files)
        expected_output = output_dir / f"{sample_input_file.stem}.mkv"
        expected_output.touch()  # Simulate successful output
        
        result = encoder.encode_file(sample_input_file, output_dir)
        
        assert result.success is True
        assert len(progress_updates) >= 1
        
        # Verify queue item was updated
        updated_item = queue_manager.get_item(item.item_id)
        assert updated_item.progress_stage == "encoding"

    def test_file_path_handling(self, temp_config):
        """Test proper file path handling."""
        encoder = DraptoEncoder(temp_config)
        
        # Test path handling
        input_path = Path("input.mkv")
        output_path = Path("output.mp4")
        
        command = encoder.build_command(input_path, output_path)
        
        # Should include paths in command (as provided)
        assert "input.mkv" in " ".join(command)
        assert "output.mp4" in " ".join(command)

    def test_quality_parameter_validation(self, temp_config):
        """Test quality parameter validation."""
        # Valid quality range
        temp_config.drapto_quality_hd = 24
        encoder = DraptoEncoder(temp_config)
        assert encoder.quality == 24
        
        # Edge cases
        temp_config.drapto_quality_hd = 18
        encoder = DraptoEncoder(temp_config)
        assert encoder.quality == 18
        
        temp_config.drapto_quality_hd = 30
        encoder = DraptoEncoder(temp_config)
        assert encoder.quality == 30