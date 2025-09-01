"""Tests for enhanced error handling system."""

import logging
from unittest.mock import patch

import pytest

from spindle.error_handling import (
    ConfigurationError,
    DependencyError,
    ErrorCategory,
    ExternalToolError,
    HardwareError,
    MediaError,
    SpindleError,
    check_dependencies,
    handle_error,
)


class TestSpindleError:
    """Test the base SpindleError class."""

    def test_basic_error_creation(self):
        """Test creating a basic SpindleError."""
        error = SpindleError(
            "Test error message",
            ErrorCategory.CONFIGURATION,
            solution="Fix your config"
        )
        
        assert error.message == "Test error message"
        assert error.category == ErrorCategory.CONFIGURATION
        assert error.solution == "Fix your config"
        assert error.recoverable is True
        assert error.log_level == logging.ERROR

    def test_error_display(self, capsys):
        """Test error display to user."""
        error = SpindleError(
            "Configuration is invalid",
            ErrorCategory.CONFIGURATION,
            solution="Check your config file",
            details="Missing required field 'api_key'"
        )
        
        error.display_to_user()
        captured = capsys.readouterr()
        
        assert "Configuration Error" in captured.out
        assert "Configuration is invalid" in captured.out
        assert "Check your config file" in captured.out
        assert "Missing required field" in captured.out

    def test_non_recoverable_error(self, capsys):
        """Test non-recoverable error display."""
        error = SpindleError(
            "Fatal system error",
            ErrorCategory.SYSTEM,
            recoverable=False
        )
        
        error.display_to_user()
        captured = capsys.readouterr()
        
        assert "requires intervention" in captured.out

    @patch('spindle.error_handling.logger')
    def test_error_logging(self, mock_logger):
        """Test error logging with original exception."""
        original = ValueError("Original error")
        error = SpindleError(
            "Wrapped error",
            ErrorCategory.SYSTEM,
            original_error=original,
            log_level=logging.WARNING
        )
        
        error.display_to_user()
        
        mock_logger.log.assert_called_once_with(
            logging.WARNING,
            "%s: %s",
            "system",
            "Wrapped error",
            exc_info=original
        )


class TestSpecificErrors:
    """Test specific error types."""

    def test_configuration_error(self):
        """Test ConfigurationError with path."""
        from pathlib import Path
        config_path = Path("/test/config.toml")
        
        error = ConfigurationError(
            "Invalid configuration",
            config_path=config_path
        )
        
        assert error.category == ErrorCategory.CONFIGURATION
        assert str(config_path) in error.solution

    def test_dependency_error(self):
        """Test DependencyError with install command."""
        error = DependencyError(
            "missing-tool",
            install_command="apt install missing-tool"
        )
        
        assert error.category == ErrorCategory.DEPENDENCY
        assert "missing-tool" in error.message
        assert "apt install missing-tool" in error.solution
        assert not error.recoverable  # Dependencies are critical

    def test_hardware_error(self):
        """Test HardwareError with default solution."""
        error = HardwareError("Drive not responding")
        
        assert error.category == ErrorCategory.HARDWARE
        assert "Drive not responding" in error.message
        assert "disc is inserted properly" in error.solution

    def test_media_error(self):
        """Test MediaError with default solution."""
        error = MediaError("Disc is scratched")
        
        assert error.category == ErrorCategory.MEDIA
        assert "Disc is scratched" in error.message
        assert "cleaning the disc" in error.solution

    def test_external_tool_error(self):
        """Test ExternalToolError with exit code and stderr."""
        error = ExternalToolError(
            "makemkv",
            exit_code=1,
            stderr="License expired"
        )
        
        assert error.category == ErrorCategory.EXTERNAL_TOOL
        assert "makemkv failed with exit code 1" in error.message
        assert "License expired" in error.details


class TestErrorHandling:
    """Test the handle_error function."""

    def test_handle_spindle_error(self, capsys):
        """Test handling SpindleError - should display directly."""
        error = ConfigurationError("Test config error")
        
        handle_error(error)
        captured = capsys.readouterr()
        
        assert "Configuration Error" in captured.out

    def test_handle_file_not_found(self, capsys):
        """Test handling FileNotFoundError."""
        error = FileNotFoundError("Config file not found")
        
        handle_error(error)
        captured = capsys.readouterr()
        
        assert "Filesystem Error" in captured.out
        assert "Config file not found" in captured.out

    def test_handle_generic_error(self, capsys):
        """Test handling generic Exception."""
        error = RuntimeError("Something went wrong")
        
        handle_error(error, category=ErrorCategory.MEDIA)
        captured = capsys.readouterr()
        
        assert "Media Error" in captured.out
        assert "Something went wrong" in captured.out


class TestDependencyChecking:
    """Test dependency checking functionality."""

    @patch('shutil.which')
    def test_check_dependencies_all_missing(self, mock_which):
        """Test when all dependencies are missing."""
        mock_which.return_value = None  # All tools missing
        
        errors = check_dependencies()
        
        assert len(errors) == 3  # uv, makemkv, drapto
        assert all(isinstance(e, DependencyError) for e in errors)
        
        # Check specific dependencies
        tools = [e.message.split("'")[1] for e in errors]
        assert "uv" in tools
        assert "MakeMKV" in tools
        assert "drapto" in tools

    @patch('shutil.which')
    def test_check_dependencies_all_present(self, mock_which):
        """Test when all dependencies are present."""
        mock_which.return_value = "/usr/bin/tool"  # All tools present
        
        errors = check_dependencies()
        
        assert len(errors) == 0

    @patch('shutil.which')
    def test_check_dependencies_partial(self, mock_which):
        """Test when some dependencies are missing."""
        def mock_which_side_effect(cmd):
            return "/usr/bin/tool" if cmd == "uv" else None
        
        mock_which.side_effect = mock_which_side_effect
        
        errors = check_dependencies()
        
        assert len(errors) == 2  # makemkv and drapto missing
        tools = [e.message.split("'")[1] for e in errors]
        assert "uv" not in tools
        assert "MakeMKV" in tools
        assert "drapto" in tools


class TestErrorCategories:
    """Test error category classification."""

    def test_all_categories_have_styles(self):
        """Test that all error categories have display styles."""
        from spindle.error_handling import ErrorCategory
        
        # Create an error for each category to test display
        for category in ErrorCategory:
            error = SpindleError(
                f"Test {category.value} error",
                category
            )
            
            # Should not raise exception
            error.display_to_user()

    def test_category_enum_values(self):
        """Test ErrorCategory enum has expected values."""
        expected_categories = {
            "configuration", "dependency", "hardware", "network",
            "filesystem", "media", "external_tool", "system", "user_input"
        }
        
        actual_categories = {cat.value for cat in ErrorCategory}
        
        assert actual_categories == expected_categories