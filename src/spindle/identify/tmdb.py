"""TMDB API integration for media identification."""

import logging
import re
from pathlib import Path
from typing import Any, cast

import httpx

from spindle.config import SpindleConfig

logger = logging.getLogger(__name__)


class MediaInfo:
    """Represents identified media information."""

    def __init__(
        self,
        title: str,
        year: int,
        media_type: str,
        tmdb_id: int,
        overview: str = "",
        genres: list[str] | None = None,
        season: int | None = None,
        episode: int | None = None,
        episode_title: str | None = None,
    ):
        self.title = title
        self.year = year
        self.media_type = media_type  # "movie" or "tv"
        self.tmdb_id = tmdb_id
        self.overview = overview
        self.genres = genres or []
        self.season = season
        self.episode = episode
        self.episode_title = episode_title

    @property
    def is_movie(self) -> bool:
        """Check if this is a movie."""
        return self.media_type == "movie"

    @property
    def is_tv_show(self) -> bool:
        """Check if this is a TV show."""
        return self.media_type == "tv"

    def get_filename(self) -> str:
        """Generate appropriate filename for this media."""
        # Remove unsafe characters but preserve hyphens in reasonable positions
        safe_title = re.sub(r"[^\w\s\-]", "", self.title).strip()
        # Collapse multiple spaces but preserve single hyphens
        safe_title = re.sub(r"\s+", " ", safe_title)

        if self.is_movie:
            return f"{safe_title} ({self.year})"
        if self.is_tv_show and self.season is not None and self.episode is not None:
            episode_part = f"S{self.season:02d}E{self.episode:02d}"
            if self.episode_title:
                safe_ep_title = re.sub(r"[^\w\s\-]", "", self.episode_title).strip()
                safe_ep_title = re.sub(r"\s+", " ", safe_ep_title)
                return f"{safe_title} - {episode_part} - {safe_ep_title}"
            return f"{safe_title} - {episode_part}"
        return f"{safe_title} ({self.year})"

    def get_library_path(
        self,
        library_root: Path,
        movies_dir: str = "movies",
        tv_dir: str = "tv",
    ) -> Path:
        """Generate library directory path for this media."""
        if self.is_movie:
            return library_root / movies_dir / f"{self.get_filename()}"
        if self.is_tv_show:
            show_dir = library_root / tv_dir / f"{self.title} ({self.year})"
            if self.season is not None:
                return show_dir / f"Season {self.season:02d}"
            return show_dir
        return library_root / "Unknown" / self.get_filename()

    def __str__(self) -> str:
        if self.is_tv_show and self.season is not None and self.episode is not None:
            return f"{self.title} ({self.year}) S{self.season:02d}E{self.episode:02d}"
        return f"{self.title} ({self.year})"


class TMDBClient:
    """Client for TMDB API interactions."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.api_key = config.tmdb_api_key
        self.language = config.tmdb_language
        self.base_url = "https://api.themoviedb.org/3"
        self.client = httpx.Client(timeout=config.tmdb_request_timeout)

    async def search_movie(self, title: str, year: int | None = None) -> list[dict]:
        """Search for movies on TMDB."""
        params = {
            "api_key": self.api_key,
            "language": self.language,
            "query": title,
        }

        if year:
            params["year"] = str(year)

        try:
            response = self.client.get(f"{self.base_url}/search/movie", params=params)
            response.raise_for_status()
            data = response.json()
            results = data.get("results", [])
            return cast("list[dict[Any, Any]]", results)
        except httpx.RequestError as e:
            logger.exception(f"TMDB API request failed: {e}")
            return []
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"TMDB API error {e.response.status_code}: {e.response.text}",
            )
            return []

    async def search_tv(self, title: str, year: int | None = None) -> list[dict]:
        """Search for TV shows on TMDB."""
        params = {
            "api_key": self.api_key,
            "language": self.language,
            "query": title,
        }

        if year:
            params["first_air_date_year"] = str(year)

        try:
            response = self.client.get(f"{self.base_url}/search/tv", params=params)
            response.raise_for_status()
            data = response.json()
            results = data.get("results", [])
            return cast("list[dict[Any, Any]]", results)
        except httpx.RequestError as e:
            logger.exception(f"TMDB API request failed: {e}")
            return []
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"TMDB API error {e.response.status_code}: {e.response.text}",
            )
            return []

    async def get_movie_details(self, movie_id: int) -> dict | None:
        """Get detailed movie information."""
        params = {
            "api_key": self.api_key,
            "language": self.language,
        }

        try:
            response = self.client.get(
                f"{self.base_url}/movie/{movie_id}",
                params=params,
            )
            response.raise_for_status()
            data = response.json()
            return cast("dict[Any, Any]", data)
        except httpx.RequestError as e:
            logger.exception(f"TMDB API request failed: {e}")
            return None
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"TMDB API error {e.response.status_code}: {e.response.text}",
            )
            return None

    async def get_tv_details(self, tv_id: int) -> dict | None:
        """Get detailed TV show information."""
        params = {
            "api_key": self.api_key,
            "language": self.language,
        }

        try:
            response = self.client.get(f"{self.base_url}/tv/{tv_id}", params=params)
            response.raise_for_status()
            data = response.json()
            return cast("dict[Any, Any]", data)
        except httpx.RequestError as e:
            logger.exception(f"TMDB API request failed: {e}")
            return None
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"TMDB API error {e.response.status_code}: {e.response.text}",
            )
            return None

    async def get_tv_episode_details(
        self,
        tv_id: int,
        season: int,
        episode: int,
    ) -> dict | None:
        """Get detailed TV episode information."""
        params = {
            "api_key": self.api_key,
            "language": self.language,
        }

        try:
            response = self.client.get(
                f"{self.base_url}/tv/{tv_id}/season/{season}/episode/{episode}",
                params=params,
            )
            response.raise_for_status()
            data = response.json()
            return cast("dict[Any, Any]", data)
        except httpx.RequestError as e:
            logger.exception(f"TMDB API request failed: {e}")
            return None
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"TMDB API error {e.response.status_code}: {e.response.text}",
            )
            return None


class MediaIdentifier:
    """Main class for identifying media files."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBClient(config)

    def parse_filename(
        self,
        filepath: str | Path,
    ) -> tuple[str, int | None, int | None, int | None]:
        """Parse filename to extract title, year, season, and episode."""
        path = Path(filepath)
        # Only use stem if there's a common video extension, otherwise use the full name
        common_extensions = {
            ".mkv",
            ".mp4",
            ".avi",
            ".mov",
            ".m4v",
            ".wmv",
            ".flv",
            ".webm",
        }
        filename = path.stem if path.suffix.lower() in common_extensions else path.name

        # Remove common disc indicators
        filename = re.sub(
            r"\b(disc|disk|cd|dvd|bluray|blu-ray)\s*\d*\b",
            "",
            filename,
            flags=re.IGNORECASE,
        )

        # Try to extract year - check parentheses first, then standalone
        year = None
        year_match = re.search(r"\((\d{4})\)", filename)
        if year_match:
            year = int(year_match.group(1))
        else:
            # Try to find standalone year (first occurrence)
            year_match = re.search(r"(\d{4})", filename)
            if year_match:
                year = int(year_match.group(1))

        # Try to extract season/episode for TV shows
        season = None
        episode = None

        # Pattern: S01E02, S1E2, etc.
        se_match = re.search(r"[Ss](\d{1,2})[Ee](\d{1,2})", filename)
        if se_match:
            season = int(se_match.group(1))
            episode = int(se_match.group(2))
        else:
            # Pattern: 1x02, 01x02, etc.
            se_match = re.search(r"(\d{1,2})x(\d{1,2})", filename)
            if se_match:
                season = int(se_match.group(1))
                episode = int(se_match.group(2))

        # Clean up title - start with original filename
        title = filename

        # Remove year (both patterns)
        if year_match:
            title = title.replace(year_match.group(0), "")

        # Remove season/episode and everything after it for TV shows
        if se_match:
            # Find the position of the season/episode match and truncate there
            se_pos = title.find(se_match.group(0))
            if se_pos != -1:
                title = title[:se_pos]

        # Clean up title
        title = re.sub(r"[._]", " ", title)  # Replace dots and underscores with spaces
        title = re.sub(r"-", " ", title)  # Replace hyphens with spaces for parsing
        title = re.sub(r"\s+", " ", title).strip()

        return title, year, season, episode

    async def identify_media(self, filepath: str | Path) -> MediaInfo | None:
        """Identify media from filepath."""
        title, year, season, episode = self.parse_filename(filepath)

        if not title:
            logger.warning(f"Could not extract title from {filepath}")
            return None

        logger.info(f"Identifying: {title} ({year}) S{season}E{episode}")

        # Determine if this is likely a TV show or movie
        is_tv = season is not None and episode is not None

        if is_tv and season is not None and episode is not None:
            return await self._identify_tv_episode(title, year, season, episode)
        return await self._identify_movie(title, year)

    async def _identify_movie(self, title: str, year: int | None) -> MediaInfo | None:
        """Identify a movie."""
        results = await self.tmdb.search_movie(title, year)

        if not results:
            logger.warning(f"No movie results found for '{title}' ({year})")
            return None

        # Take the first (most relevant) result
        movie_data = results[0]

        # Get detailed information
        detailed_data = await self.tmdb.get_movie_details(movie_data["id"])
        if not detailed_data:
            detailed_data = movie_data

        # Extract year from release date
        release_date = detailed_data.get("release_date", "")
        movie_year = (
            int(release_date[:4]) if release_date and len(release_date) >= 4 else year
        )

        genres = [g["name"] for g in detailed_data.get("genres", [])]

        return MediaInfo(
            title=detailed_data.get("title", title),
            year=movie_year or 0,
            media_type="movie",
            tmdb_id=detailed_data["id"],
            overview=detailed_data.get("overview", ""),
            genres=genres,
        )

    async def _identify_tv_episode(
        self,
        title: str,
        year: int | None,
        season: int,
        episode: int,
    ) -> MediaInfo | None:
        """Identify a TV episode."""
        results = await self.tmdb.search_tv(title, year)

        if not results:
            logger.warning(f"No TV show results found for '{title}' ({year})")
            return None

        # Take the first (most relevant) result
        tv_data = results[0]

        # Get detailed show information
        show_details = await self.tmdb.get_tv_details(tv_data["id"])
        if not show_details:
            show_details = tv_data

        # Get episode details
        episode_details = await self.tmdb.get_tv_episode_details(
            tv_data["id"],
            season,
            episode,
        )

        # Extract year from first air date
        first_air_date = show_details.get("first_air_date", "")
        show_year = (
            int(first_air_date[:4])
            if first_air_date and len(first_air_date) >= 4
            else year
        )

        genres = [g["name"] for g in show_details.get("genres", [])]
        episode_title = episode_details.get("name") if episode_details else None

        return MediaInfo(
            title=show_details.get("name", title),
            year=show_year or 0,
            media_type="tv",
            tmdb_id=show_details["id"],
            overview=show_details.get("overview", ""),
            genres=genres,
            season=season,
            episode=episode,
            episode_title=episode_title,
        )

    async def identify_from_disc_title(self, disc_title: str) -> MediaInfo | None:
        """Identify media from disc title/label."""
        # Clean up disc title
        title = re.sub(r"[._-]", " ", disc_title)
        title = re.sub(r"\s+", " ", title).strip()

        # Try to extract year from title
        year_match = re.search(r"(\d{4})", title)
        year = int(year_match.group(1)) if year_match else None

        # Remove year from title for search
        if year_match:
            title = title.replace(year_match.group(0), "").strip()

        # Try movie first (most common for discs)
        return await self._identify_movie(title, year)
