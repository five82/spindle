"""Comprehensive tests for drapto wrapper functionality."""

import json
import subprocess
import tempfile
from pathlib import Path
from unittest.mock import Mock, patch, MagicMock, call
import pytest

from spindle.config import SpindleConfig
from spindle.encode.drapto_wrapper import DraptoEncoder, EncodeResult


class TestDraptoEncoder:
    """Test the DraptoEncoder class."""

    @pytest.fixture
    def config(self):
        """Create test config."""
        with tempfile.TemporaryDirectory() as tmpdir:
            yield SpindleConfig(
                log_dir=Path(tmpdir) / "logs",
                staging_dir=Path(tmpdir) / "staging",
                library_dir=Path(tmpdir) / "library",
                drapto_binary="drapto",
            )

    @pytest.fixture
    def encoder(self, config):
        """Create encoder instance."""
        return DraptoEncoder(config)

    def test_encoder_initialization(self, config):
        """Test encoder initialization."""
        encoder = DraptoEncoder(config)
        assert encoder.config == config

    def test_build_drapto_command_basic(self, encoder):
        """Test basic command building."""
        input_file = Path("/test/input.mkv")
        output_dir = Path("/test/output")
        
        cmd = encoder._build_drapto_command(input_file, output_dir)
        
        assert cmd[0] == "drapto"
        assert "encode" in cmd
        assert "-i" in cmd
        assert str(input_file) in cmd
        assert "-o" in cmd
        assert str(output_dir) in cmd
        assert "--json-progress" in cmd
        assert "--verbose" in cmd

    def test_build_drapto_command_quality_settings(self, encoder):
        """Test command building with quality settings."""
        input_file = Path("/test/input.mkv")
        output_dir = Path("/test/output")
        
        cmd = encoder._build_drapto_command(input_file, output_dir)
        
        assert "--quality-sd" in cmd
        assert "--quality-hd" in cmd
        assert "--quality-uhd" in cmd
        assert "--preset" in cmd
        
        # Check quality values
        sd_idx = cmd.index("--quality-sd")
        assert cmd[sd_idx + 1] == "23"
        hd_idx = cmd.index("--quality-hd")
        assert cmd[hd_idx + 1] == "25"

    @patch('spindle.encode.drapto_wrapper.subprocess.run')
    def test_get_drapto_version_success(self, mock_run, encoder):
        """Test successful version retrieval."""
        mock_run.return_value.stdout = "drapto 1.2.3"
        mock_run.return_value.returncode = 0
        
        version = encoder.get_drapto_version()
        
        assert version == "drapto 1.2.3"
        mock_run.assert_called_once_with(
            ["drapto", "--version"],
            check=False,
            capture_output=True,
            text=True,
            timeout=10
        )

    @patch('spindle.encode.drapto_wrapper.subprocess.run')
    def test_get_drapto_version_failure(self, mock_run, encoder):
        """Test version retrieval failure."""
        mock_run.side_effect = subprocess.SubprocessError("Command failed")
        
        version = encoder.get_drapto_version()
        
        assert version is None

    @patch('spindle.encode.drapto_wrapper.subprocess.run')
    def test_check_drapto_availability_success(self, mock_run, encoder):
        """Test drapto availability check success."""
        mock_run.return_value.returncode = 0
        
        available = encoder.check_drapto_availability()
        
        assert available is True

    @patch('spindle.encode.drapto_wrapper.subprocess.run')
    def test_check_drapto_availability_failure(self, mock_run, encoder):
        """Test drapto availability check failure."""
        mock_run.side_effect = FileNotFoundError("drapto not found")
        
        available = encoder.check_drapto_availability()
        
        assert available is False

    def test_find_output_file_success(self, encoder):
        """Test successful output file detection."""
        with tempfile.TemporaryDirectory() as tmpdir:
            input_file = Path("/test/input.mkv")
            output_dir = Path(tmpdir)
            
            # Create expected output file
            expected_output = output_dir / "input.mkv"
            expected_output.touch()
            
            found_file = encoder._find_output_file(input_file, output_dir)
            
            assert found_file == expected_output

    def test_find_output_file_not_found(self, encoder):
        """Test output file not found."""
        with tempfile.TemporaryDirectory() as tmpdir:
            input_file = Path("/test/input.mkv")
            output_dir = Path(tmpdir)
            
            found_file = encoder._find_output_file(input_file, output_dir)
            
            assert found_file is None

    @patch('spindle.encode.drapto_wrapper.subprocess.Popen')
    def test_run_drapto_with_progress_success(self, mock_popen, encoder):
        """Test successful drapto execution with progress."""
        # Mock process
        mock_process = Mock()
        mock_process.wait.return_value = 0
        mock_process.stdout.readline.side_effect = [
            '{"type": "initialization", "message": "Starting"}',
            '{"type": "encoding_progress", "percent": 50.0}',
            '{"type": "completed", "message": "Done"}',
            ''  # EOF
        ]
        mock_popen.return_value = mock_process

        progress_events = []
        def progress_callback(data):
            progress_events.append(data)

        cmd = ["drapto", "input.mkv", "--json-progress"]
        result = encoder._run_drapto_with_progress(cmd, progress_callback)

        assert result.returncode == 0
        assert len(progress_events) == 3
        assert progress_events[0]["type"] == "initialization"
        assert progress_events[1]["type"] == "encoding_progress"
        assert progress_events[2]["type"] == "completed"

    @patch('spindle.encode.drapto_wrapper.subprocess.Popen')
    def test_run_drapto_with_progress_json_error(self, mock_popen, encoder):
        """Test drapto execution with malformed JSON."""
        mock_process = Mock()
        mock_process.wait.return_value = 0
        mock_process.stdout.readline.side_effect = [
            'invalid json',
            '{"type": "completed", "message": "Done"}',
            ''
        ]
        mock_popen.return_value = mock_process

        progress_events = []
        def progress_callback(data):
            progress_events.append(data)

        cmd = ["drapto", "input.mkv", "--json-progress"]
        result = encoder._run_drapto_with_progress(cmd, progress_callback)

        # Should skip invalid JSON and continue
        assert result.returncode == 0
        assert len(progress_events) == 1
        assert progress_events[0]["type"] == "completed"

    @patch('spindle.encode.drapto_wrapper.subprocess.Popen')
    def test_run_drapto_with_progress_process_error(self, mock_popen, encoder):
        """Test drapto execution with process error."""
        mock_process = Mock()
        mock_process.wait.return_value = 1
        mock_process.stdout.readline.return_value = ''
        mock_popen.return_value = mock_process

        def progress_callback(data):
            pass

        cmd = ["drapto", "input.mkv", "--json-progress"]
        result = encoder._run_drapto_with_progress(cmd, progress_callback)

        assert result.returncode == 1

    @patch.object(DraptoEncoder, '_run_drapto_with_progress')
    @patch.object(DraptoEncoder, '_find_output_file')
    def test_encode_file_success(self, mock_find_output, mock_run_drapto, encoder):
        """Test successful file encoding."""
        # Create real temporary files to avoid Path mocking issues
        with tempfile.NamedTemporaryFile(suffix='.mkv', delete=False) as input_temp:
            input_temp.write(b"test data" * 100)  # Create some content
            input_file = Path(input_temp.name)
            
        with tempfile.NamedTemporaryFile(suffix='.mkv', delete=False) as output_temp:
            output_temp.write(b"test data" * 60)  # Smaller file for size reduction
            output_file = Path(output_temp.name)
        
        try:
            # Setup mocks
            mock_result = Mock()
            mock_result.returncode = 0
            mock_result.stdout_lines = ["Output line 1"]
            mock_run_drapto.return_value = mock_result
            mock_find_output.return_value = output_file

            with tempfile.TemporaryDirectory() as output_dir:
                output_path = Path(output_dir)
                
                progress_events = []
                def progress_callback(data):
                    progress_events.append(data)

                result = encoder.encode_file(input_file, output_path, progress_callback)

                assert isinstance(result, EncodeResult)
                assert result.success is True
                assert result.input_file == input_file
                assert result.output_file == output_file
                assert result.size_reduction_percent == 40.0
                
        finally:
            # Clean up temporary files
            input_file.unlink(missing_ok=True)
            output_file.unlink(missing_ok=True)

    @patch.object(DraptoEncoder, '_run_drapto_with_progress')
    def test_encode_file_drapto_failure(self, mock_run_drapto, encoder):
        """Test file encoding with drapto failure."""
        # Setup mock for failed drapto execution
        mock_result = Mock()
        mock_result.returncode = 1
        mock_result.stdout = "Error: encoding failed"
        mock_result.stderr = ""
        mock_run_drapto.return_value = mock_result

        with tempfile.NamedTemporaryFile(suffix='.mkv') as input_temp:
            input_file = Path(input_temp.name)
            with tempfile.TemporaryDirectory() as output_dir:
                output_path = Path(output_dir)
                
                def progress_callback(data):
                    pass

                result = encoder.encode_file(input_file, output_path, progress_callback)

                assert isinstance(result, EncodeResult)
                assert result.success is False
                # Error message should contain the mock stdout
                assert result.error_message == "Error: encoding failed"

    @patch.object(DraptoEncoder, '_find_output_file')
    @patch.object(DraptoEncoder, '_run_drapto_with_progress')
    def test_encode_file_output_not_found(self, mock_run_drapto, mock_find_output, encoder):
        """Test file encoding when output file is not found."""
        # Setup mocks
        mock_result = Mock()
        mock_result.returncode = 0
        mock_run_drapto.return_value = mock_result
        mock_find_output.return_value = None

        with tempfile.NamedTemporaryFile(suffix='.mkv') as input_temp:
            input_file = Path(input_temp.name)
            with tempfile.TemporaryDirectory() as output_dir:
                output_path = Path(output_dir)
                
                def progress_callback(data):
                    pass

                result = encoder.encode_file(input_file, output_path, progress_callback)

                assert result.success is False
                assert "Output file not found" in result.error_message

    def test_encode_file_invalid_input(self, encoder):
        """Test encoding with non-existent input file."""
        input_file = Path("/nonexistent/file.mkv")
        output_dir = Path("/test/output")
        
        def progress_callback(data):
            pass

        result = encoder.encode_file(input_file, output_dir, progress_callback)

        assert result.success is False
        assert "Input file does not exist" in result.error_message

    @patch.object(DraptoEncoder, 'encode_file')
    def test_encode_batch_success(self, mock_encode_file, encoder):
        """Test successful batch encoding."""
        # Setup mock results
        mock_results = []
        for i in range(3):
            result = Mock(spec=EncodeResult)
            result.success = True
            result.input_file = Path(f"/test/input{i}.mkv")
            mock_results.append(result)
        
        mock_encode_file.side_effect = mock_results

        input_files = [Path(f"/test/input{i}.mkv") for i in range(3)]
        output_dir = Path("/test/output")
        
        def progress_callback(data):
            pass

        results = encoder.encode_batch(input_files, output_dir, progress_callback)

        assert len(results) == 3
        assert all(r.success for r in results)
        assert mock_encode_file.call_count == 3

    @patch.object(DraptoEncoder, 'encode_file')
    def test_encode_batch_partial_failure(self, mock_encode_file, encoder):
        """Test batch encoding with some failures."""
        # Setup mixed results
        success_result = Mock(spec=EncodeResult)
        success_result.success = True
        
        failure_result = Mock(spec=EncodeResult) 
        failure_result.success = False
        failure_result.error_message = "Encoding failed"
        
        mock_encode_file.side_effect = [success_result, failure_result, success_result]

        input_files = [Path(f"/test/input{i}.mkv") for i in range(3)]
        output_dir = Path("/test/output")
        
        def progress_callback(data):
            pass

        results = encoder.encode_batch(input_files, output_dir, progress_callback)

        assert len(results) == 3
        assert results[0].success is True
        assert results[1].success is False
        assert results[2].success is True


class TestEncodeResult:
    """Test the EncodeResult class."""

    def test_encode_result_success(self):
        """Test successful encode result."""
        input_file = Path("/test/input.mkv")
        output_file = Path("/test/output.mkv")
        
        result = EncodeResult(
            success=True,
            input_file=input_file,
            output_file=output_file,
            input_size=1000,
            output_size=600
        )

        assert result.success is True
        assert result.input_file == input_file
        assert result.output_file == output_file
        assert result.size_reduction_percent == 40.0

    def test_encode_result_failure(self):
        """Test failed encode result."""
        input_file = Path("/test/input.mkv")
        
        result = EncodeResult(
            success=False,
            input_file=input_file,
            error_message="Encoding failed"
        )

        assert result.success is False
        assert result.input_file == input_file
        assert result.output_file is None
        assert result.error_message == "Encoding failed"

    def test_size_reduction_percent_no_sizes(self):
        """Test size reduction calculation with no sizes."""
        result = EncodeResult(
            success=True,
            input_file=Path("/test/input.mkv")
        )

        assert result.size_reduction_percent == 0.0

    def test_encode_result_str_success(self):
        """Test string representation of successful result."""
        result = EncodeResult(
            success=True,
            input_file=Path("/test/input.mkv"),
            output_file=Path("/test/output.mkv"),
            input_size=1000,
            output_size=600
        )

        str_repr = str(result)
        assert "Encoded" in str_repr
        assert "40.0%" in str_repr

    def test_encode_result_str_failure(self):
        """Test string representation of failed result."""
        result = EncodeResult(
            success=False,
            input_file=Path("/test/input.mkv"),
            error_message="Encoding failed"
        )

        str_repr = str(result)
        assert "Failed" in str_repr
        assert "Encoding failed" in str_repr