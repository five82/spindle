"""Drapto encoding service wrapper."""

import logging
from collections.abc import Callable
from pathlib import Path
from typing import Any

from ..config import SpindleConfig
from ..encode.drapto_wrapper import DraptoEncoder, EncodeResult

logger = logging.getLogger(__name__)


class DraptoService:
    """Clean wrapper for Drapto AV1 encoding."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.encoder = DraptoEncoder(config)

    async def encode_file(
        self,
        input_file: Path,
        output_file: Path,
        progress_callback: Callable[[str, int, str], None] | None = None
    ) -> EncodeResult:
        """Encode video file with AV1."""
        try:
            logger.info(f"Starting encoding: {input_file} -> {output_file}")

            # Validate input
            if not input_file.exists():
                raise FileNotFoundError(f"Input file not found: {input_file}")

            # Ensure output directory exists
            output_file.parent.mkdir(parents=True, exist_ok=True)

            # Perform encoding
            result = await self.encoder.encode_file(
                input_file=input_file,
                output_file=output_file,
                progress_callback=progress_callback
            )

            if result.success:
                logger.info(f"Encoding completed: {output_file}")
            else:
                logger.error(f"Encoding failed: {result.error_message}")

            return result

        except Exception as e:
            logger.exception(f"Encoding service error: {e}")
            return EncodeResult(
                success=False,
                input_file=input_file,
                output_file=output_file,
                error_message=str(e)
            )

    def validate_drapto_available(self) -> bool:
        """Check if drapto is available and working."""
        return self.encoder.validate_drapto_installation()

    def get_encoder_info(self) -> dict[str, Any]:
        """Get information about the encoder."""
        return {
            "drapto_available": self.validate_drapto_available(),
            "config": {
                "quality": self.config.drapto_quality,
                "preset": self.config.drapto_preset,
            }
        }
