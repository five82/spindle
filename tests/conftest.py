"""Shared test configuration and fixtures."""

import logging
import pytest

from spindle.cli import cleanup_logging


@pytest.fixture(scope="function", autouse=True)
def cleanup_logging_handlers():
    """Automatically cleanup logging handlers after each test to prevent ResourceWarnings."""
    yield
    cleanup_logging()


@pytest.fixture(scope="function", autouse=True) 
def reset_logging():
    """Reset logging configuration after each test."""
    yield
    # Clear all handlers and reset to default
    root_logger = logging.getLogger()
    for handler in root_logger.handlers[:]:
        handler.close()
        root_logger.removeHandler(handler)
    # Reset logging level
    root_logger.setLevel(logging.WARNING)