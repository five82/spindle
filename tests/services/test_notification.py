"""Test notification service wrapper."""

import pytest
from unittest.mock import Mock, AsyncMock

from spindle.services.ntfy import NotificationService
from spindle.config import SpindleConfig


class TestNotificationService:
    """Test NotificationService functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(
            staging_dir=tmp_path / "staging",
            # NotificationService uses config.ntfy dict
            # Remove invalid ntfy_url parameter
        )

    @pytest.fixture
    def service(self, config):
        """Create notification service instance."""
        return NotificationService(config)

    def test_notify_disc_detected(self, service):
        """Test disc detection notification."""
        # This should not raise an exception with proper config
        service.notify_disc_detected("TEST_DISC", "movie")

    def test_notify_error(self, service):
        """Test error notification."""
        # This should not raise an exception with proper config
        service.notify_error("Test error", "Test context")

    def test_notify_encode_complete(self, service):
        """Test encode completion notification."""
        # This should not raise an exception with proper config
        service.notify_encode_complete("Test Movie")

    def test_service_initialization(self, service, config):
        """Test service initializes with config."""
        assert service.config == config
        # Config is accessed via the notifier
        assert service.notifier is not None

    def test_has_notifier(self, service):
        """Test service has notifier instance."""
        assert service.notifier is not None