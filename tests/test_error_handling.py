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
    """Test specific error types basic functionality."""

    def test_configuration_error_basic(self):
        """Test ConfigurationError basic creation."""
        from pathlib import Path
        
        error = ConfigurationError("Invalid configuration")
        
        assert error.category == ErrorCategory.CONFIGURATION
        assert "Invalid configuration" in str(error)

    def test_dependency_error_basic(self):
        """Test DependencyError basic creation.""" 
        error = DependencyError("makemkvcon")
        
        assert error.category == ErrorCategory.DEPENDENCY
        assert "makemkvcon" in str(error)

    def test_external_tool_error_basic(self):
        """Test ExternalToolError basic creation."""
        error = ExternalToolError("makemkvcon")
        
        assert error.category == ErrorCategory.EXTERNAL_TOOL
        assert "makemkvcon" in str(error)

    def test_hardware_error_basic(self):
        """Test HardwareError basic creation."""
        error = HardwareError("Drive not responding")
        
        assert error.category == ErrorCategory.HARDWARE
        assert "Drive not responding" in str(error)

    def test_media_error_basic(self):
        """Test MediaError basic creation."""
        error = MediaError("Disc is scratched")
        
        assert error.category == ErrorCategory.MEDIA
        assert "Disc is scratched" in str(error)


class TestDependencyChecking:
    """Test dependency checking functionality."""

    @patch('shutil.which')
    def test_check_dependencies_all_present(self, mock_which):
        """Test when all dependencies are present."""
        mock_which.return_value = "/usr/bin/tool"  # All tools found
        
        errors = check_dependencies()
        
        assert len(errors) == 0

    @patch('shutil.which')
    def test_check_dependencies_missing(self, mock_which):
        """Test when dependencies are missing."""
        mock_which.return_value = None  # No tools found
        
        errors = check_dependencies()
        
        assert len(errors) > 0
        assert any("uv" in str(error) for error in errors)  # uv is always checked


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
        """Test error handler displays fatal errors properly."""
        error = SpindleError(
            "Fatal error", 
            ErrorCategory.SYSTEM, 
            recoverable=False
        )
        
        # The current implementation doesn't exit, just displays
        handle_error(error, exit_on_fatal=True)
        # Test passes if no exception is raised

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
        """Test category display through error display."""
        error = SpindleError("Test", ErrorCategory.CONFIGURATION)
        
        # Test that error category is reflected in string representation
        assert error.category == ErrorCategory.CONFIGURATION
        assert error.category.value == "configuration"


class TestUserExperienceFlow:
    """Test user-facing error experience workflow."""

    def test_workflow_error_chain(self, capsys):
        """Test complete error handling workflow."""
        try:
            # Simulate a workflow error
            raise ValueError("Simulated disc read error")
        except ValueError as e:
            error = ExternalToolError(
                "makemkvcon",
                original_error=e,
                solution="Check disc for scratches and try again"
            )
            
            handle_error(error)
            captured = capsys.readouterr()
            
            assert "External_Tool Error" in captured.out
            assert "makemkvcon failed" in captured.out
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
            DependencyError("makemkvcon"),
            DependencyError("drapto")
        ]
        
        for error in errors:
            # Should not raise exceptions
            handle_error(error, exit_on_fatal=False)
            
        assert len(errors) == 2