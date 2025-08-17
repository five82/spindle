"""Library organization and Plex integration."""

import logging
import shutil
from pathlib import Path

from plexapi.server import PlexServer

from ..config import SpindleConfig
from ..identify.tmdb import MediaInfo

logger = logging.getLogger(__name__)


class LibraryOrganizer:
    """Organizes media files and manages Plex library integration."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.plex_server: PlexServer | None = None

        # Initialize Plex connection if configured
        if config.plex_url and config.plex_token:
            try:
                self.plex_server = PlexServer(config.plex_url, config.plex_token)
                logger.info(f"Connected to Plex server: {self.plex_server.friendlyName}")
            except Exception as e:
                logger.warning(f"Failed to connect to Plex server: {e}")
                self.plex_server = None

    def organize_media(self, video_file: Path, media_info: MediaInfo) -> Path:
        """Organize a video file into the library structure."""

        # Generate target directory based on media type
        target_dir = media_info.get_library_path(self.config.library_dir)

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

        logger.info(f"Moving {video_file} -> {target_file}")

        try:
            # Move the file to the target location
            shutil.move(str(video_file), str(target_file))
            logger.info(f"Successfully organized: {target_file}")

            return target_file

        except Exception as e:
            logger.error(f"Failed to move file: {e}")
            raise

    def scan_plex_library(self, media_info: MediaInfo) -> bool:
        """Trigger a Plex library scan for the specific media type."""
        if not self.plex_server:
            logger.warning("Plex server not configured, skipping library scan")
            return False

        try:
            # Determine which library to scan
            library_name = (
                self.config.movies_library if media_info.is_movie
                else self.config.tv_library
            )

            # Get the library
            library = self.plex_server.library.section(library_name)

            # Trigger scan
            library.update()
            logger.info(f"Triggered Plex scan for {library_name} library")

            return True

        except Exception as e:
            logger.error(f"Failed to trigger Plex library scan: {e}")
            return False

    def add_to_plex(self, video_file: Path, media_info: MediaInfo) -> bool:
        """Add media to Plex library (organize + scan)."""
        try:
            # Organize the file
            organized_file = self.organize_media(video_file, media_info)

            # Trigger Plex scan
            scan_success = self.scan_plex_library(media_info)

            logger.info(f"Successfully added {media_info.title} to library")
            return True

        except Exception as e:
            logger.error(f"Failed to add {media_info.title} to library: {e}")
            return False

    def create_review_directory(self, video_file: Path, reason: str = "unidentified") -> Path:
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
            logger.error(f"Failed to move file to review directory: {e}")
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
            logger.error(f"Plex connection verification failed: {e}")
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
            logger.error(f"Failed to get Plex libraries: {e}")
            return []

    def wait_for_plex_scan(self, media_info: MediaInfo, timeout: int = 60) -> bool:
        """Wait for Plex to complete scanning and find the new media."""
        if not self.plex_server:
            return False

        try:
            library_name = (
                self.config.movies_library if media_info.is_movie
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
                        results = library.search(title=media_info.title, year=media_info.year)
                    else:
                        results = library.search(title=media_info.title)

                    if results:
                        logger.info(f"Found {media_info.title} in Plex library")
                        return True

                except Exception:
                    pass

                time.sleep(5)  # Check every 5 seconds

            logger.warning(f"Timeout waiting for {media_info.title} to appear in Plex")
            return False

        except Exception as e:
            logger.error(f"Error waiting for Plex scan: {e}")
            return False
