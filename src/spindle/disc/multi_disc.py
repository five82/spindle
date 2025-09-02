"""Simple multi-disc handling with series caching for individual disc processing."""

import logging
import re
from dataclasses import dataclass

from spindle.config import SpindleConfig
from spindle.disc.metadata_extractor import EnhancedDiscMetadata
from spindle.disc.monitor import DiscInfo
from spindle.disc.series_cache import SeriesCache
from spindle.identify.tmdb import MediaInfo

logger = logging.getLogger(__name__)


@dataclass
class TVSeriesDiscInfo:
    """Information about a TV series disc for individual processing."""

    series_title: str
    season_number: int | None = None
    disc_number: int | None = None
    is_tv_series: bool = True


class SimpleMultiDiscManager:
    """Simple multi-disc manager that processes each disc individually while caching series metadata."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.logger = logging.getLogger(__name__)
        self.series_cache = SeriesCache(config)

    def detect_tv_series_disc(
        self,
        disc_info: DiscInfo,
        enhanced_metadata: EnhancedDiscMetadata,
    ) -> TVSeriesDiscInfo | None:
        """Detect if this is a TV series disc and extract series information."""

        # Check if this is a TV series disc
        if not enhanced_metadata.is_tv_series():
            return None

        # Extract series title, season, and disc number
        series_title = self._extract_series_title(enhanced_metadata, disc_info)
        season, disc_num = enhanced_metadata.get_season_disc_info()

        if series_title:
            tv_info = TVSeriesDiscInfo(
                series_title=series_title,
                season_number=season,
                disc_number=disc_num,
                is_tv_series=True,
            )

            self.logger.info(
                f"Detected TV series disc: {series_title} Season {season}, Disc {disc_num}",
            )
            return tv_info

        return None

    def _extract_series_title(
        self,
        metadata: EnhancedDiscMetadata,
        disc_info: DiscInfo,
    ) -> str:
        """Extract the series title from metadata, removing disc/season info."""

        # Try title candidates in priority order
        candidates = metadata.get_best_title_candidates()

        for candidate in candidates:
            if candidate:
                # Clean up the title by removing disc/season patterns
                cleaned = candidate
                cleaned = re.sub(
                    r"\s*-\s*Season\s+\d+.*$",
                    "",
                    cleaned,
                    flags=re.IGNORECASE,
                )
                cleaned = re.sub(
                    r"\s*Season\s+\d+.*$",
                    "",
                    cleaned,
                    flags=re.IGNORECASE,
                )
                cleaned = re.sub(r"\s*Disc\s+\d+.*$", "", cleaned, flags=re.IGNORECASE)
                cleaned = re.sub(r"\s*S\d+.*$", "", cleaned, flags=re.IGNORECASE)
                cleaned = cleaned.strip()

                if cleaned and not self._is_generic_label(cleaned):
                    return cleaned

        # Fallback to disc label cleanup
        cleaned_label = disc_info.label
        cleaned_label = re.sub(
            r"_S\d+_DISC_\d+$",
            "",
            cleaned_label,
            flags=re.IGNORECASE,
        )
        cleaned_label = re.sub(r"_DISC_\d+$", "", cleaned_label, flags=re.IGNORECASE)
        cleaned_label = cleaned_label.replace("_", " ")

        return cleaned_label.strip()

    def _is_generic_label(self, label: str) -> bool:
        """Check if label is too generic."""
        if not label or len(label) < 3:
            return True

        generic_patterns = [
            "LOGICAL_VOLUME_ID",
            "DVD_VIDEO",
            "BLURAY",
            "BD_ROM",
            "UNTITLED",
            r"^\d+$",
        ]

        for pattern in generic_patterns:
            if re.match(pattern, label, re.IGNORECASE):
                return True

        return False

    def get_or_cache_series_metadata(
        self,
        tv_info: TVSeriesDiscInfo,
        media_info: MediaInfo | None = None,
    ) -> MediaInfo | None:
        """Get cached series metadata or cache new metadata for future discs."""

        # Try to get cached metadata first
        cached_metadata = self.series_cache.get_series_metadata(
            tv_info.series_title,
            tv_info.season_number,
        )

        if cached_metadata and cached_metadata.media_info:
            self.logger.info(
                f"Using cached metadata for {tv_info.series_title} S{tv_info.season_number}",
            )
            return cached_metadata.media_info

        # If we have new media info, cache it for future discs
        if media_info:
            self.series_cache.cache_series_metadata(
                tv_info.series_title,
                tv_info.season_number,
                media_info,
            )
            self.logger.info(
                f"Cached new metadata for {tv_info.series_title} S{tv_info.season_number}",
            )
            return media_info

        # No cached or new metadata available
        self.logger.info(
            f"No metadata available for {tv_info.series_title} S{tv_info.season_number}",
        )
        return None

    def process_tv_series_disc(
        self,
        disc_info: DiscInfo,
        enhanced_metadata: EnhancedDiscMetadata,
        media_info: MediaInfo | None = None,
    ) -> tuple[TVSeriesDiscInfo | None, MediaInfo | None]:
        """Process a TV series disc individually with metadata caching."""

        # Detect if this is a TV series disc
        tv_info = self.detect_tv_series_disc(disc_info, enhanced_metadata)
        if not tv_info:
            return None, None

        # Get or cache series metadata
        consistent_media_info = self.get_or_cache_series_metadata(tv_info, media_info)

        return tv_info, consistent_media_info

    def cleanup_expired_cache(self) -> int:
        """Clean up expired cache entries."""
        return self.series_cache.cleanup_expired_entries()

    def get_cache_stats(self) -> dict:
        """Get series cache statistics."""
        return self.series_cache.get_cache_stats()
