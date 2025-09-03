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
        assert error.config_path == config_path
        assert "configuration" in error.solution.lower()

    def test_dependency_error(self):
        """Test DependencyError."""
        error = DependencyError(
            "MakeMKV not found",
            tool="makemkvcon",
            install_cmd="sudo apt install makemkv-bin"
        )
        
        assert error.category == ErrorCategory.DEPENDENCY
        assert error.tool == "makemkvcon"
        assert error.install_cmd == "sudo apt install makemkv-bin"

    def test_external_tool_error(self):
        """Test ExternalToolError."""
        error = ExternalToolError(
            "Disc scan failed",
            tool="makemkvcon",
            exit_code=1,
            stderr="Read error"
        )
        
        assert error.category == ErrorCategory.EXTERNAL_TOOL
        assert error.tool == "makemkvcon"
        assert error.exit_code == 1
        assert error.stderr == "Read error"

    def test_hardware_error(self):
        """Test HardwareError."""
        error = HardwareError(
            "Optical drive not responding",
            device="/dev/sr0"
        )
        
        assert error.category == ErrorCategory.HARDWARE
        assert error.device == "/dev/sr0"

    def test_media_error(self):
        """Test MediaError."""
        error = MediaError(
            "Disc is scratched",
            disc_label="MOVIE_DISC"
        )
        
        assert error.category == ErrorCategory.MEDIA
        assert error.disc_label == "MOVIE_DISC"


class TestDependencyChecking:
    """Test dependency checking functionality."""

    @patch('shutil.which')
    def test_check_dependencies_all_present(self, mock_which):
        """Test when all dependencies are present."""
        mock_which.return_value = "/usr/bin/makemkvcon"
        
        errors = check_dependencies()
        
        assert len(errors) == 0

    @patch('shutil.which')
    def test_check_dependencies_missing(self, mock_which):
        """Test when dependencies are missing."""
        mock_which.return_value = None
        
        errors = check_dependencies()
        
        assert len(errors) > 0
        assert any("makemkvcon" in str(error) for error in errors)

    @patch('shutil.which')
    def test_check_specific_dependency(self, mock_which):
        """Test checking specific dependency."""
        mock_which.side_effect = lambda cmd: "/usr/bin/" + cmd if cmd == "makemkvcon" else None
        
        errors = check_dependencies(tools=["makemkvcon", "drapto"])
        
        assert len(errors) == 1
        assert "drapto" in str(errors[0])


class TestErrorHandler:
    """Test error handler functionality."""

    def test_handle_error_display_only(self, capsys):
        """Test error handler displays error."""
        error = SpindleError("Test error", ErrorCategory.CONFIGURATION)
        
        handle_error(error)
        captured = capsys.readouterr()
        
        assert "Configuration Error" in captured.out
        assert "Test error" in captured.out

    def test_handle_error_with_exit(self):
        """Test error handler can exit on fatal errors."""
        error = SpindleError(
            "Fatal error", 
            ErrorCategory.SYSTEM, 
            recoverable=False
        )
        
        with pytest.raises(SystemExit):
            handle_error(error, exit_on_fatal=True)

    @patch('spindle.error_handling.logger')
    def test_handle_error_logging(self, mock_logger):
        """Test error handler logs errors."""
        error = SpindleError("Test error", ErrorCategory.CONFIGURATION)
        
        handle_error(error)
        
        mock_logger.log.assert_called_once()


class TestErrorCategories:
    """Test error categorization."""

    def test_error_categories_exist(self):
        """Test that error categories are properly defined."""
        categories = [
            ErrorCategory.CONFIGURATION,
            ErrorCategory.DEPENDENCY,
            ErrorCategory.EXTERNAL_TOOL,
            ErrorCategory.HARDWARE,
            ErrorCategory.MEDIA,
            ErrorCategory.NETWORK,
            ErrorCategory.SYSTEM
        ]
        
        for category in categories:
            assert category is not None
            assert isinstance(category.value, str)

    def test_category_display_names(self):
        """Test category display names."""
        error = SpindleError("Test", ErrorCategory.CONFIGURATION)
        display_name = error._get_category_display()
        
        assert "Configuration" in display_name
        assert "⚙️" in display_name or "🔧" in display_name


class TestUserExperienceFlow:
    """Test user-facing error experience workflow."""

    def test_workflow_error_chain(self, capsys):
        """Test complete error handling workflow."""
        try:
            # Simulate a workflow error
            raise ValueError("Simulated disc read error")
        except ValueError as e:
            error = ExternalToolError(
                "Failed to read disc",
                tool="makemkvcon",
                exit_code=1,
                original_error=e,
                solution="Check disc for scratches and try again"
            )
            
            handle_error(error)
            captured = capsys.readouterr()
            
            assert "External Tool Error" in captured.out
            assert "Failed to read disc" in captured.out
            assert "Check disc for scratches" in captured.out

    def test_error_recovery_guidance(self, capsys):
        """Test that errors provide clear recovery guidance."""
        error = ConfigurationError(
            "TMDB API key not configured",
            solution="Add your TMDB API key to config.toml"
        )
        
        error.display_to_user()
        captured = capsys.readouterr()
        
        assert "Add your TMDB API key" in captured.out
        assert "config.toml" in captured.out

    def test_multiple_error_handling(self):
        """Test handling multiple errors in sequence."""
        errors = [
            DependencyError("makemkvcon not found", tool="makemkvcon"),
            DependencyError("drapto not found", tool="drapto")
        ]
        
        for error in errors:
            # Should not raise exceptions
            handle_error(error, exit_on_fatal=False)
            
        assert len(errors) == 2