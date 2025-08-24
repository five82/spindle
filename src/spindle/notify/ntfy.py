"""ntfy.sh notification integration."""

import logging

import httpx

from spindle.config import SpindleConfig

logger = logging.getLogger(__name__)


class NtfyNotifier:
    """Sends notifications via ntfy.sh service."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.topic_url = config.ntfy_topic
        self.client = httpx.Client(
            timeout=config.ntfy_request_timeout,
            headers={"User-Agent": "Spindle/0.1.0"}
        )

    def send_notification(
        self,
        message: str,
        title: str | None = None,
        priority: str = "default",
        tags: str | None = None,
    ) -> bool:
        """Send a notification via ntfy."""
        if not self.topic_url:
            logger.debug("No ntfy topic configured, skipping notification")
            return False

        try:
            headers = {}

            if title:
                # Encode header value to handle unicode characters
                try:
                    headers["Title"] = title.encode('latin1').decode('latin1')
                except UnicodeEncodeError:
                    # If latin1 fails, use a fallback without emojis
                    headers["Title"] = title.encode('ascii', errors='ignore').decode('ascii')

            if priority != "default":
                headers["Priority"] = priority

            if tags:
                headers["Tags"] = tags

            response = self.client.post(
                self.topic_url,
                data=message.encode('utf-8'),
                headers=headers,
            )

            response.raise_for_status()
            logger.debug(f"Sent notification: {title or message[:50]}")
            return True

        except httpx.RequestError as e:
            logger.exception(f"Failed to send notification: {e}")
            return False
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"Notification service error {e.response.status_code}: {e.response.text}",
            )
            return False

    def notify_disc_detected(self, disc_title: str, disc_type: str) -> bool:
        """Send notification when a disc is detected."""
        return self.send_notification(
            f"Detected {disc_type} disc: {disc_title}",
            title="💿 Disc Detected",
            tags="spindle,disc,detected",
        )

    def notify_rip_started(self, disc_title: str) -> bool:
        """Send notification when ripping starts."""
        return self.send_notification(
            f"Started ripping: {disc_title}",
            title="🎬 Ripping Started",
            tags="spindle,rip,started",
        )

    def notify_rip_completed(self, disc_title: str, duration: str) -> bool:
        """Send notification when ripping completes."""
        return self.send_notification(
            f"Completed ripping: {disc_title} (took {duration})",
            title="✅ Ripping Complete",
            tags="spindle,rip,completed",
        )

    def notify_media_added(self, title: str, media_type: str) -> bool:
        """Send notification when media is added to Plex."""
        return self.send_notification(
            f"Added to Plex: {title}",
            title=f"📚 {media_type.title()} Added",
            tags="spindle,plex,added",
        )

    def notify_queue_started(self, count: int) -> bool:
        """Send notification when queue processing starts."""
        return self.send_notification(
            f"Started processing queue with {count} items",
            title="🔄 Queue Processing Started",
            tags="spindle,queue,started",
        )

    def notify_queue_completed(
        self,
        processed: int,
        failed: int,
        duration: str,
    ) -> bool:
        """Send notification when queue processing completes."""
        if failed == 0:
            message = (
                f"Queue processing complete: {processed} items processed in {duration}"
            )
            title = "✅ Queue Complete"
        else:
            message = f"Queue processing complete: {processed} succeeded, {failed} failed in {duration}"
            title = "⚠️ Queue Complete (with errors)"

        return self.send_notification(
            message,
            title=title,
            tags="spindle,queue,completed",
        )

    def notify_error(self, error_message: str, context: str | None = None) -> bool:
        """Send error notification."""
        message = f"Error: {error_message}"
        if context:
            message += f"\nContext: {context}"

        return self.send_notification(
            message,
            title="❌ Spindle Error",
            priority="high",
            tags="spindle,error,alert",
        )

    def notify_unidentified_media(self, filename: str) -> bool:
        """Send notification for unidentified media."""
        return self.send_notification(
            f"Could not identify: {filename}\nMoved to review directory",
            title="❓ Unidentified Media",
            tags="spindle,unidentified,review",
        )

    def test_notification(self) -> bool:
        """Send a test notification."""
        return self.send_notification(
            "Spindle notification system is working correctly!",
            title="🧪 Test Notification",
            tags="spindle,test",
        )
