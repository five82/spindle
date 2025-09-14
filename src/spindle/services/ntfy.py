"""Notification service via ntfy.sh."""

import logging

from spindle.config import SpindleConfig

from .ntfy_impl import NtfyNotifier

logger = logging.getLogger(__name__)


class NotificationService:
    """Clean wrapper for ntfy.sh notifications."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.notifier = NtfyNotifier(config)

    def notify_disc_detected(self, disc_label: str, disc_type: str) -> None:
        """Send notification when disc is detected."""
        try:
            message = f"📀 Disc detected: {disc_label} ({disc_type})"
            self.notifier.send_notification(
                title="Spindle - Disc Detected",
                message=message,
                priority="default",
            )
        except Exception as e:
            logger.warning(f"Failed to send disc detection notification: {e}")

    def notify_identification_complete(self, title: str, media_type: str) -> None:
        """Send notification when identification is complete."""
        try:
            message = f"🎬 Identified: {title} ({media_type})"
            self.notifier.send_notification(
                title="Spindle - Identified",
                message=message,
                priority="default",
            )
        except Exception as e:
            logger.warning(f"Failed to send identification notification: {e}")

    def notify_rip_complete(self, title: str) -> None:
        """Send notification when ripping is complete."""
        try:
            message = f"💿 Rip complete: {title}"
            self.notifier.send_notification(
                title="Spindle - Rip Complete",
                message=message,
                priority="default",
            )
        except Exception as e:
            logger.warning(f"Failed to send rip notification: {e}")

    def notify_encode_complete(self, title: str) -> None:
        """Send notification when encoding is complete."""
        try:
            message = f"🎞️ Encoding complete: {title}"
            self.notifier.send_notification(
                title="Spindle - Encoded",
                message=message,
                priority="default",
            )
        except Exception as e:
            logger.warning(f"Failed to send encoding notification: {e}")

    def notify_processing_complete(self, title: str) -> None:
        """Send notification when full processing is complete."""
        try:
            message = f"✅ Ready to watch: {title}"
            self.notifier.send_notification(
                title="Spindle - Complete",
                message=message,
                priority="high",
            )
        except Exception as e:
            logger.warning(f"Failed to send completion notification: {e}")

    def notify_error(self, error_message: str, context: str | None = None) -> None:
        """Send error notification."""
        try:
            title = "Spindle - Error"
            if context:
                message = f"❌ Error with {context}: {error_message}"
            else:
                message = f"❌ Error: {error_message}"

            self.notifier.send_notification(
                title=title,
                message=message,
                priority="high",
            )
        except Exception as e:
            logger.exception(f"Failed to send error notification: {e}")

    def test_notifications(self) -> bool:
        """Test notification system."""
        try:
            self.notifier.send_notification(
                title="Spindle - Test",
                message="🧪 Notification system test",
                priority="low",
            )
            return True
        except Exception as e:
            logger.exception(f"Notification test failed: {e}")
            return False
