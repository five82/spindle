"""MakeMKV service wrapper."""

import logging
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import Any

from ..config import SpindleConfig
from ..disc.ripper import MakeMKVRipper

logger = logging.getLogger(__name__)


class MakeMKVService:
    """Clean wrapper for MakeMKV disc ripping."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.ripper = MakeMKVRipper(config)

    async def scan_disc(self, device: str) -> dict[str, Any]:
        """Scan disc and return title information."""
        try:
            logger.info(f"Scanning disc: {device}")

            scan_result = await self.ripper.scan_disc(device)

            if not scan_result:
                raise RuntimeError("MakeMKV disc scan failed")

            return {
                "device": device,
                "titles": scan_result.get("titles", []),
                "disc_info": scan_result.get("disc_info", {}),
            }

        except Exception as e:
            logger.exception(f"Disc scan failed: {e}")
            raise

    async def rip_titles(
        self,
        device: str,
        titles: list[dict],
        output_directory: Path,
        progress_callback: Callable[[str, int, str], None] | None = None
    ) -> list[Path]:
        """Rip specified titles from disc."""
        try:
            logger.info(f"Ripping {len(titles)} titles from {device}")

            # Ensure output directory exists
            output_directory.mkdir(parents=True, exist_ok=True)

            ripped_files = await self.ripper.rip_titles(
                device=device,
                titles=titles,
                output_directory=output_directory,
                progress_callback=progress_callback
            )

            logger.info(f"Ripped {len(ripped_files)} files")
            return ripped_files

        except Exception as e:
            logger.exception(f"Title ripping failed: {e}")
            raise

    def validate_makemkv_available(self) -> bool:
        """Check if MakeMKV is available and licensed."""
        try:
            result = subprocess.run(
                ["makemkvcon", "--version"],
                capture_output=True,
                text=True,
                timeout=10
            )
            return result.returncode == 0
        except (subprocess.SubprocessError, FileNotFoundError):
            return False

    def get_disc_info(self, device: str) -> dict[str, Any] | None:
        """Get basic disc information."""
        try:
            return self.ripper.get_disc_info(device)
        except Exception as e:
            logger.warning(f"Failed to get disc info: {e}")
            return None
