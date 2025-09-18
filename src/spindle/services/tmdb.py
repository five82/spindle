"""Simplified TMDB API integration used for disc classification."""

from __future__ import annotations

import logging
import re
from collections.abc import Iterable
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

import httpx

if TYPE_CHECKING:
    from pathlib import Path

    from spindle.config import SpindleConfig

logger = logging.getLogger(__name__)


def _clean_title(value: str | None) -> str:
    """Create a filesystem and query friendly version of a title string."""
    if not value:
        return ""

    cleaned = re.sub(r"[^\w\s-]", " ", value)
    cleaned = re.sub(r"\s+", " ", cleaned)
    return cleaned.strip()


@dataclass
class MediaInfo:
    """Lightweight metadata record returned from TMDB."""

    title: str
    year: int
    media_type: str  # "movie" or "tv"
    tmdb_id: int
    overview: str = ""
    genres: list[str] = field(default_factory=list)
    season: int | None = None
    seasons: int | None = None
    episode: int | None = None
    episode_title: str | None = None
    runtime: int | None = None  # minutes
    episodes: list[dict[str, Any]] | None = None
    confidence: float = 0.0

    @property
    def is_movie(self) -> bool:
        return self.media_type == "movie"

    @property
    def is_tv_show(self) -> bool:
        return self.media_type == "tv"

    def get_filename(self) -> str:
        base = _clean_title(self.title)
        if not base:
            base = "Unknown"

        if self.is_movie:
            return f"{base} ({self.year})"

        if self.is_tv_show and self.season is not None and self.episode is not None:
            episode_part = f"S{self.season:02d}E{self.episode:02d}"
            if self.episode_title:
                episode_title = _clean_title(self.episode_title)
                return f"{base} - {episode_part} - {episode_title}"
            return f"{base} - {episode_part}"

        return f"{base} ({self.year})"

    def get_library_path(
        self,
        library_root: Path,
        movies_dir: str = "movies",
        tv_dir: str = "tv",
    ) -> Path:
        if self.is_movie:
            return library_root / movies_dir / self.get_filename()

        if self.is_tv_show:
            show_dir = (
                library_root / tv_dir / f"{_clean_title(self.title)} ({self.year})"
            )
            if self.season is not None:
                return show_dir / f"Season {self.season:02d}"
            return show_dir

        return library_root / "Unknown" / self.get_filename()

    def to_dict(self) -> dict[str, Any]:
        return {
            "title": self.title,
            "year": self.year,
            "media_type": self.media_type,
            "tmdb_id": self.tmdb_id,
            "overview": self.overview,
            "genres": list(self.genres),
            "season": self.season,
            "seasons": self.seasons,
            "episode": self.episode,
            "episode_title": self.episode_title,
            "runtime": self.runtime,
            "confidence": self.confidence,
        }


class TMDBService:
    """Minimal async TMDB client with a small in-memory cache."""

    _BASE_URL = "https://api.themoviedb.org/3"

    def __init__(self, config: SpindleConfig):
        self.config = config
        self._cache: dict[tuple[str, str, int | None], MediaInfo | None] = {}

    async def identify_media(
        self,
        query: str,
        content_type: str = "movie",
        *,
        year: int | None = None,
        runtime_hint: int | None = None,
        season_hint: int | None = None,
    ) -> MediaInfo | None:
        """Return the best TMDB match for the provided query."""

        normalized_type = "tv" if content_type in {"tv", "tv_series"} else "movie"

        cache_key = (query.lower(), normalized_type, year)
        if cache_key in self._cache:
            return self._cache[cache_key]

        try:
            results = await self._search(query, normalized_type, year)
        except httpx.HTTPError as exc:  # pragma: no cover - network failure path
            logger.warning("TMDB search failed for %s: %s", query, exc)
            self._cache[cache_key] = None
            return None

        if not results:
            self._cache[cache_key] = None
            return None

        best = results[0]
        if normalized_type == "movie":
            media_info = await self._build_movie_info(best, runtime_hint)
        else:
            media_info = await self._build_tv_info(best, runtime_hint, season_hint)

        if media_info:
            media_info.confidence = self._score_match(media_info, runtime_hint, year)

        self._cache[cache_key] = media_info
        return media_info

    async def search_movies(self, query: str, year: int | None = None) -> list[dict]:
        return await self._search(query, "movie", year)

    async def search_tv_series(self, query: str, year: int | None = None) -> list[dict]:
        return await self._search(query, "tv", year)

    async def _search(
        self,
        query: str,
        content_type: str,
        year: int | None,
    ) -> list[dict[str, Any]]:
        endpoint = "/search/movie" if content_type == "movie" else "/search/tv"
        params = {
            "query": query,
            "api_key": self.config.tmdb_api_key,
            "language": self.config.tmdb_language,
            "include_adult": False,
        }

        if year:
            params["year" if content_type == "movie" else "first_air_date_year"] = year

        data = await self._request(endpoint, params)
        return data.get("results", []) if data else []

    async def _build_movie_info(
        self,
        payload: dict[str, Any],
        runtime_hint: int | None,
    ) -> MediaInfo | None:
        details = await self._request(
            f"/movie/{payload['id']}",
            {
                "api_key": self.config.tmdb_api_key,
                "language": self.config.tmdb_language,
            },
        )

        if not details:
            return None

        release_date = details.get("release_date") or payload.get("release_date") or ""
        year = int(release_date[:4]) if release_date[:4].isdigit() else 0

        media_info = MediaInfo(
            title=details.get("title")
            or payload.get("title")
            or query_fallback(payload),
            year=year,
            media_type="movie",
            tmdb_id=payload["id"],
            overview=details.get("overview", ""),
            genres=[
                genre.get("name", "")
                for genre in details.get("genres", [])
                if genre.get("name")
            ],
            runtime=details.get("runtime") or payload.get("runtime"),
        )

        if runtime_hint and not media_info.runtime:
            media_info.runtime = runtime_hint

        return media_info

    async def _build_tv_info(
        self,
        payload: dict[str, Any],
        runtime_hint: int | None,
        season_hint: int | None,
    ) -> MediaInfo | None:
        details = await self._request(
            f"/tv/{payload['id']}",
            {
                "api_key": self.config.tmdb_api_key,
                "language": self.config.tmdb_language,
            },
        )

        if not details:
            return None

        first_air = details.get("first_air_date") or payload.get("first_air_date") or ""
        year = int(first_air[:4]) if first_air[:4].isdigit() else 0
        seasons = details.get("number_of_seasons")

        season_number = season_hint or self._guess_season(details.get("seasons", []))

        runtime = None
        run_times = details.get("episode_run_time") or payload.get("episode_run_time")
        if run_times:
            runtime = run_times[0] if isinstance(run_times, Iterable) else run_times

        if runtime_hint and not runtime:
            runtime = runtime_hint

        episodes = None
        if season_number:
            episodes = await self._fetch_episodes(payload["id"], season_number)

        return MediaInfo(
            title=details.get("name") or payload.get("name") or query_fallback(payload),
            year=year,
            media_type="tv",
            tmdb_id=payload["id"],
            overview=details.get("overview", ""),
            genres=[
                genre.get("name", "")
                for genre in details.get("genres", [])
                if genre.get("name")
            ],
            seasons=seasons,
            season=season_number,
            runtime=runtime,
            episodes=episodes,
        )

    async def _fetch_episodes(
        self,
        tmdb_id: int,
        season_number: int,
    ) -> list[dict[str, Any]]:
        data = await self._request(
            f"/tv/{tmdb_id}/season/{season_number}",
            {
                "api_key": self.config.tmdb_api_key,
                "language": self.config.tmdb_language,
            },
        )
        if not data:
            return []

        episodes: list[dict[str, Any]] = []
        for episode in data.get("episodes", []) or []:
            episodes.append(
                {
                    "season_number": episode.get("season_number", season_number),
                    "episode_number": episode.get("episode_number"),
                    "name": episode.get("name", ""),
                    "runtime": episode.get("runtime"),
                },
            )
        return episodes

    async def _request(
        self,
        endpoint: str,
        params: dict[str, Any],
    ) -> dict[str, Any] | None:
        url = f"{self._BASE_URL}{endpoint}"
        try:
            async with httpx.AsyncClient(
                timeout=self.config.tmdb_request_timeout,
            ) as client:
                response = await client.get(url, params=params)
                response.raise_for_status()
                return response.json()
        except httpx.HTTPError as exc:  # pragma: no cover - network failure path
            logger.warning("TMDB request failed for %s: %s", endpoint, exc)
            return None

    def _score_match(
        self,
        media_info: MediaInfo,
        runtime_hint: int | None,
        year_hint: int | None,
    ) -> float:
        score = 0.5

        if year_hint and media_info.year:
            if abs(media_info.year - year_hint) <= 1:
                score += 0.2

        if runtime_hint and media_info.runtime:
            if (
                abs(media_info.runtime - runtime_hint)
                <= self.config.tmdb_runtime_tolerance_minutes
            ):
                score += 0.2

        if media_info.genres:
            score += 0.05

        return min(score, 0.95)

    def _guess_season(self, seasons_payload: list[dict[str, Any]] | None) -> int | None:
        if not seasons_payload:
            return None

        for season in seasons_payload:
            if season.get("season_number") == 1:
                return 1

        # Fallback to first non-zero season number
        for season in seasons_payload:
            number = season.get("season_number")
            if number:
                return number

        return None


def query_fallback(payload: dict[str, Any]) -> str:
    """Use common fields to derive a fallback title if TMDB omits it."""

    return (
        payload.get("original_title")
        or payload.get("name")
        or payload.get("title")
        or "Unknown"
    )


__all__ = ["MediaInfo", "TMDBService"]
