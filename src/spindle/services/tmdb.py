"""TMDB API integration service."""

import logging
from typing import Any

from ..config import SpindleConfig
from ..identify.tmdb import MediaIdentifier, MediaInfo

logger = logging.getLogger(__name__)


class TMDBService:
    """Clean wrapper for TMDB media identification."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.identifier = MediaIdentifier(config)

    async def identify_media(
        self,
        title: str,
        content_type: str = "movie",
        year: int | None = None
    ) -> MediaInfo | None:
        """Identify media via TMDB API."""
        try:
            logger.info(f"Identifying {content_type}: {title} ({year})")

            if content_type == "movie":
                return await self.identifier.identify_movie(title, year)
            elif content_type == "tv_series":
                return await self.identifier.identify_tv_series(title, year)
            else:
                logger.warning(f"Unknown content type: {content_type}")
                return None

        except Exception as e:
            logger.exception(f"TMDB identification failed: {e}")
            return None

    async def search_movies(self, query: str, year: int | None = None) -> list[dict]:
        """Search for movies via TMDB."""
        return await self.identifier.search_movies(query, year)

    async def search_tv_series(self, query: str, year: int | None = None) -> list[dict]:
        """Search for TV series via TMDB."""
        return await self.identifier.search_tv_series(query, year)

    def get_cache_stats(self) -> dict[str, int]:
        """Get TMDB cache statistics."""
        return self.identifier.cache.get_stats()
