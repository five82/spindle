"""Plex media server integration service."""

import logging
from collections.abc import Callable
from pathlib import Path
from typing import Any

from spindle.config import SpindleConfig

from .plex_impl import LibraryOrganizer
from .tmdb_impl import MediaInfo

logger = logging.getLogger(__name__)


class PlexService:
    """Clean wrapper for Plex library management."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.organizer = LibraryOrganizer(config)

    async def organize_media(
        self,
        source_file: Path,
        media_info: MediaInfo,
        progress_callback: Callable[[str, int, str], None] | None = None,
    ) -> Path:
        """Organize media file into Plex library structure."""
        try:
            logger.info(f"Organizing media: {source_file} -> {media_info.title}")

            if progress_callback:
                progress_callback("Organizing", 10, "Determining library path")

            # Organize into appropriate library structure
            final_path = await self.organizer.organize_media_file(
                source_file=source_file,
                media_info=media_info,
                progress_callback=progress_callback,
            )

            logger.info(f"Media organized to: {final_path}")
            return final_path

        except Exception as e:
            logger.exception(f"Library organization failed: {e}")
            raise

    async def refresh_library(self, content_type: str) -> None:
        """Trigger Plex library refresh for content type."""
        try:
            logger.info(f"Refreshing Plex library for: {content_type}")

            if content_type == "movie":
                await self.organizer.refresh_movie_library()
            elif content_type == "tv_series":
                await self.organizer.refresh_tv_library()
            else:
                logger.warning(f"Unknown content type for refresh: {content_type}")

        except Exception as e:
            logger.warning(f"Plex library refresh failed: {e}")
            # Don't raise - library refresh is not critical

    def test_connection(self) -> bool:
        """Test Plex server connection."""
        return self.organizer.test_plex_connection()

    def get_library_stats(self) -> dict[str, Any]:
        """Get Plex library statistics."""
        return self.organizer.get_library_info()
