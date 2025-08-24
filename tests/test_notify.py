"""Tests for notification system."""

from unittest.mock import Mock, patch

import httpx
import pytest

from spindle.config import SpindleConfig
from spindle.notify.ntfy import NtfyNotifier


@pytest.fixture
def mock_config():
    """Create a mock configuration for testing."""
    config = Mock(spec=SpindleConfig)
    config.ntfy_topic = "https://ntfy.sh/test-topic"
    config.ntfy_request_timeout = 30.0
    return config


@pytest.fixture
def mock_config_no_topic():
    """Create a mock configuration with no ntfy topic."""
    config = Mock(spec=SpindleConfig)
    config.ntfy_topic = None
    config.ntfy_request_timeout = 30.0
    return config


@pytest.fixture
def notifier(mock_config):
    """Create a NtfyNotifier instance for testing."""
    return NtfyNotifier(mock_config)


@pytest.fixture
def notifier_no_topic(mock_config_no_topic):
    """Create a NtfyNotifier instance with no topic configured."""
    return NtfyNotifier(mock_config_no_topic)


class TestNtfyNotifierInit:
    """Test NtfyNotifier initialization."""

    def test_init_with_topic(self, mock_config):
        """Test initialization with valid configuration."""
        notifier = NtfyNotifier(mock_config)

        assert notifier.config == mock_config
        assert notifier.topic_url == "https://ntfy.sh/test-topic"
        assert isinstance(notifier.client, httpx.Client)

    def test_init_without_topic(self, mock_config_no_topic):
        """Test initialization without ntfy topic."""
        notifier = NtfyNotifier(mock_config_no_topic)

        assert notifier.config == mock_config_no_topic
        assert notifier.topic_url is None
        assert isinstance(notifier.client, httpx.Client)


class TestSendNotification:
    """Test the core send_notification method."""

    @patch("spindle.notify.ntfy.httpx.Client")
    def test_send_notification_success(self, mock_client_class, notifier):
        """Test successful notification sending."""
        # Setup mock client
        mock_client = Mock()
        mock_response = Mock()
        mock_response.raise_for_status.return_value = None
        mock_client.post.return_value = mock_response
        mock_client_class.return_value = mock_client
        notifier.client = mock_client

        # Test basic notification
        result = notifier.send_notification("Test message")

        assert result is True
        mock_client.post.assert_called_once_with(
            "https://ntfy.sh/test-topic",
            data=b"Test message",
            headers={},
        )

    @patch("spindle.notify.ntfy.httpx.Client")
    def test_send_notification_with_all_params(self, mock_client_class, notifier):
        """Test notification with all parameters."""
        mock_client = Mock()
        mock_response = Mock()
        mock_response.raise_for_status.return_value = None
        mock_client.post.return_value = mock_response
        mock_client_class.return_value = mock_client
        notifier.client = mock_client

        result = notifier.send_notification(
            message="Test message",
            title="Test Title",
            priority="high",
            tags="test,notification",
        )

        assert result is True
        mock_client.post.assert_called_once_with(
            "https://ntfy.sh/test-topic",
            data=b"Test message",
            headers={
                "Title": "Test Title",
                "Priority": "high",
                "Tags": "test,notification",
            },
        )

    @patch("spindle.notify.ntfy.httpx.Client")
    def test_send_notification_default_priority(self, mock_client_class, notifier):
        """Test notification with default priority is not included in headers."""
        mock_client = Mock()
        mock_response = Mock()
        mock_response.raise_for_status.return_value = None
        mock_client.post.return_value = mock_response
        mock_client_class.return_value = mock_client
        notifier.client = mock_client

        result = notifier.send_notification(message="Test message", priority="default")

        assert result is True
        expected_headers = {}
        mock_client.post.assert_called_once_with(
            "https://ntfy.sh/test-topic",
            data=b"Test message",
            headers=expected_headers,
        )

    def test_send_notification_no_topic(self, notifier_no_topic):
        """Test notification when no topic is configured."""
        result = notifier_no_topic.send_notification("Test message")
        assert result is False

    @patch("spindle.notify.ntfy.httpx.Client")
    def test_send_notification_request_error(self, mock_client_class, notifier):
        """Test handling of HTTP request errors."""
        mock_client = Mock()
        mock_client.post.side_effect = httpx.RequestError("Connection failed")
        mock_client_class.return_value = mock_client
        notifier.client = mock_client

        result = notifier.send_notification("Test message")
        assert result is False

    @patch("spindle.notify.ntfy.httpx.Client")
    def test_send_notification_http_status_error(self, mock_client_class, notifier):
        """Test handling of HTTP status errors."""
        mock_client = Mock()
        mock_response = Mock()
        mock_response.status_code = 404
        mock_response.text = "Not Found"

        http_error = httpx.HTTPStatusError(
            "404 Not Found", request=Mock(), response=mock_response,
        )
        mock_client.post.side_effect = http_error
        mock_client_class.return_value = mock_client
        notifier.client = mock_client

        result = notifier.send_notification("Test message")
        assert result is False


class TestNotificationMethods:
    """Test specific notification methods."""

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_disc_detected(self, mock_send, notifier):
        """Test disc detection notification."""
        mock_send.return_value = True

        result = notifier.notify_disc_detected("Test Movie", "Blu-ray")

        assert result is True
        mock_send.assert_called_once_with(
            "Detected Blu-ray disc: Test Movie",
            title="💿 Disc Detected",
            tags="spindle,disc,detected",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_rip_started(self, mock_send, notifier):
        """Test rip started notification."""
        mock_send.return_value = True

        result = notifier.notify_rip_started("Test Movie")

        assert result is True
        mock_send.assert_called_once_with(
            "Started ripping: Test Movie",
            title="🎬 Ripping Started",
            tags="spindle,rip,started",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_rip_completed(self, mock_send, notifier):
        """Test rip completion notification."""
        mock_send.return_value = True

        result = notifier.notify_rip_completed("Test Movie", "1h 30m")

        assert result is True
        mock_send.assert_called_once_with(
            "Completed ripping: Test Movie (took 1h 30m)",
            title="✅ Ripping Complete",
            tags="spindle,rip,completed",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_media_added(self, mock_send, notifier):
        """Test media added notification."""
        mock_send.return_value = True

        result = notifier.notify_media_added("Test Movie", "movie")

        assert result is True
        mock_send.assert_called_once_with(
            "Added to Plex: Test Movie",
            title="📚 Movie Added",
            tags="spindle,plex,added",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_queue_started(self, mock_send, notifier):
        """Test queue started notification."""
        mock_send.return_value = True

        result = notifier.notify_queue_started(5)

        assert result is True
        mock_send.assert_called_once_with(
            "Started processing queue with 5 items",
            title="🔄 Queue Processing Started",
            tags="spindle,queue,started",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_queue_completed_success(self, mock_send, notifier):
        """Test queue completion notification with no failures."""
        mock_send.return_value = True

        result = notifier.notify_queue_completed(5, 0, "2h 15m")

        assert result is True
        mock_send.assert_called_once_with(
            "Queue processing complete: 5 items processed in 2h 15m",
            title="✅ Queue Complete",
            tags="spindle,queue,completed",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_queue_completed_with_failures(self, mock_send, notifier):
        """Test queue completion notification with failures."""
        mock_send.return_value = True

        result = notifier.notify_queue_completed(3, 2, "2h 15m")

        assert result is True
        mock_send.assert_called_once_with(
            "Queue processing complete: 3 succeeded, 2 failed in 2h 15m",
            title="⚠️ Queue Complete (with errors)",
            tags="spindle,queue,completed",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_error_with_context(self, mock_send, notifier):
        """Test error notification with context."""
        mock_send.return_value = True

        result = notifier.notify_error("Something went wrong", "During ripping")

        assert result is True
        mock_send.assert_called_once_with(
            "Error: Something went wrong\nContext: During ripping",
            title="❌ Spindle Error",
            priority="high",
            tags="spindle,error,alert",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_error_without_context(self, mock_send, notifier):
        """Test error notification without context."""
        mock_send.return_value = True

        result = notifier.notify_error("Something went wrong")

        assert result is True
        mock_send.assert_called_once_with(
            "Error: Something went wrong",
            title="❌ Spindle Error",
            priority="high",
            tags="spindle,error,alert",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_notify_unidentified_media(self, mock_send, notifier):
        """Test unidentified media notification."""
        mock_send.return_value = True

        result = notifier.notify_unidentified_media("unknown_movie.mkv")

        assert result is True
        mock_send.assert_called_once_with(
            "Could not identify: unknown_movie.mkv\nMoved to review directory",
            title="❓ Unidentified Media",
            tags="spindle,unidentified,review",
        )

    @patch.object(NtfyNotifier, "send_notification")
    def test_test_notification(self, mock_send, notifier):
        """Test the test notification method."""
        mock_send.return_value = True

        result = notifier.test_notification()

        assert result is True
        mock_send.assert_called_once_with(
            "Spindle notification system is working correctly!",
            title="🧪 Test Notification",
            tags="spindle,test",
        )


class TestErrorScenarios:
    """Test various error scenarios."""

    def test_all_methods_handle_send_failure(self, notifier):
        """Test that all notification methods handle send_notification failure."""
        with patch.object(notifier, "send_notification", return_value=False):
            assert notifier.notify_disc_detected("Test", "DVD") is False
            assert notifier.notify_rip_started("Test") is False
            assert notifier.notify_rip_completed("Test", "1h") is False
            assert notifier.notify_media_added("Test", "movie") is False
            assert notifier.notify_queue_started(1) is False
            assert notifier.notify_queue_completed(1, 0, "1h") is False
            assert notifier.notify_error("Test error") is False
            assert notifier.notify_unidentified_media("test.mkv") is False
            assert notifier.test_notification() is False

    @patch("spindle.notify.ntfy.httpx.Client")
    def test_client_timeout_configuration(self, mock_client_class, mock_config):
        """Test that client is configured with proper timeout."""
        NtfyNotifier(mock_config)

        mock_client_class.assert_called_once_with(
            timeout=30.0,
            headers={"User-Agent": "Spindle/0.1.0"}
        )


class TestIntegration:
    """Integration-style tests using real httpx client (but mocked responses)."""

    @patch("httpx.Client.post")
    def test_real_client_integration(self, mock_post, notifier):
        """Test with real httpx client but mocked responses."""
        mock_response = Mock()
        mock_response.raise_for_status.return_value = None
        mock_post.return_value = mock_response

        result = notifier.send_notification("Integration test")

        assert result is True
        mock_post.assert_called_once_with(
            "https://ntfy.sh/test-topic",
            data=b"Integration test",
            headers={},
        )

    @patch("httpx.Client.post")
    def test_unicode_emoji_handling(self, mock_post, notifier):
        """Test handling of Unicode emojis in title and message."""
        mock_response = Mock()
        mock_response.raise_for_status.return_value = None
        mock_post.return_value = mock_response

        # Test with emoji in title - should handle encoding gracefully
        result = notifier.send_notification(
            "Disc detected successfully",
            title="💿 Disc Detected",
            tags="test"
        )

        assert result is True
        mock_post.assert_called_once()
        
        call_args = mock_post.call_args
        assert call_args[1]["data"] == b"Disc detected successfully"
        
        # Check that title header was set (either with emoji or fallback)
        headers = call_args[1]["headers"]
        assert "Title" in headers
        assert "Tags" in headers
        assert headers["Tags"] == "test"

    @patch("httpx.Client.post")
    def test_unicode_message_encoding(self, mock_post, notifier):
        """Test proper UTF-8 encoding of message content."""
        mock_response = Mock()
        mock_response.raise_for_status.return_value = None
        mock_post.return_value = mock_response

        # Test with Unicode characters in message
        message_with_unicode = "Movie: 《Amélie》 - 电影"
        result = notifier.send_notification(message_with_unicode)

        assert result is True
        mock_post.assert_called_once_with(
            "https://ntfy.sh/test-topic",
            data=message_with_unicode.encode('utf-8'),
            headers={},
        )
