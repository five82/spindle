"""MakeMKV service wrapper."""

import asyncio
import logging
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import Any

from spindle.config import SpindleConfig
from spindle.disc.rip_spec import RipSpec
from spindle.disc.ripper import MakeMKVRipper

logger = logging.getLogger(__name__)


class MakeMKVService:
    """Async wrapper for MakeMKV disc ripping."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.ripper = MakeMKVRipper(config)

    async def scan_disc(self, device: str) -> dict[str, Any]:
        """Scan disc and return title information."""
        try:
            logger.info(f"Scanning disc: {device}")

            loop = asyncio.get_running_loop()
            titles, raw_output = await loop.run_in_executor(
                None,
                self.ripper.scan_disc_with_output,
                device,
            )

            if not titles:
                msg = "MakeMKV disc scan failed"
                raise RuntimeError(msg)

            fingerprint = self.ripper.extract_disc_fingerprint(raw_output)
            if not fingerprint:
                logger.critical(
                    "MakeMKV did not return a disc fingerprint for %s",
                    device,
                )
                msg = "MakeMKV did not provide a disc fingerprint"
                raise RuntimeError(msg)

            return {
                "device": device,
                "titles": titles,
                "disc_info": {"device": device},
                "fingerprint": fingerprint,
                "makemkv_output": raw_output,
            }

        except Exception as e:
            logger.exception(f"Disc scan failed: {e}")
            raise

    async def rip_disc(
        self,
        rip_spec: RipSpec,
        progress_callback: Callable[[str, int, str], None] | None = None,
    ) -> list[Path]:
        """Rip disc based on rip specification - this is what DiscHandler expects."""
        try:
            device = rip_spec.device or rip_spec.disc_info.device
            output_dir = rip_spec.output_dir or self.config.staging_dir / "ripped"

            logger.info(f"Ripping disc from {device} to {output_dir}")

            # Ensure output directory exists
            output_dir.mkdir(parents=True, exist_ok=True)

            # The ripper's rip_disc method is synchronous, run in executor
            ripped_file = await asyncio.get_running_loop().run_in_executor(
                None,
                self.ripper.rip_disc,
                output_dir,
                device,
            )

            logger.info(f"Ripped file: {ripped_file}")
            return [ripped_file]  # Return as list as expected by DiscHandler

        except Exception as e:
            logger.exception(f"Disc ripping failed: {e}")
            raise

    async def rip_titles(
        self,
        device: str,
        titles: list[dict],
        output_directory: Path,
        progress_callback: Callable[[str, int, str], None] | None = None,
    ) -> list[Path]:
        """Rip specified titles from disc."""
        try:
            logger.info(f"Ripping {len(titles)} titles from {device}")

            # Ensure output directory exists
            output_directory.mkdir(parents=True, exist_ok=True)

            # If no specific titles provided, rip main title
            if not titles:
                ripped_file = await asyncio.get_running_loop().run_in_executor(
                    None,
                    self.ripper.rip_disc,
                    output_directory,
                    device,
                )
                return [ripped_file]

            # Rip each specified title
            ripped_files = []
            for title_info in titles:
                # For now, assume title_info is a Title object or has compatible interface
                # This would need proper implementation based on actual title structure
                logger.info(f"Ripping title: {title_info}")

                # Note: This assumes the ripper can handle individual titles
                # The actual implementation would depend on how titles are structured
                ripped_file = await asyncio.get_running_loop().run_in_executor(
                    None,
                    self.ripper.rip_title,
                    title_info,
                    output_directory,
                    device,
                    progress_callback,
                )
                ripped_files.append(ripped_file)

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
                check=False,
                capture_output=True,
                text=True,
                timeout=10,
            )
            return result.returncode == 0
        except (subprocess.SubprocessError, FileNotFoundError):
            return False

    def get_disc_info(self, device: str) -> dict[str, Any] | None:
        """Get basic disc information."""
        try:
            # Use the ripper's scan functionality
            titles = self.ripper.scan_disc(device)
            return {
                "device": device,
                "title_count": len(titles),
                "titles": titles,
            }
        except Exception as e:
            logger.warning(f"Failed to get disc info: {e}")
            return None
