"""Continuous queue processor for automated workflow."""

import asyncio
import logging
import time

from .config import SpindleConfig
from .disc.monitor import DiscMonitor, detect_disc, eject_disc
from .disc.ripper import MakeMKVRipper
from .encode.drapto_wrapper import DraptoEncoder
from .identify.tmdb import MediaIdentifier
from .notify.ntfy import NtfyNotifier
from .organize.library import LibraryOrganizer
from .queue.manager import QueueItemStatus, QueueManager

logger = logging.getLogger(__name__)


class ContinuousProcessor:
    """Manages continuous processing of discs and queue items."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.queue_manager = QueueManager(config)
        self.ripper = MakeMKVRipper(config)
        self.identifier = MediaIdentifier(config)
        self.encoder = DraptoEncoder(config)
        self.organizer = LibraryOrganizer(config)
        self.notifier = NtfyNotifier(config)

        self.disc_monitor: DiscMonitor | None = None
        self.processing_task: asyncio.Task | None = None
        self.is_running = False

    def start(self) -> None:
        """Start the continuous processor."""
        if self.is_running:
            logger.warning("Processor is already running")
            return

        logger.info("Starting continuous processor")
        self.is_running = True

        # Start disc monitoring
        self.disc_monitor = DiscMonitor(
            device=self.config.optical_drive,
            callback=self._on_disc_detected,
        )
        self.disc_monitor.start_monitoring()

        # Start background queue processing
        loop = asyncio.get_event_loop()
        self.processing_task = loop.create_task(self._process_queue_continuously())

        logger.info("Continuous processor started - insert discs to begin")

    def stop(self) -> None:
        """Stop the continuous processor."""
        if not self.is_running:
            return

        logger.info("Stopping continuous processor")
        self.is_running = False

        # Stop disc monitoring
        if self.disc_monitor:
            self.disc_monitor.stop_monitoring()

        # Cancel background processing
        if self.processing_task:
            self.processing_task.cancel()

        logger.info("Continuous processor stopped")

    def _on_disc_detected(self, disc_info) -> None:
        """Handle disc detection by automatically ripping."""
        logger.info(f"Detected disc: {disc_info}")
        self.notifier.notify_disc_detected(disc_info.label, disc_info.disc_type)

        try:
            # Add to queue
            item = self.queue_manager.add_disc(disc_info.label)
            logger.info(f"Added to queue: {item}")

            # Start ripping immediately
            self._rip_disc(item, disc_info)

        except Exception as e:
            logger.error(f"Error handling disc detection: {e}")
            self.notifier.notify_error(f"Failed to process disc: {e}")

    def _rip_disc(self, item, disc_info) -> None:
        """Rip the detected disc."""
        try:
            logger.info(f"Starting rip: {disc_info.label}")
            self.notifier.notify_rip_started(disc_info.label)

            # Update status
            item.status = QueueItemStatus.RIPPING
            self.queue_manager.update_item(item)

            start_time = time.time()

            # Perform the rip
            output_file = self.ripper.rip_disc(self.config.staging_dir)

            # Update item with ripped file
            item.ripped_file = output_file
            item.status = QueueItemStatus.RIPPED
            self.queue_manager.update_item(item)

            # Calculate duration
            duration = time.strftime("%H:%M:%S", time.gmtime(time.time() - start_time))

            logger.info(f"Rip completed: {output_file}")
            self.notifier.notify_rip_completed(disc_info.label, duration)

            # Eject disc
            eject_disc(self.config.optical_drive)
            logger.info("Disc ejected - ready for next disc")

        except Exception as e:
            logger.error(f"Error ripping disc: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            self.notifier.notify_error(f"Ripping failed: {e}", context=disc_info.label)

    async def _process_queue_continuously(self) -> None:
        """Continuously process queue items in the background."""
        logger.info("Started background queue processor")

        while self.is_running:
            try:
                # Get next item to process
                item = self._get_next_processable_item()

                if item:
                    await self._process_single_item(item)
                else:
                    # No items to process, wait a bit
                    await asyncio.sleep(5)

            except asyncio.CancelledError:
                logger.info("Queue processor cancelled")
                break
            except Exception as e:
                logger.error(f"Error in queue processor: {e}")
                await asyncio.sleep(10)  # Wait before retrying

        logger.info("Background queue processor stopped")

    def _get_next_processable_item(self):
        """Get the next item that needs processing."""
        # Get items that are ready for the next step
        processable_statuses = [
            QueueItemStatus.RIPPED,      # Ready for identification
            QueueItemStatus.IDENTIFIED,  # Ready for encoding
            QueueItemStatus.ENCODED,      # Ready for organization
        ]

        for status in processable_statuses:
            items = self.queue_manager.get_items_by_status(status)
            if items:
                return items[0]  # Return oldest item

        return None

    async def _process_single_item(self, item) -> None:
        """Process a single queue item through its next stage."""
        try:
            logger.info(f"Processing: {item}")

            if item.status == QueueItemStatus.RIPPED:
                await self._identify_item(item)
            elif item.status == QueueItemStatus.IDENTIFIED:
                await self._encode_item(item)
            elif item.status == QueueItemStatus.ENCODED:
                await self._organize_item(item)

        except Exception as e:
            logger.error(f"Error processing {item}: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            self.notifier.notify_error(f"Processing failed: {e}", context=str(item))

    async def _identify_item(self, item) -> None:
        """Identify media for a ripped item."""
        logger.info(f"Identifying: {item.ripped_file.name}")

        item.status = QueueItemStatus.IDENTIFYING
        self.queue_manager.update_item(item)

        # Identify the media
        media_info = await self.identifier.identify_media(item.ripped_file)

        if media_info:
            item.media_info = media_info
            item.status = QueueItemStatus.IDENTIFIED
            logger.info(f"Identified: {media_info}")
        else:
            # Move to review directory
            self.organizer.create_review_directory(item.ripped_file, "unidentified")
            item.status = QueueItemStatus.REVIEW
            self.notifier.notify_unidentified_media(item.ripped_file.name)
            logger.warning(f"Could not identify: {item.ripped_file.name}")

        self.queue_manager.update_item(item)

    async def _encode_item(self, item) -> None:
        """Encode a identified item."""
        logger.info(f"Encoding: {item.media_info}")
        # Drapto handles encoding notifications, so spindle doesn't send duplicates

        item.status = QueueItemStatus.ENCODING
        self.queue_manager.update_item(item)

        # Encode the file
        result = self.encoder.encode_file(
            item.ripped_file,
            self.config.staging_dir / "encoded",
        )

        if result.success:
            item.encoded_file = result.output_file
            item.status = QueueItemStatus.ENCODED
            # Drapto already sent encoding completion notification
            logger.info(f"Encoded: {result.output_file}")
        else:
            item.status = QueueItemStatus.FAILED
            item.error_message = result.error_message
            self.notifier.notify_error(f"Encoding failed: {result.error_message}")
            logger.error(f"Encoding failed: {result.error_message}")

        self.queue_manager.update_item(item)

    async def _organize_item(self, item) -> None:
        """Organize and import an encoded item."""
        logger.info(f"Organizing: {item.media_info}")

        item.status = QueueItemStatus.ORGANIZING
        self.queue_manager.update_item(item)

        # Organize and import to Plex
        if self.organizer.add_to_plex(item.encoded_file, item.media_info):
            item.status = QueueItemStatus.COMPLETED
            self.notifier.notify_media_added(
                str(item.media_info),
                item.media_info.media_type,
            )
            logger.info(f"Added to Plex: {item.media_info}")
        else:
            item.status = QueueItemStatus.FAILED
            item.error_message = "Failed to organize/import to Plex"
            self.notifier.notify_error("Failed to organize/import to Plex")
            logger.error("Failed to organize/import to Plex")

        self.queue_manager.update_item(item)

    def get_status(self) -> dict:
        """Get current processor status."""
        stats = self.queue_manager.get_queue_stats()

        # Check for current disc
        current_disc = detect_disc(self.config.optical_drive)

        return {
            "running": self.is_running,
            "current_disc": str(current_disc) if current_disc else None,
            "queue_stats": stats,
            "total_items": sum(stats.values()) if stats else 0,
        }
