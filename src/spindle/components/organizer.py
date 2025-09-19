"""Library organization component coordination."""

import logging

from spindle.config import SpindleConfig
from spindle.error_handling import ToolError
from spindle.services.plex import PlexService
from spindle.storage.queue import QueueItem, QueueItemStatus

logger = logging.getLogger(__name__)


class OrganizerComponent:
    """Coordinates library organization and Plex integration."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.plex_service = PlexService(config)
        self.queue_manager = None  # Will be injected by orchestrator

    def organize_item(self, item: QueueItem) -> None:
        """Organize an encoded item into the library."""
        try:
            logger.info(f"Starting organization for: {item}")

            if not item.encoded_file or not item.encoded_file.exists():
                msg = f"Encoded file not found: {item.encoded_file}"
                raise ToolError(msg)

            # Update status to organizing
            item.status = QueueItemStatus.ORGANIZING
            item.progress_stage = "Organizing library"
            item.progress_percent = 0
            self.queue_manager.update_item(item)

            # Determine library organization structure
            if not item.media_info:
                msg = "No media info available for organization"
                raise ToolError(msg)

            item.progress_percent = 20
            item.progress_message = "Creating library structure"
            self.queue_manager.update_item(item)

            # Organize into library
            final_file_path = self.plex_service.organize_media(
                source_file=item.encoded_file,
                media_info=item.media_info,
                progress_callback=self._create_progress_callback(item),
            )

            # Store final file path
            item.final_file = final_file_path

            # Trigger Plex library scan
            item.progress_percent = 80
            item.progress_message = "Updating Plex library"
            self.queue_manager.update_item(item)

            self.plex_service.refresh_library(item.media_info.content_type)

            # Mark as completed
            item.status = QueueItemStatus.COMPLETED
            item.progress_stage = "Organization completed"
            item.progress_percent = 100
            item.progress_message = f"Available in library: {final_file_path.name}"

            self.queue_manager.update_item(item)
            logger.info(f"Successfully organized: {final_file_path}")

        except Exception as e:
            logger.exception(f"Error organizing item: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            raise

    def _create_progress_callback(self, item: QueueItem):
        """Create progress callback for organization operations."""

        def progress_callback(stage: str, percent: int, message: str) -> None:
            # Scale progress to 20-80% range (organization portion)
            scaled_percent = 20 + int(percent * 0.6)
            item.progress_stage = stage
            item.progress_percent = scaled_percent
            item.progress_message = message
            self.queue_manager.update_item(item)

        return progress_callback

    def set_queue_manager(self, queue_manager):
        """Inject queue manager dependency."""
        self.queue_manager = queue_manager
