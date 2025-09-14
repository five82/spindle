"""Library organization and Plex integration."""

import logging
import shutil
from collections.abc import Callable
from pathlib import Path
from typing import Any

from plexapi.server import PlexServer  # type: ignore[import-untyped]

from spindle.config import SpindleConfig

from .tmdb import MediaInfo

logger = logging.getLogger(__name__)


class LibraryOrganizer:
    """Organizes media files and manages Plex library integration."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.library_dir = config.library_dir
        self.plex_url = config.plex_url
        self.plex_token = config.plex_token
        self.plex_server: PlexServer | None = None

        # Initialize Plex connection if configured
        if config.plex_url and config.plex_token:
            try:
                self.plex_server = PlexServer(config.plex_url, config.plex_token)
                logger.info(
                    "Connected to Plex server: %s",
                    self.plex_server.friendlyName,
                )
            except Exception as e:
                logger.warning("Failed to connect to Plex server: %s", e)
                self.plex_server = None

    def generate_filename(self, media_info: MediaInfo) -> str:
        """Generate filename for media (wrapper for MediaInfo.get_filename)."""
        return media_info.get_filename()

    def get_target_directory(self, media_info: MediaInfo) -> Path:
        """Get target directory for media (wrapper for MediaInfo.get_library_path)."""
        return media_info.get_library_path(
            self.config.library_dir,
            self.config.movies_dir,
            self.config.tv_dir,
        )

    def sanitize_filename(self, filename: str) -> str:
        """Sanitize filename for filesystem compatibility."""
        import re

        # Remove or replace problematic characters
        safe_name = re.sub(r'[<>:"/\\|?*]', "_", filename)
        # Remove control characters
        safe_name = re.sub(r"[\x00-\x1f\x7f-\x9f]", "", safe_name)
        # Trim whitespace and dots
        return safe_name.strip(" .")

    def trigger_library_scan(self) -> bool:
        """Trigger a general Plex library scan."""
        if not self.plex_server:
            logger.warning("Plex server not configured, skipping library scan")
            return False

        try:
            # Scan all libraries
            for section in self.plex_server.library.sections():
                section.update()
            logger.info("Triggered Plex library scan for all sections")
            return True
        except Exception as e:
            logger.exception(f"Failed to trigger Plex library scan: {e}")
            return False

    def ensure_library_structure(self) -> None:
        """Ensure basic library directory structure exists."""
        # Create base directories
        self.config.library_dir.mkdir(parents=True, exist_ok=True)
        (self.config.library_dir / self.config.movies_dir).mkdir(exist_ok=True)
        (self.config.library_dir / self.config.tv_dir).mkdir(exist_ok=True)

    def organize_media(self, video_file: Path, media_info: MediaInfo) -> Path:
        """Organize a video file into the library structure."""

        # Generate target directory based on media type
        target_dir = media_info.get_library_path(
            self.config.library_dir,
            self.config.movies_dir,
            self.config.tv_dir,
        )

        # Ensure target directory exists
        target_dir.mkdir(parents=True, exist_ok=True)

        # Generate final filename
        filename = media_info.get_filename() + video_file.suffix
        target_file = target_dir / filename

        # Handle file conflicts
        if target_file.exists():
            counter = 1
            base_name = media_info.get_filename()
            while target_file.exists():
                filename = f"{base_name} ({counter}){video_file.suffix}"
                target_file = target_dir / filename
                counter += 1

        logger.info("Moving %s -> %s", video_file, target_file)

        try:
            # Move the file to the target location
            shutil.move(str(video_file), str(target_file))
            logger.info("Successfully organized: %s", target_file)

            return target_file

        except Exception as e:
            logger.exception(f"Failed to move file: {e}")
            raise

    def scan_plex_library(self, media_info: MediaInfo) -> bool:
        """Trigger a Plex library scan for the specific media type."""
        if not self.plex_server:
            logger.warning("Plex server not configured, skipping library scan")
            return False

        try:
            # Determine which library to scan
            library_name = (
                self.config.movies_library
                if media_info.is_movie
                else self.config.tv_library
            )

            # Get the library
            library = self.plex_server.library.section(library_name)

            # Trigger scan
            library.update()
            logger.info(f"Triggered Plex scan for {library_name} library")

            return True

        except Exception as e:
            logger.exception(f"Failed to trigger Plex library scan: {e}")
            return False

    def add_to_plex(self, video_file: Path, media_info: MediaInfo) -> bool:
        """Add media to Plex library (organize + scan)."""
        try:
            # Organize the file
            self.organize_media(video_file, media_info)

            # Trigger Plex scan
            self.scan_plex_library(media_info)

            logger.info(f"Successfully added {media_info.title} to library")
            return True

        except Exception as e:
            logger.exception(f"Failed to add {media_info.title} to library: {e}")
            return False

    def create_review_directory(
        self,
        video_file: Path,
        reason: str = "unidentified",
    ) -> Path:
        """Move unidentified media to review directory."""
        review_dir = self.config.review_dir / reason
        review_dir.mkdir(parents=True, exist_ok=True)

        # Generate unique filename in review directory
        target_file = review_dir / video_file.name
        counter = 1

        while target_file.exists():
            stem = video_file.stem
            suffix = video_file.suffix
            target_file = review_dir / f"{stem}_{counter}{suffix}"
            counter += 1

        logger.info(f"Moving unidentified media to review: {target_file}")

        try:
            shutil.move(str(video_file), str(target_file))
            return target_file
        except Exception as e:
            logger.exception(f"Failed to move file to review directory: {e}")
            raise

    def verify_plex_connection(self) -> bool:
        """Verify that Plex connection is working."""
        if not self.plex_server:
            return False

        try:
            # Try to get server info
            _ = self.plex_server.friendlyName
            return True
        except Exception as e:
            logger.exception(f"Plex connection verification failed: {e}")
            return False

    def get_plex_libraries(self) -> list[str]:
        """Get list of available Plex libraries."""
        if not self.plex_server:
            return []

        try:
            libraries = []
            for section in self.plex_server.library.sections():
                libraries.append(section.title)
            return libraries
        except Exception as e:
            logger.exception(f"Failed to get Plex libraries: {e}")
            return []

    def wait_for_plex_scan(self, media_info: MediaInfo, timeout: int = 60) -> bool:
        """Wait for Plex to complete scanning and find the new media."""
        if not self.plex_server:
            return False

        try:
            library_name = (
                self.config.movies_library
                if media_info.is_movie
                else self.config.tv_library
            )

            library = self.plex_server.library.section(library_name)

            # Simple check - try to find the media by title
            # This is a basic implementation, could be enhanced
            import time

            start_time = time.time()

            while time.time() - start_time < timeout:
                try:
                    if media_info.is_movie:
                        results = library.search(
                            title=media_info.title,
                            year=media_info.year,
                        )
                    else:
                        results = library.search(title=media_info.title)

                    if results:
                        logger.info(f"Found {media_info.title} in Plex library")
                        return True

                except Exception as e:
                    logger.debug(f"Plex search failed for {media_info.title}: {e}")
                    # Continue polling - might be temporary connection issue

                time.sleep(self.config.plex_scan_interval)  # Check based on config

            logger.warning(f"Timeout waiting for {media_info.title} to appear in Plex")
            return False

        except Exception as e:
            logger.exception(f"Error waiting for Plex scan: {e}")
            return False

    def organize_media_file(
        self,
        source_file: Path,
        media_info: MediaInfo,
        progress_callback: Callable[[str, int, str], None] | None = None,
    ) -> Path:
        """Organize media file into proper library structure."""
        if progress_callback:
            progress_callback("Organizing", 50, "Moving file to library")

        return self.organize_media(source_file, media_info)

    def refresh_movie_library(self) -> None:
        """Refresh Plex movie library."""
        if not self.plex_server:
            logger.warning("Plex server not configured")
            return

        try:
            library = self.plex_server.library.section(self.config.movies_library)
            library.update()
            logger.info("Refreshed movie library")
        except Exception as e:
            logger.warning(f"Failed to refresh movie library: {e}")

    def refresh_tv_library(self) -> None:
        """Refresh Plex TV library."""
        if not self.plex_server:
            logger.warning("Plex server not configured")
            return

        try:
            library = self.plex_server.library.section(self.config.tv_library)
            library.update()
            logger.info("Refreshed TV library")
        except Exception as e:
            logger.warning(f"Failed to refresh TV library: {e}")

    def test_plex_connection(self) -> bool:
        """Test Plex connection."""
        return self.verify_plex_connection()

    def get_library_info(self) -> dict[str, Any]:
        """Get Plex library information."""
        if not self.plex_server:
            return {"connected": False, "libraries": []}

        return {
            "connected": True,
            "server_name": self.plex_server.friendlyName,
            "libraries": self.get_plex_libraries(),
        }


class PlexService:
    """Consolidated Plex service combining wrapper and implementation."""

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
            final_path = self.organizer.organize_media_file(
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
                self.organizer.refresh_movie_library()
            elif content_type == "tv_series":
                self.organizer.refresh_tv_library()
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


# Re-export key classes for compatibility
__all__ = ["LibraryOrganizer", "PlexService"]
