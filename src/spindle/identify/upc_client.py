"""UPCitemdb.com API client for barcode-to-product lookups."""

import json
import logging
import re
import sqlite3
import time
from dataclasses import dataclass
from pathlib import Path

import httpx

from spindle.config import SpindleConfig

logger = logging.getLogger(__name__)


@dataclass
class UPCProduct:
    """Product information from UPC lookup."""

    upc: str
    title: str
    brand: str | None = None
    category: str | None = None
    description: str | None = None
    raw_response: dict | None = None

    @property
    def is_media_product(self) -> bool:
        """Check if this appears to be a media product (movie/TV)."""
        if not self.category:
            return False

        media_categories = [
            "movies",
            "tv",
            "dvd",
            "blu-ray",
            "bluray",
            "blu ray",
            "video",
            "entertainment",
            "film",
            "series",
            "season",
        ]

        category_lower = self.category.lower()
        return any(keyword in category_lower for keyword in media_categories)

    def extract_media_info(self) -> tuple[str | None, int | None]:
        """Extract media title and year from product information."""
        if not self.is_media_product:
            return None, None

        # Try to extract meaningful title and year
        title = self.title
        year = None

        # Remove common disc/format indicators
        title = re.sub(
            r"\[?(DVD|Blu-?ray|4K|UHD|Digital|HD)\]?",
            "",
            title,
            flags=re.IGNORECASE,
        ).strip()

        # Extract year from title
        year_match = re.search(r"\(?(\d{4})\)?", title)
        if year_match:
            year = int(year_match.group(1))
            title = re.sub(r"\s*\(?(\d{4})\)?\s*", "", title).strip()

        # Clean up title
        title = re.sub(r"\s+", " ", title).strip()

        # Remove extra metadata like "Special Edition", "Director's Cut", etc.
        title = re.sub(
            r"\s*[-—]\s*(Special Edition|Director'?s Cut|Extended|Theatrical|Collector'?s Edition|Limited Edition).*?$",
            "",
            title,
            flags=re.IGNORECASE,
        ).strip()

        return title if title else None, year


class UPCCache:
    """SQLite-based cache for UPC lookup results."""

    def __init__(self, cache_dir: Path):
        self.cache_dir = cache_dir
        self.cache_dir.mkdir(parents=True, exist_ok=True)
        self.db_path = cache_dir / "upc_cache.db"
        self._init_database()

    def _init_database(self) -> None:
        """Initialize the UPC cache database."""
        with sqlite3.connect(self.db_path) as conn:
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS upc_lookups (
                    upc TEXT PRIMARY KEY,
                    title TEXT,
                    brand TEXT,
                    category TEXT,
                    description TEXT,
                    is_media INTEGER,
                    raw_response TEXT,
                    cached_at INTEGER,
                    hit_count INTEGER DEFAULT 0
                )
            """,
            )

            # Create index for faster lookups
            conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_cached_at
                ON upc_lookups(cached_at)
            """,
            )

    def get(self, upc: str) -> UPCProduct | None:
        """Get cached UPC product information."""
        with sqlite3.connect(self.db_path) as conn:
            conn.row_factory = sqlite3.Row
            cursor = conn.cursor()

            cursor.execute(
                """
                SELECT * FROM upc_lookups WHERE upc = ?
            """,
                (upc,),
            )

            row = cursor.fetchone()
            if not row:
                return None

            # Update hit count
            cursor.execute(
                """
                UPDATE upc_lookups SET hit_count = hit_count + 1 WHERE upc = ?
            """,
                (upc,),
            )

            # Reconstruct UPCProduct
            raw_response = (
                json.loads(row["raw_response"]) if row["raw_response"] else None
            )

            return UPCProduct(
                upc=row["upc"],
                title=row["title"],
                brand=row["brand"],
                category=row["category"],
                description=row["description"],
                raw_response=raw_response,
            )

    def set(self, upc: str, product: UPCProduct) -> None:
        """Cache UPC product information."""
        with sqlite3.connect(self.db_path) as conn:
            raw_response_json = (
                json.dumps(product.raw_response) if product.raw_response else None
            )

            conn.execute(
                """
                INSERT OR REPLACE INTO upc_lookups
                (upc, title, brand, category, description, is_media, raw_response, cached_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
                (
                    upc,
                    product.title,
                    product.brand,
                    product.category,
                    product.description,
                    1 if product.is_media_product else 0,
                    raw_response_json,
                    int(time.time()),
                ),
            )

    def cleanup_old_entries(self, max_age_days: int = 30) -> None:
        """Remove old cache entries."""
        cutoff_time = int(time.time()) - (max_age_days * 24 * 60 * 60)

        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.cursor()
            cursor.execute(
                """
                DELETE FROM upc_lookups WHERE cached_at < ?
            """,
                (cutoff_time,),
            )

            deleted_count = cursor.rowcount
            if deleted_count > 0:
                logger.info(f"Cleaned up {deleted_count} old UPC cache entries")


class UPCItemDBClient:
    """Client for UPCitemdb.com API with caching."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.api_key = getattr(config, "upcitemdb_api_key", None)
        self.base_url = "https://api.upcitemdb.com/prod/trial"
        self.client = httpx.Client(timeout=10.0)

        # Initialize cache
        self.cache = UPCCache(config.log_dir / "cache")

        # Rate limiting (respecting free tier limits)
        self._request_count = 0
        self._request_window_start = time.time()
        self._max_requests_per_day = 100 if not self.api_key else 20000

    async def lookup_product(self, upc: str) -> UPCProduct | None:
        """Look up product information by UPC code."""
        # Clean UPC code
        upc = re.sub(r"[^\d]", "", upc)
        if not upc or not upc.isdigit():
            logger.warning(f"Invalid UPC format: {upc}")
            return None

        # Check cache first
        cached_product = self.cache.get(upc)
        if cached_product:
            logger.debug(f"UPC {upc} found in cache")
            return cached_product

        # Check rate limits
        if not self._check_rate_limit():
            logger.warning("UPC API rate limit exceeded")
            return None

        try:
            # Make API request
            logger.info(f"Looking up UPC: {upc}")

            headers = {}
            if self.api_key:
                headers["Authorization"] = f"Bearer {self.api_key}"

            response = self.client.get(
                f"{self.base_url}/lookup",
                params={"upc": upc},
                headers=headers,
            )

            response.raise_for_status()
            data = response.json()

            self._request_count += 1

            # Parse response
            if not data.get("items"):
                logger.debug(f"No product found for UPC: {upc}")
                # Cache negative results to avoid repeat lookups
                empty_product = UPCProduct(upc=upc, title="Not Found")
                self.cache.set(upc, empty_product)
                return None

            item = data["items"][0]  # Take first result

            product = UPCProduct(
                upc=upc,
                title=item.get("title", ""),
                brand=item.get("brand"),
                category=item.get("category"),
                description=item.get("description"),
                raw_response=data,
            )

            # Cache the result
            self.cache.set(upc, product)

            logger.info(f"Found product: {product.title} ({product.category})")
            return product

        except httpx.HTTPStatusError as e:
            if e.response.status_code == 429:
                logger.warning("UPC API rate limit exceeded (429)")
            else:
                logger.warning(
                    f"UPC API error {e.response.status_code}: {e.response.text}",
                )
            return None
        except Exception as e:
            logger.warning(f"UPC lookup failed: {e}")
            return None

    def _check_rate_limit(self) -> bool:
        """Check if we're within rate limits."""
        now = time.time()

        # Reset window if more than 24 hours have passed
        if now - self._request_window_start > 24 * 60 * 60:
            self._request_count = 0
            self._request_window_start = now

        return self._request_count < self._max_requests_per_day

    def get_cache_stats(self) -> dict:
        """Get cache statistics."""
        with sqlite3.connect(self.cache.db_path) as conn:
            cursor = conn.cursor()

            # Total entries
            cursor.execute("SELECT COUNT(*) FROM upc_lookups")
            total_count = cursor.fetchone()[0]

            # Media entries
            cursor.execute("SELECT COUNT(*) FROM upc_lookups WHERE is_media = 1")
            media_count = cursor.fetchone()[0]

            # Total hits
            cursor.execute("SELECT SUM(hit_count) FROM upc_lookups")
            total_hits = cursor.fetchone()[0] or 0

            return {
                "total_entries": total_count,
                "media_entries": media_count,
                "total_hits": total_hits,
                "api_requests_today": self._request_count,
            }
