"""TMDB API result caching using SQLite."""

import json
import logging
import sqlite3
import time
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path

logger = logging.getLogger(__name__)


@dataclass
class CachedResult:
    """Cached TMDB search result."""

    query: str
    media_type: str
    results: list[dict]
    detailed_info: dict | None
    timestamp: float
    ttl_days: int

    def is_valid(self) -> bool:
        """Check if cache entry is still valid."""
        age_days = (time.time() - self.timestamp) / (24 * 3600)
        return age_days < self.ttl_days

    def to_dict(self) -> dict:
        """Convert to dictionary for storage."""
        return {
            "query": self.query,
            "media_type": self.media_type,
            "results": self.results,
            "detailed_info": self.detailed_info,
            "timestamp": self.timestamp,
            "ttl_days": self.ttl_days,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "CachedResult":
        """Create from dictionary."""
        return cls(
            query=data["query"],
            media_type=data["media_type"],
            results=data["results"],
            detailed_info=data.get("detailed_info"),
            timestamp=data["timestamp"],
            ttl_days=data["ttl_days"],
        )


class TMDBCache:
    """SQLite-based cache for TMDB API results."""

    def __init__(self, cache_dir: Path, ttl_days: int = 30):
        self.cache_dir = cache_dir
        self.cache_dir.mkdir(parents=True, exist_ok=True)
        self.db_path = cache_dir / "tmdb_cache.db"
        self.ttl_days = ttl_days
        self.logger = logging.getLogger(__name__)
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
        """Initialize the cache database."""
        try:
            with self._get_connection() as conn:
                conn.execute(
                    """
                    CREATE TABLE IF NOT EXISTS tmdb_cache (
                        id INTEGER PRIMARY KEY AUTOINCREMENT,
                        query_hash TEXT UNIQUE NOT NULL,
                        query TEXT NOT NULL,
                        media_type TEXT NOT NULL,
                        results TEXT NOT NULL,
                        detailed_info TEXT,
                        timestamp REAL NOT NULL,
                        ttl_days INTEGER NOT NULL,
                        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                    )
                """,
                )

                # Create index for faster lookups
                conn.execute(
                    """
                    CREATE INDEX IF NOT EXISTS idx_query_hash
                    ON tmdb_cache (query_hash)
                """,
                )

                conn.commit()
                self.logger.debug(f"Initialized TMDB cache database at {self.db_path}")

        except sqlite3.Error as e:
            self.logger.exception(f"Failed to initialize cache database: {e}")
            raise

    def _hash_query(self, query: str, media_type: str = "movie") -> str:
        """Create a hash for the query to use as cache key."""
        import hashlib

        key = f"{query.lower().strip()}:{media_type}"
        return hashlib.sha256(key.encode()).hexdigest()

    def search_cache(
        self,
        query: str,
        media_type: str = "movie",
    ) -> CachedResult | None:
        """Search for cached results."""
        query_hash = self._hash_query(query, media_type)

        try:
            with self._get_connection() as conn:
                cursor = conn.execute(
                    """
                    SELECT query, media_type, results, detailed_info, timestamp, ttl_days
                    FROM tmdb_cache
                    WHERE query_hash = ?
                """,
                    (query_hash,),
                )

                row = cursor.fetchone()
                if not row:
                    return None

                # Parse JSON data
                results = json.loads(row[2])
                detailed_info = json.loads(row[3]) if row[3] else None

                cached_result = CachedResult(
                    query=row[0],
                    media_type=row[1],
                    results=results,
                    detailed_info=detailed_info,
                    timestamp=row[4],
                    ttl_days=row[5],
                )

                if cached_result.is_valid():
                    self.logger.debug(f"Cache hit for query: {query} ({media_type})")
                    return cached_result
                # Remove expired entry
                self._remove_expired_entry(query_hash)
                self.logger.debug(
                    f"Cache expired for query: {query} ({media_type})",
                )
                return None

        except sqlite3.Error as e:
            self.logger.exception(f"Failed to search cache: {e}")
            return None
        except json.JSONDecodeError as e:
            self.logger.exception(f"Failed to decode cached JSON: {e}")
            # Remove corrupted entry
            self._remove_expired_entry(query_hash)
            return None

    def cache_results(
        self,
        query: str,
        results: list[dict],
        media_type: str = "movie",
        detailed_info: dict | None = None,
    ) -> bool:
        """Cache search results."""
        query_hash = self._hash_query(query, media_type)
        timestamp = time.time()

        try:
            with self._get_connection() as conn:
                conn.execute(
                    """
                    INSERT OR REPLACE INTO tmdb_cache
                    (query_hash, query, media_type, results, detailed_info, timestamp, ttl_days)
                    VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                    (
                        query_hash,
                        query,
                        media_type,
                        json.dumps(results),
                        json.dumps(detailed_info) if detailed_info else None,
                        timestamp,
                        self.ttl_days,
                    ),
                )

                conn.commit()
                self.logger.debug(f"Cached results for query: {query} ({media_type})")
                return True

        except sqlite3.Error as e:
            self.logger.exception(f"Failed to cache results: {e}")
            return False
        except (TypeError, ValueError) as e:
            self.logger.exception(f"Failed to encode JSON for caching: {e}")
            return False

    def _remove_expired_entry(self, query_hash: str) -> None:
        """Remove a specific expired entry."""
        try:
            with self._get_connection() as conn:
                conn.execute(
                    "DELETE FROM tmdb_cache WHERE query_hash = ?",
                    (query_hash,),
                )
                conn.commit()
        except sqlite3.Error as e:
            self.logger.exception(f"Failed to remove expired cache entry: {e}")

    def cleanup_expired(self) -> int:
        """Remove all expired cache entries."""
        current_time = time.time()
        removed_count = 0

        try:
            with self._get_connection() as conn:
                # Find expired entries
                cursor = conn.execute(
                    """
                    SELECT id, timestamp, ttl_days FROM tmdb_cache
                """,
                )

                expired_ids = []
                for row in cursor.fetchall():
                    entry_id, timestamp, ttl_days = row
                    age_days = (current_time - timestamp) / (24 * 3600)
                    if age_days >= ttl_days:
                        expired_ids.append(entry_id)

                # Remove expired entries
                if expired_ids:
                    placeholders = ",".join("?" * len(expired_ids))
                    # Safe: only using placeholders, no user data in query construction
                    query = (
                        "DELETE FROM tmdb_cache WHERE id IN ({})".format(  # noqa: UP032
                            placeholders,
                        )
                    )
                    conn.execute(query, expired_ids)
                    removed_count = conn.total_changes
                    conn.commit()

                self.logger.debug(f"Removed {removed_count} expired cache entries")
                return removed_count

        except sqlite3.Error as e:
            self.logger.exception(f"Failed to cleanup expired entries: {e}")
            return 0

    def get_cache_stats(self) -> dict:
        """Get cache statistics."""
        try:
            with self._get_connection() as conn:
                cursor = conn.execute(
                    """
                    SELECT
                        COUNT(*) as total_entries,
                        COUNT(CASE WHEN media_type = 'movie' THEN 1 END) as movie_entries,
                        COUNT(CASE WHEN media_type = 'tv' THEN 1 END) as tv_entries,
                        MIN(created_at) as oldest_entry,
                        MAX(created_at) as newest_entry
                    FROM tmdb_cache
                """,
                )

                row = cursor.fetchone()
                if row:
                    return {
                        "total_entries": row[0],
                        "movie_entries": row[1],
                        "tv_entries": row[2],
                        "oldest_entry": row[3],
                        "newest_entry": row[4],
                        "cache_file_size": (
                            self.db_path.stat().st_size if self.db_path.exists() else 0
                        ),
                    }

        except sqlite3.Error as e:
            self.logger.exception(f"Failed to get cache stats: {e}")

        return {}

    def clear_cache(self) -> bool:
        """Clear all cache entries."""
        try:
            with self._get_connection() as conn:
                conn.execute("DELETE FROM tmdb_cache")
                conn.commit()
                self.logger.info("Cleared all TMDB cache entries")
                return True

        except sqlite3.Error as e:
            self.logger.exception(f"Failed to clear cache: {e}")
            return False
