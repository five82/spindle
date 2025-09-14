"""Series metadata caching for consistent multi-disc TV series processing."""

import json
import logging
import sqlite3
import time
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import asdict, dataclass
from typing import Any

from spindle.config import SpindleConfig
from spindle.services.tmdb import MediaInfo

logger = logging.getLogger(__name__)


@dataclass
class SeriesMetadata:
    """Cached series metadata for consistent disc processing."""

    series_title: str
    season_number: int | None
    media_info: MediaInfo | None
    tmdb_id: int | None = None
    cached_at: float | None = None

    def __post_init__(self) -> None:
        if self.cached_at is None:
            self.cached_at = time.time()

    def to_dict(self) -> dict:
        """Convert to dictionary for JSON storage."""
        data = asdict(self)
        # Convert MediaInfo to dict if present
        if self.media_info:
            data["media_info"] = {
                "title": self.media_info.title,
                "year": self.media_info.year,
                "media_type": self.media_info.media_type,
                "tmdb_id": self.media_info.tmdb_id,
                "overview": getattr(self.media_info, "overview", ""),
                "genres": getattr(self.media_info, "genres", []),
            }
        return data

    @classmethod
    def from_dict(cls, data: dict) -> "SeriesMetadata":
        """Create from dictionary."""
        # Reconstruct MediaInfo if present
        media_info = None
        if data.get("media_info"):
            mi_data = data["media_info"]
            media_info = MediaInfo(
                title=mi_data["title"],
                year=mi_data["year"],
                media_type=mi_data["media_type"],
                tmdb_id=mi_data["tmdb_id"],
            )
            # Add optional attributes
            if "overview" in mi_data:
                media_info.overview = mi_data["overview"]
            if "genres" in mi_data:
                media_info.genres = mi_data["genres"]

        return cls(
            series_title=data["series_title"],
            season_number=data["season_number"],
            media_info=media_info,
            tmdb_id=data.get("tmdb_id"),
            cached_at=data.get("cached_at", time.time()),
        )


class SeriesCache:
    """Cache for TV series metadata to ensure consistent processing across discs."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.cache_dir = config.log_dir
        self.db_path = self.cache_dir / "series_cache.db"
        self.ttl_days = getattr(config, "series_cache_ttl_days", 90)  # 3 months
        self.logger = logging.getLogger(__name__)

        # Ensure cache directory exists
        self.cache_dir.mkdir(parents=True, exist_ok=True)

        # Initialize database
        self._init_database()

    @contextmanager
    def _get_connection(self) -> Iterator[sqlite3.Connection]:
        """Get a database connection that is properly closed with transaction support."""
        conn = sqlite3.connect(self.db_path)
        try:
            yield conn
            conn.commit()
        except Exception:
            conn.rollback()
            raise
        finally:
            conn.close()

    def _init_database(self) -> None:
        """Initialize the SQLite database."""
        with self._get_connection() as conn:
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS series_cache (
                    cache_key TEXT PRIMARY KEY,
                    series_title TEXT NOT NULL,
                    season_number INTEGER,
                    metadata_json TEXT NOT NULL,
                    cached_at REAL NOT NULL,
                    accessed_at REAL NOT NULL
                )
            """,
            )

            # Create index for cleanup queries
            conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_cached_at ON series_cache(cached_at)
            """,
            )

            conn.commit()

    def _generate_cache_key(self, series_title: str, season_number: int | None) -> str:
        """Generate cache key for series and season."""
        season_str = str(season_number) if season_number is not None else "0"
        # Normalize the title for consistent caching
        normalized_title = series_title.upper().replace(" ", "_").replace("-", "_")
        return f"{normalized_title}_S{season_str}"

    def cache_series_metadata(
        self,
        series_title: str,
        season_number: int | None,
        media_info: MediaInfo | None,
    ) -> None:
        """Cache series metadata for future disc processing."""
        cache_key = self._generate_cache_key(series_title, season_number)

        metadata = SeriesMetadata(
            series_title=series_title,
            season_number=season_number,
            media_info=media_info,
            tmdb_id=media_info.tmdb_id if media_info else None,
        )

        metadata_json = json.dumps(metadata.to_dict())
        current_time = time.time()

        with self._get_connection() as conn:
            conn.execute(
                """
                INSERT OR REPLACE INTO series_cache
                (cache_key, series_title, season_number, metadata_json, cached_at, accessed_at)
                VALUES (?, ?, ?, ?, ?, ?)
            """,
                (
                    cache_key,
                    series_title,
                    season_number,
                    metadata_json,
                    current_time,
                    current_time,
                ),
            )
            conn.commit()

        self.logger.info(f"Cached series metadata: {series_title} S{season_number}")

    def get_series_metadata(
        self,
        series_title: str,
        season_number: int | None,
    ) -> SeriesMetadata | None:
        """Get cached series metadata."""
        cache_key = self._generate_cache_key(series_title, season_number)

        with self._get_connection() as conn:
            conn.row_factory = sqlite3.Row
            cursor = conn.execute(
                """
                SELECT metadata_json, cached_at FROM series_cache
                WHERE cache_key = ?
            """,
                (cache_key,),
            )

            row = cursor.fetchone()
            if not row:
                return None

            # Check if cache entry is still valid
            cached_at = row["cached_at"]
            if time.time() - cached_at > (self.ttl_days * 24 * 3600):
                # Cache expired, delete it
                conn.execute(
                    "DELETE FROM series_cache WHERE cache_key = ?",
                    (cache_key,),
                )
                conn.commit()
                return None

            # Update access time
            conn.execute(
                """
                UPDATE series_cache SET accessed_at = ? WHERE cache_key = ?
            """,
                (time.time(), cache_key),
            )
            conn.commit()

            # Deserialize metadata
            try:
                metadata_dict = json.loads(row["metadata_json"])
                metadata = SeriesMetadata.from_dict(metadata_dict)
                self.logger.info(
                    f"Retrieved cached series metadata: {series_title} S{season_number}",
                )
                return metadata
            except (json.JSONDecodeError, KeyError, TypeError) as e:
                self.logger.warning(
                    f"Failed to deserialize cached metadata for {cache_key}: {e}",
                )
                # Delete corrupted entry
                conn.execute(
                    "DELETE FROM series_cache WHERE cache_key = ?",
                    (cache_key,),
                )
                conn.commit()
                return None

    def cleanup_expired_entries(self) -> int:
        """Clean up expired cache entries and return count of deleted entries."""
        cutoff_time = time.time() - (self.ttl_days * 24 * 3600)

        with self._get_connection() as conn:
            cursor = conn.execute(
                """
                DELETE FROM series_cache WHERE cached_at < ?
            """,
                (cutoff_time,),
            )

            deleted_count = cursor.rowcount
            conn.commit()

            if deleted_count > 0:
                self.logger.info(
                    f"Cleaned up {deleted_count} expired series cache entries",
                )

            return deleted_count

    def get_cache_stats(self) -> dict[str, Any]:
        """Get cache statistics."""
        with self._get_connection() as conn:
            conn.row_factory = sqlite3.Row

            # Total entries
            cursor = conn.execute("SELECT COUNT(*) as total FROM series_cache")
            total = cursor.fetchone()["total"]

            # Recent entries (last 7 days)
            recent_cutoff = time.time() - (7 * 24 * 3600)
            cursor = conn.execute(
                """
                SELECT COUNT(*) as recent FROM series_cache
                WHERE cached_at > ?
            """,
                (recent_cutoff,),
            )
            recent = cursor.fetchone()["recent"]

            # Most accessed series
            cursor = conn.execute(
                """
                SELECT series_title, season_number, COUNT(*) as access_count
                FROM series_cache
                GROUP BY series_title, season_number
                ORDER BY access_count DESC
                LIMIT 5
            """,
            )
            popular = [dict(row) for row in cursor.fetchall()]

            return {
                "total_entries": total,
                "recent_entries": recent,
                "popular_series": popular,
                "ttl_days": self.ttl_days,
            }

    def clear_all(self) -> int:
        """Clear all cache entries (useful for testing)."""
        with self._get_connection() as conn:
            cursor = conn.execute("DELETE FROM series_cache")
            deleted_count = cursor.rowcount
            conn.commit()

            self.logger.info(
                f"Cleared all series cache entries ({deleted_count} deleted)",
            )
            return deleted_count
