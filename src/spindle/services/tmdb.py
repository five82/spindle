"""TMDB API integration for media identification."""

import logging
import re
from pathlib import Path
from typing import Any, cast

import httpx

from spindle.config import SpindleConfig

from .tmdb_cache import TMDBCache

logger = logging.getLogger(__name__)


def extract_genres(genres_data: list[dict]) -> list[str]:
    """Extract genre names from TMDB genre data."""
    return [genre.get("name", "") for genre in genres_data if genre.get("name")]


def extract_year(date_string: str) -> int | None:
    """Extract year from TMDB date string."""
    if not date_string or len(date_string) < 4:
        return None
    try:
        return int(date_string[:4])
    except ValueError:
        return None


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
        seasons: int | None = None,  # Total seasons count for TV shows
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
        self.seasons = seasons  # Total seasons count for TV shows

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

    def to_dict(self) -> dict[str, Any]:
        """Convert to dictionary representation."""
        return {
            "title": self.title,
            "year": self.year,
            "media_type": self.media_type,
            "tmdb_id": self.tmdb_id,
            "overview": self.overview,
            "genres": self.genres,
            "season": self.season,
            "episode": self.episode,
            "episode_title": self.episode_title,
            "seasons": self.seasons,
        }

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

    async def find_by_external_id(
        self,
        external_id: str,
        source: str = "imdb_id",
    ) -> dict | None:
        """Find movie/TV show by external ID (IMDB, etc.)."""
        params = {
            "api_key": self.api_key,
            "language": self.language,
            "external_source": source,
        }

        try:
            response = self.client.get(
                f"{self.base_url}/find/{external_id}",
                params=params,
            )
            response.raise_for_status()
            data = response.json()
            return cast("dict[Any, Any]", data)
        except httpx.RequestError as e:
            logger.exception(f"TMDB external ID search failed: {e}")
            return None
        except httpx.HTTPStatusError as e:
            logger.exception(
                f"TMDB external ID error {e.response.status_code}: {e.response.text}",
            )
            return None

    def _calculate_confidence_score(
        self,
        disc_runtime_minutes: int,
        api_runtime_minutes: float,
        popularity: float,
        vote_average: float,
        result_position: int,
        tolerance_minutes: int = 5,
    ) -> tuple[float, dict[str, float]]:
        """Calculate confidence score for TMDB result disambiguation.

        Returns:
            Tuple of (confidence_score, score_breakdown)
            - confidence_score: 0.0-1.0 confidence rating
            - score_breakdown: Dict with individual scoring factors
        """
        scores = {}

        # Runtime match score (0.0-0.5, most important factor)
        runtime_diff = abs(api_runtime_minutes - disc_runtime_minutes)
        if runtime_diff <= tolerance_minutes:
            scores["runtime"] = 0.5  # Perfect runtime match
        elif runtime_diff <= tolerance_minutes * 2:
            scores["runtime"] = 0.3  # Close runtime match
        elif runtime_diff <= tolerance_minutes * 4:
            scores["runtime"] = 0.1  # Distant runtime match
        else:
            scores["runtime"] = 0.0  # No runtime match

        # Popularity score (0.0-0.2)
        # Scale popularity logarithmically (popular movies can have 1000+ popularity)
        if popularity > 0:
            normalized_popularity = min(1.0, popularity / 100.0)  # Cap at 100
            scores["popularity"] = normalized_popularity * 0.2
        else:
            scores["popularity"] = 0.0

        # Quality score (0.0-0.15)
        if vote_average > 0:
            scores["quality"] = (vote_average / 10.0) * 0.15
        else:
            scores["quality"] = 0.0

        # Search position penalty (0.0-0.15)
        # First result gets full points, subsequent results get less
        position_score = max(0.0, 0.15 - (result_position * 0.03))
        scores["position"] = position_score

        total_confidence = sum(scores.values())
        return total_confidence, scores

    async def search_with_runtime_verification(
        self,
        title: str,
        disc_runtime_minutes: int,
        year: int | None = None,
        media_type: str = "movie",
        tolerance_minutes: int = 5,
        confidence_threshold: float = 0.8,
    ) -> tuple[dict | None, list[dict] | None, float]:
        """Search and verify results against disc runtime with confidence scoring.

        Returns:
            Tuple of (verified_result, all_search_results, confidence_score)
            - verified_result: The best result, or None if below confidence threshold
            - all_search_results: All search results from TMDB (for caching)
            - confidence_score: 0.0-1.0 confidence rating for the selected result
        """
        if media_type == "movie":
            results = await self.search_movie(title, year)
        else:
            results = await self.search_tv(title, year)

        if not results:
            return None, None, 0.0

        best_result = None
        best_confidence = 0.0

        # Evaluate each result with confidence scoring
        for i, result in enumerate(results):
            if media_type == "movie":
                details = await self.get_movie_details(result["id"])
                if details and details.get("runtime"):
                    api_runtime = float(details["runtime"])
                    popularity = details.get("popularity", 0.0)
                    vote_average = details.get("vote_average", 0.0)

                    confidence, breakdown = self._calculate_confidence_score(
                        disc_runtime_minutes=disc_runtime_minutes,
                        api_runtime_minutes=api_runtime,
                        popularity=popularity,
                        vote_average=vote_average,
                        result_position=i,
                        tolerance_minutes=tolerance_minutes,
                    )

                    logger.info(
                        f"Movie candidate #{i+1}: '{details.get('title')}' "
                        f"({details.get('release_date', 'Unknown')[:4]}) - "
                        f"Runtime: {api_runtime}m (disc: {disc_runtime_minutes}m), "
                        f"Confidence: {confidence:.2f} "
                        f"(runtime:{breakdown['runtime']:.2f}, pop:{breakdown['popularity']:.2f}, "
                        f"quality:{breakdown['quality']:.2f}, pos:{breakdown['position']:.2f})",
                    )

                    if confidence > best_confidence:
                        best_result = details
                        best_confidence = confidence
            else:
                details = await self.get_tv_details(result["id"])
                if details and details.get("episode_run_time"):
                    runtimes = details["episode_run_time"]
                    if runtimes:
                        api_runtime = sum(runtimes) / len(runtimes)
                        popularity = details.get("popularity", 0.0)
                        vote_average = details.get("vote_average", 0.0)

                        confidence, breakdown = self._calculate_confidence_score(
                            disc_runtime_minutes=disc_runtime_minutes,
                            api_runtime_minutes=api_runtime,
                            popularity=popularity,
                            vote_average=vote_average,
                            result_position=i,
                            tolerance_minutes=tolerance_minutes,
                        )

                        logger.info(
                            f"TV candidate #{i+1}: '{details.get('name')}' "
                            f"({details.get('first_air_date', 'Unknown')[:4]}) - "
                            f"Runtime: {api_runtime:.1f}m (disc: {disc_runtime_minutes}m), "
                            f"Confidence: {confidence:.2f} "
                            f"(runtime:{breakdown['runtime']:.2f}, pop:{breakdown['popularity']:.2f}, "
                            f"quality:{breakdown['quality']:.2f}, pos:{breakdown['position']:.2f})",
                        )

                        if confidence > best_confidence:
                            best_result = details
                            best_confidence = confidence

        # Log final decision
        if best_result:
            result_title = best_result.get("title" if media_type == "movie" else "name")
            if best_confidence >= confidence_threshold:
                logger.info(
                    f"✓ High confidence match: '{result_title}' "
                    f"(confidence: {best_confidence:.2f} >= {confidence_threshold})",
                )
            else:
                logger.warning(
                    f"⚠ Low confidence match: '{result_title}' "
                    f"(confidence: {best_confidence:.2f} < {confidence_threshold}) - "
                    f"Consider manual review",
                )
        else:
            logger.warning(
                f"No viable candidates found for '{title}' ({disc_runtime_minutes}m)",
            )
            return results[0] if results else None, results, 0.0

        # Return best result only if confidence meets threshold
        if best_confidence >= confidence_threshold:
            return best_result, results, best_confidence
        # Return None to trigger manual review, but keep results for caching
        return None, results, best_confidence


class MediaIdentifier:
    """Main class for identifying media files."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBClient(config)
        # Initialize TMDB cache
        cache_dir = config.log_dir / "tmdb_cache"
        self.cache = TMDBCache(cache_dir, ttl_days=config.tmdb_cache_ttl_days)

    def clean_title(self, title: str) -> str:
        """Clean and normalize a title for searching."""
        # Remove common disc indicators
        title = re.sub(
            r"\b(disc|disk|cd|dvd|bluray|blu-ray)\s*\d*\b",
            "",
            title,
            flags=re.IGNORECASE,
        )

        # Clean up title
        title = re.sub(r"[._]", " ", title)  # Replace dots and underscores with spaces
        title = re.sub(r"-", " ", title)  # Replace hyphens with spaces for parsing
        return re.sub(r"\s+", " ", title).strip()

    def build_poster_url(self, poster_path: str | None) -> str | None:
        """Build full poster URL from TMDB poster path."""
        if not poster_path:
            return None
        return f"https://image.tmdb.org/t/p/w500{poster_path}"

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
            seasons=show_details.get("number_of_seasons"),
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

    async def identify_with_runtime_verification(
        self,
        disc_title: str,
        main_title_runtime_seconds: int,
        year: int | None = None,
    ) -> tuple[MediaInfo | None, dict[str, list[dict]] | None]:
        """Identify media using disc title and runtime verification.

        Returns:
            Tuple of (media_info, search_results)
            - media_info: Identified MediaInfo object, or None if not found
            - search_results: Dict with 'movie' and 'tv' search results for caching
        """
        # Clean up disc title
        title = re.sub(r"[._-]", " ", disc_title)
        title = re.sub(r"\s+", " ", title).strip()

        # Convert runtime to minutes for API comparison
        runtime_minutes = main_title_runtime_seconds // 60

        # Try to extract year from title if not provided
        if not year:
            year_match = re.search(r"(\d{4})", title)
            year = int(year_match.group(1)) if year_match else None

        # Remove year from title for search
        if year:
            year_pattern = f"({year})" if f"({year})" in title else str(year)
            title = title.replace(year_pattern, "").strip()

        # Try runtime-verified search
        movie_result, movie_search_results, movie_confidence = (
            await self.tmdb.search_with_runtime_verification(
                title=title,
                disc_runtime_minutes=runtime_minutes,
                year=year,
                media_type="movie",
                tolerance_minutes=self.config.tmdb_runtime_tolerance_minutes,
                confidence_threshold=self.config.tmdb_confidence_threshold,
            )
        )

        if movie_result:
            # Convert to MediaInfo
            release_date = movie_result.get("release_date", "")
            movie_year = (
                int(release_date[:4])
                if release_date and len(release_date) >= 4
                else year
            )
            genres = [g["name"] for g in movie_result.get("genres", [])]

            # Cache search results for potential reuse
            search_cache = {"movie": movie_search_results or [], "tv": []}

            return (
                MediaInfo(
                    title=movie_result.get("title", title),
                    year=movie_year or 0,
                    media_type="movie",
                    tmdb_id=movie_result["id"],
                    overview=movie_result.get("overview", ""),
                    genres=genres,
                ),
                search_cache,
            )

        # Try TV show if movie search failed
        tv_result, tv_search_results, tv_confidence = (
            await self.tmdb.search_with_runtime_verification(
                title=title,
                disc_runtime_minutes=runtime_minutes,
                year=year,
                media_type="tv",
                tolerance_minutes=5,
                confidence_threshold=0.8,
            )
        )

        if tv_result:
            # Convert to MediaInfo
            first_air_date = tv_result.get("first_air_date", "")
            show_year = (
                int(first_air_date[:4])
                if first_air_date and len(first_air_date) >= 4
                else year
            )
            genres = [g["name"] for g in tv_result.get("genres", [])]

            # Cache search results for potential reuse
            search_cache = {
                "movie": movie_search_results or [],
                "tv": tv_search_results or [],
            }

            return (
                MediaInfo(
                    title=tv_result.get("name", title),
                    year=show_year or 0,
                    media_type="tv",
                    tmdb_id=tv_result["id"],
                    overview=tv_result.get("overview", ""),
                    genres=genres,
                ),
                search_cache,
            )
        if tv_search_results:
            # Low confidence - cache results but return None for manual review
            logger.warning(
                f"Low confidence TV identification for '{disc_title}' - "
                f"marking for manual review (confidence: {tv_confidence:.2f})",
            )
            search_cache = {
                "movie": movie_search_results or [],
                "tv": tv_search_results or [],
            }
            return None, search_cache

        # No matches found, but return search results for caching
        logger.warning(
            f"No runtime-verified matches found for '{disc_title}' ({runtime_minutes}m)",
        )
        search_cache = {
            "movie": movie_search_results or [],
            "tv": tv_search_results or [],
        }
        return None, search_cache

    def extract_best_title(self, title_candidates: list[str]) -> str | None:
        """Extract the best available title from multiple sources."""
        for title in title_candidates:
            if title and not self.is_generic_label(title):
                return self.normalize_title(title)

        # If all sources are generic, return None for manual identification
        return None

    def normalize_title(self, title: str) -> str:
        """Clean and normalize a title for searching."""
        if not title:
            return ""

        # Remove common disc indicators
        title = re.sub(
            r"\b(disc|disk|cd|dvd|bluray|blu-ray)\s*\d*\b",
            "",
            title,
            flags=re.IGNORECASE,
        )

        # Clean up title
        title = re.sub(r"[._]", " ", title)  # Replace dots and underscores with spaces
        title = re.sub(r"-", " ", title)  # Replace hyphens with spaces for parsing
        return re.sub(r"\s+", " ", title).strip()

    def is_generic_label(self, label: str) -> bool:
        """Check if disc label is too generic for identification."""
        if not label:
            return True

        generic_patterns = [
            "LOGICAL_VOLUME_ID",
            "DVD_VIDEO",
            "BLURAY",
            "BD_ROM",
            "UNTITLED",
            r"^\d+$",  # Just numbers
            r"^[A-Z0-9_]{1,3}$",  # Very short codes
        ]

        for pattern in generic_patterns:
            if re.match(pattern, label, re.IGNORECASE):
                return True

        return False

    async def identify_disc_content(
        self,
        title_candidates: list[str],
        runtime_minutes: int | None = None,
        content_type: str | None = None,
    ) -> MediaInfo | None:
        """Identify disc content using multiple title sources and caching."""
        # Step 1: Extract best title from all sources
        clean_title = self.extract_best_title(title_candidates)

        if not clean_title:
            logger.warning("No usable title found in disc metadata")
            return None

        # Check if title is too generic for TMDB search
        if self._is_generic_title(clean_title):
            logger.info(f"Skipping TMDB search for generic title: '{clean_title}'")
            return None

        # Determine target media type for focused search
        target_media_type = "movie"  # Default fallback
        if content_type:
            if content_type.lower() in ["tv_series", "tv", "television"]:
                target_media_type = "tv"
            elif content_type.lower() in ["movie", "film"]:
                target_media_type = "movie"

        logger.info(
            f"Identifying disc content with title: '{clean_title}' (target: {target_media_type})",
        )

        # Step 2: Check cache first
        cached = self.cache.search_cache(clean_title, target_media_type)
        if cached and cached.is_valid():
            logger.info(
                f"Using cached TMDB results for '{clean_title}' ({target_media_type})",
            )
            # Use cached detailed info if available, otherwise use first search result
            if cached.detailed_info:
                return self._convert_tmdb_to_media_info(
                    cached.detailed_info,
                    target_media_type,
                )
            if cached.results:
                # Get detailed info for first result
                if target_media_type == "movie":
                    detailed_info = await self.tmdb.get_movie_details(
                        cached.results[0]["id"],
                    )
                else:
                    detailed_info = await self.tmdb.get_tv_details(
                        cached.results[0]["id"],
                    )
                if detailed_info:
                    # Cache the detailed info for future use
                    self.cache.cache_results(
                        clean_title,
                        cached.results,
                        target_media_type,
                        detailed_info,
                    )
                    return self._convert_tmdb_to_media_info(
                        detailed_info,
                        target_media_type,
                    )

        # Step 3: Make targeted TMDB search based on content type
        if target_media_type == "movie":
            # Movie search with optional runtime verification
            if runtime_minutes:
                logger.info(
                    f"Searching TMDB movies with runtime verification ({runtime_minutes} min)",
                )
                movie_result, movie_search_results, movie_confidence = (
                    await self.tmdb.search_with_runtime_verification(
                        title=clean_title,
                        disc_runtime_minutes=runtime_minutes,
                        media_type="movie",
                        tolerance_minutes=self.config.tmdb_runtime_tolerance_minutes,
                        confidence_threshold=self.config.tmdb_confidence_threshold,
                    )
                )
                if movie_result:
                    # Cache search results
                    if movie_search_results:
                        self.cache.cache_results(
                            clean_title,
                            movie_search_results,
                            "movie",
                            movie_result,
                        )
                    return self._convert_tmdb_to_media_info(movie_result, "movie")
                if movie_search_results:
                    # Low confidence - cache results but return None for manual review
                    logger.warning(
                        f"Low confidence identification for '{clean_title}' - "
                        f"marking for manual review (confidence: {movie_confidence:.2f})",
                    )
                    self.cache.cache_results(clean_title, movie_search_results, "movie")
                    return None

            # Fallback to regular movie search
            logger.info("Searching TMDB movies")
            movie_results = await self.tmdb.search_movie(clean_title)
            if movie_results:
                detailed_info = await self.tmdb.get_movie_details(
                    movie_results[0]["id"],
                )
                if detailed_info:
                    self.cache.cache_results(
                        clean_title,
                        movie_results,
                        "movie",
                        detailed_info,
                    )
                    return self._convert_tmdb_to_media_info(detailed_info, "movie")

        else:
            # TV search
            logger.info("Searching TMDB TV shows")
            tv_results = await self.tmdb.search_tv(clean_title)
            if tv_results:
                detailed_info = await self.tmdb.get_tv_details(tv_results[0]["id"])
                if detailed_info:
                    self.cache.cache_results(
                        clean_title,
                        tv_results,
                        "tv",
                        detailed_info,
                    )
                    return self._convert_tmdb_to_media_info(detailed_info, "tv")

        # Step 4: If targeted search failed, try the opposite type as fallback
        fallback_type = "tv" if target_media_type == "movie" else "movie"
        logger.info(f"Targeted search failed, trying fallback: {fallback_type}")

        if fallback_type == "movie":
            movie_results = await self.tmdb.search_movie(clean_title)
            if movie_results:
                detailed_info = await self.tmdb.get_movie_details(
                    movie_results[0]["id"],
                )
                if detailed_info:
                    self.cache.cache_results(
                        clean_title,
                        movie_results,
                        "movie",
                        detailed_info,
                    )
                    return self._convert_tmdb_to_media_info(detailed_info, "movie")
        else:
            tv_results = await self.tmdb.search_tv(clean_title)
            if tv_results:
                detailed_info = await self.tmdb.get_tv_details(tv_results[0]["id"])
                if detailed_info:
                    self.cache.cache_results(
                        clean_title,
                        tv_results,
                        "tv",
                        detailed_info,
                    )
                    return self._convert_tmdb_to_media_info(detailed_info, "tv")

        logger.warning(
            f"No TMDB matches found for '{clean_title}' in either {target_media_type} or {fallback_type}",
        )
        return None

    def _is_generic_title(self, title: str) -> bool:
        """Check if title is too generic for TMDB search."""
        if not title or len(title.strip()) < 3:
            return True

        title_upper = title.upper().strip()

        generic_patterns = [
            "LOGICAL VOLUME ID",
            "LOGICAL_VOLUME_ID",
            "DVD VIDEO",
            "DVD_VIDEO",
            "BLURAY",
            "BLU RAY",
            "BD ROM",
            "BD_ROM",
            "UNTITLED",
            "DISC",
            "MOVIE",
            "FILM",
            "VIDEO",
            r"^\d+$",  # Just numbers
            r"^[A-Z0-9_\s]{1,4}$",  # Very short codes/labels
            r"^DISC\s*\d*$",  # DISC, DISC1, etc.
            r"^TITLE\s*\d*$",  # TITLE, TITLE1, etc.
        ]

        for pattern in generic_patterns:
            if re.match(pattern, title_upper) or pattern == title_upper:
                return True

        return False

    def _convert_tmdb_to_media_info(
        self,
        tmdb_data: dict,
        media_type: str,
    ) -> MediaInfo:
        """Convert TMDB API response to MediaInfo object."""
        if media_type == "movie":
            # Extract year from release date
            release_date = tmdb_data.get("release_date", "")
            year = (
                int(release_date[:4]) if release_date and len(release_date) >= 4 else 0
            )
            genres = [g["name"] for g in tmdb_data.get("genres", [])]

            return MediaInfo(
                title=tmdb_data.get("title", "Unknown"),
                year=year,
                media_type="movie",
                tmdb_id=tmdb_data["id"],
                overview=tmdb_data.get("overview", ""),
                genres=genres,
            )
        # TV show
        # Extract year from first air date
        first_air_date = tmdb_data.get("first_air_date", "")
        year = (
            int(first_air_date[:4])
            if first_air_date and len(first_air_date) >= 4
            else 0
        )
        genres = [g["name"] for g in tmdb_data.get("genres", [])]

        return MediaInfo(
            title=tmdb_data.get("name", "Unknown"),
            year=year,
            media_type="tv",
            tmdb_id=tmdb_data["id"],
            overview=tmdb_data.get("overview", ""),
            genres=genres,
            seasons=tmdb_data.get("number_of_seasons"),
        )

    async def identify_movie(self, title: str, year: int | None) -> MediaInfo | None:
        """Identify a movie by title and year."""
        return await self._identify_movie(title, year)

    async def identify_tv_series(
        self,
        title: str,
        year: int | None,
    ) -> MediaInfo | None:
        """Identify a TV series by title and year."""
        results = await self.tmdb.search_tv(title, year)

        if not results:
            logger.warning(f"No TV series results found for '{title}' ({year})")
            return None

        # Take the first (most relevant) result
        tv_data = results[0]

        # Get detailed show information
        detailed_data = await self.tmdb.get_tv_details(tv_data["id"])
        if not detailed_data:
            detailed_data = tv_data

        # Extract year from first air date
        first_air_date = detailed_data.get("first_air_date", "")
        show_year = (
            int(first_air_date[:4])
            if first_air_date and len(first_air_date) >= 4
            else year
        )

        genres = [g["name"] for g in detailed_data.get("genres", [])]

        return MediaInfo(
            title=detailed_data.get("name", title),
            year=show_year or 0,
            media_type="tv",
            tmdb_id=detailed_data["id"],
            overview=detailed_data.get("overview", ""),
            genres=genres,
            seasons=detailed_data.get("number_of_seasons"),
        )

    async def search_movies(self, query: str, year: int | None = None) -> list[dict]:
        """Search for movies via TMDB."""
        return await self.tmdb.search_movie(query, year)

    async def search_tv_series(self, query: str, year: int | None = None) -> list[dict]:
        """Search for TV series via TMDB."""
        return await self.tmdb.search_tv(query, year)


class TMDBService:
    """Consolidated TMDB service combining wrapper and implementation."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.identifier = MediaIdentifier(config)

    async def identify_media(
        self,
        title: str,
        content_type: str = "movie",
        year: int | None = None,
    ) -> MediaInfo | None:
        """Identify media via TMDB API."""
        try:
            logger.info(f"Identifying {content_type}: {title} ({year})")

            if content_type == "movie":
                return await self.identifier.identify_movie(title, year)
            if content_type == "tv_series":
                return await self.identifier.identify_tv_series(title, year)
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


# Re-export key classes for compatibility
__all__ = ["MediaIdentifier", "MediaInfo", "TMDBService"]
