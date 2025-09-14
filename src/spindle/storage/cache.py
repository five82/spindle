"""Unified caching system for Spindle."""

import logging
from typing import Any

from ..config import SpindleConfig
from ..disc.series_cache import SeriesCache
from ..identify.tmdb_cache import TMDBCache

logger = logging.getLogger(__name__)


class SpindleCache:
    """Unified cache management for all Spindle subsystems."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBCache(config)
        self.series = SeriesCache(config)

    def clear_all(self) -> None:
        """Clear all caches."""
        logger.info("Clearing all caches")
        try:
            self.tmdb.clear()
            self.series.clear()
            logger.info("All caches cleared successfully")
        except Exception as e:
            logger.error(f"Error clearing caches: {e}")
            raise

    def get_cache_stats(self) -> dict[str, Any]:
        """Get statistics for all caches."""
        return {
            "tmdb": self.tmdb.get_stats(),
            "series": self.series.get_stats(),
        }

    def cleanup_expired(self) -> None:
        """Remove expired entries from all caches."""
        logger.info("Cleaning up expired cache entries")
        try:
            tmdb_removed = self.tmdb.cleanup_expired()
            series_removed = self.series.cleanup_expired()

            logger.info(f"Cleanup complete: {tmdb_removed} TMDB entries, {series_removed} series entries removed")
        except Exception as e:
            logger.error(f"Error during cache cleanup: {e}")
            raise

    def get_total_size(self) -> dict[str, int]:
        """Get total size information for all caches."""
        return {
            "tmdb_entries": self.tmdb.get_entry_count(),
            "series_entries": self.series.get_entry_count(),
            "total_size_bytes": self.tmdb.get_size_bytes() + self.series.get_size_bytes(),
        }

    def validate_integrity(self) -> dict[str, bool]:
        """Validate integrity of all caches."""
        return {
            "tmdb_valid": self.tmdb.validate_integrity(),
            "series_valid": self.series.validate_integrity(),
        }
