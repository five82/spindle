"""Main workflow orchestration for Spindle."""

import asyncio
import logging

from spindle.components.disc_handler import DiscHandler
from spindle.components.encoder import EncoderComponent
from spindle.components.organizer import OrganizerComponent
from spindle.config import SpindleConfig
from spindle.disc.monitor import DiscInfo, DiscMonitor, detect_disc
from spindle.services.ntfy import NotificationService
from spindle.storage.queue import QueueItem, QueueItemStatus, QueueManager

logger = logging.getLogger(__name__)


class SpindleOrchestrator:
    """Orchestrates the complete Spindle workflow."""

    def __init__(self, config: SpindleConfig):
        self.config = config

        # Core components
        self.queue_manager = QueueManager(config)
        self.disc_handler = DiscHandler(config)
        self.encoder = EncoderComponent(config)
        self.organizer = OrganizerComponent(config)
        self.notifier = NotificationService(config)

        # Monitoring
        self.disc_monitor: DiscMonitor | None = None
        self.processing_task: asyncio.Task | None = None
        self.is_running = False

    def start(self) -> None:
        """Start the orchestrator."""
        if self.is_running:
            logger.warning("Orchestrator is already running")
            return

        logger.info("Starting Spindle orchestrator")
        self.is_running = True

        # Reset any stuck items from previous runs
        reset_count = self.queue_manager.reset_stuck_processing_items()
        if reset_count > 0:
            logger.info("Reset %s stuck items to pending status", reset_count)

        # Start disc monitoring
        self.disc_monitor = DiscMonitor(
            device=self.config.optical_drive,
            callback=self._on_disc_detected,
        )
        self.disc_monitor.start_monitoring()

        # Start background processing
        loop = asyncio.get_event_loop()
        self.processing_task = loop.create_task(self._process_queue_continuously())

        # Check for existing disc
        existing_disc = detect_disc(self.config.optical_drive)
        if existing_disc:
            logger.info("Found existing disc: %s", existing_disc)
            self._on_disc_detected(existing_disc)

        logger.info("Orchestrator started - ready for discs")

    def stop(self) -> None:
        """Stop the orchestrator."""
        if not self.is_running:
            return

        logger.info("Stopping orchestrator")
        self.is_running = False

        if self.disc_monitor:
            self.disc_monitor.stop_monitoring()

        if self.processing_task:
            self.processing_task.cancel()

        logger.info("Orchestrator stopped")

    def _on_disc_detected(self, disc_info: DiscInfo) -> None:
        """Handle disc detection."""
        logger.info("Detected disc: %s", disc_info)
        self.notifier.notify_disc_detected(disc_info.label, disc_info.disc_type)

        try:
            # Add to queue and start identification
            item = self.queue_manager.add_disc(disc_info.label)
            logger.info("Added to queue: %s", item)

            # Schedule identification task
            loop = asyncio.get_event_loop()
            task = loop.create_task(self.disc_handler.identify_disc(item, disc_info))

            def handle_completion(t):
                if not t.cancelled() and t.exception():
                    logger.exception(
                        "Disc identification failed",
                        exc_info=t.exception(),
                    )
                    self.notifier.notify_error(
                        f"Failed to identify disc: {t.exception()}",
                        context=disc_info.label,
                    )

            task.add_done_callback(handle_completion)

        except Exception as e:
            logger.exception("Error handling disc detection")
            self.notifier.notify_error(
                f"Failed to process disc: {e}",
                context=disc_info.label,
            )

    async def _process_queue_continuously(self) -> None:
        """Continuously process queue items."""
        logger.info("Started background queue processor")

        while self.is_running:
            try:
                item = self._get_next_processable_item()

                if item:
                    await self._process_single_item(item)
                else:
                    await asyncio.sleep(self.config.queue_poll_interval)

            except asyncio.CancelledError:
                logger.info("Queue processor cancelled")
                break
            except Exception as e:
                logger.exception(f"Error in queue processor: {e}")
                await asyncio.sleep(self.config.error_retry_interval)

        logger.info("Background queue processor stopped")

    def _get_next_processable_item(self) -> QueueItem | None:
        """Get the next item that needs processing."""
        processable_statuses = [
            QueueItemStatus.IDENTIFIED,  # Ready for ripping
            QueueItemStatus.RIPPED,  # Ready for encoding
            QueueItemStatus.ENCODED,  # Ready for organization
        ]

        for status in processable_statuses:
            items = self.queue_manager.get_items_by_status(status)
            if items:
                return items[0]

        return None

    async def _process_single_item(self, item: QueueItem) -> None:
        """Process a single queue item through its next stage."""
        try:
            logger.info(f"Processing: {item}")

            if item.status == QueueItemStatus.IDENTIFIED:
                await self.disc_handler.rip_identified_item(item)
            elif item.status == QueueItemStatus.RIPPED:
                await self.encoder.encode_item(item)
            elif item.status == QueueItemStatus.ENCODED:
                await self.organizer.organize_item(item)

        except Exception as e:
            logger.exception(f"Error processing {item}: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)

    def get_status(self) -> dict:
        """Get current orchestrator status."""
        stats = self.queue_manager.get_queue_stats()

        current_disc_name = None
        current_disc = detect_disc(self.config.optical_drive)
        if current_disc:
            # Try to find identified name from processing items
            processing_items = []
            for status in [
                QueueItemStatus.PENDING,
                QueueItemStatus.IDENTIFYING,
                QueueItemStatus.IDENTIFIED,
                QueueItemStatus.RIPPING,
            ]:
                processing_items.extend(self.queue_manager.get_items_by_status(status))

            for item in processing_items:
                if item.media_info and item.disc_title != current_disc.label:
                    current_disc_name = item.disc_title
                    break

            if not current_disc_name:
                current_disc_name = str(current_disc)

        return {
            "running": self.is_running,
            "current_disc": current_disc_name,
            "queue_stats": stats,
            "total_items": sum(stats.values()) if stats else 0,
        }
