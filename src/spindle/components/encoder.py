"""Encoding component coordination."""

import logging
from pathlib import Path

from ..config import SpindleConfig
from ..error_handling import ToolError
from ..services.drapto import DraptoService
from ..storage.queue import QueueItem, QueueItemStatus

logger = logging.getLogger(__name__)

class EncoderComponent:
    """Coordinates video encoding operations."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.drapto_service = DraptoService(config)
        self.queue_manager = None  # Will be injected by orchestrator

    async def encode_item(self, item: QueueItem) -> None:
        """Encode a ripped item."""
        try:
            logger.info(f"Starting encoding for: {item}")

            if not item.ripped_file or not item.ripped_file.exists():
                raise ToolError(f"Ripped file not found: {item.ripped_file}")

            # Update status to encoding
            item.status = QueueItemStatus.ENCODING
            item.progress_stage = "Encoding video"
            item.progress_percent = 0
            self.queue_manager.update_item(item)

            # Determine output file path
            encoded_dir = self.config.staging_dir / "encoded"
            encoded_dir.mkdir(parents=True, exist_ok=True)

            # Generate output filename (replace extension)
            output_file = encoded_dir / f"{item.ripped_file.stem}_encoded.mkv"

            # Progress callback for encoding
            def progress_callback(stage: str, percent: int, message: str) -> None:
                item.progress_stage = stage
                item.progress_percent = percent
                item.progress_message = message
                self.queue_manager.update_item(item)

            # Perform encoding
            logger.info(f"Encoding {item.ripped_file} -> {output_file}")
            encode_result = await self.drapto_service.encode_file(
                input_file=item.ripped_file,
                output_file=output_file,
                progress_callback=progress_callback
            )

            if not encode_result.success:
                raise ToolError(f"Encoding failed: {encode_result.error_message}")

            # Store encoded file path
            item.encoded_file = output_file
            item.status = QueueItemStatus.ENCODED
            item.progress_stage = "Encoding completed"
            item.progress_percent = 100
            item.progress_message = f"Encoded to {output_file.name}"

            self.queue_manager.update_item(item)
            logger.info(f"Successfully encoded: {output_file}")

        except Exception as e:
            logger.exception(f"Error encoding item: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            raise

    def set_queue_manager(self, queue_manager):
        """Inject queue manager dependency."""
        self.queue_manager = queue_manager
