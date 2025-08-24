"""Intelligent disc content analysis and classification."""

import logging
import statistics
from dataclasses import dataclass
from enum import Enum
from typing import Any

from spindle.config import SpindleConfig
from spindle.identify.tmdb import TMDBClient

from .monitor import DiscInfo
from .ripper import Title

logger = logging.getLogger(__name__)


class ContentType(Enum):
    """Types of content that can be on a disc."""

    MOVIE = "movie"
    TV_SERIES = "tv_series"
    CARTOON_COLLECTION = "cartoon_collection"
    CARTOON_SHORTS = "cartoon_shorts"
    CARTOON_SERIES = "cartoon_series"
    ANIMATED_MOVIE = "animated_movie"
    ANIMATED_SERIES = "animated_series"
    DOCUMENTARY = "documentary"
    MUSIC_VIDEO = "music_video"
    CONCERT = "concert"
    UNKNOWN = "unknown"


@dataclass
class ContentPattern:
    """Pattern analysis result for disc titles."""

    type: ContentType
    confidence: float
    episode_count: int | None = None
    episode_duration: int | None = None
    main_feature_duration: int | None = None
    extras_count: int | None = None
    segments: int | None = None


@dataclass
class SeriesInfo:
    """Information about a TV series."""

    name: str
    tmdb_id: int
    detected_season: int | None = None
    year: int | None = None


@dataclass
class EpisodeInfo:
    """Information about a specific TV episode."""

    season_number: int
    episode_number: int
    episode_title: str
    air_date: str | None = None
    overview: str = ""
    runtime: int | None = None


@dataclass
class DiscAnalysisResult:
    """Complete analysis result for a disc."""

    disc_info: DiscInfo
    content_type: ContentType
    confidence: float
    titles_to_rip: list[Title]
    metadata: Any | None = None
    episode_mappings: dict[Title, EpisodeInfo] | None = None


class IntelligentDiscAnalyzer:
    """Comprehensive disc analysis and content detection."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBClient(config)

    async def analyze_disc(
        self,
        disc_info: DiscInfo,
        titles: list[Title],
    ) -> DiscAnalysisResult:
        """Complete disc analysis workflow."""

        logger.info(f"Analyzing disc: {disc_info}")

        # Phase 1: Multi-API content identification
        content_candidates = await self.identify_content_multi_api(
            disc_label=disc_info.label,
            titles=titles,
            disc_type=disc_info.disc_type,
        )

        # Phase 2: Pattern analysis fallback
        if not content_candidates:
            content_candidates = self.analyze_title_patterns(titles, disc_info.label)

        # Phase 3: Intelligent title selection
        selected_titles = await self.select_titles_intelligently(
            titles=titles,
            content_pattern=content_candidates,
            disc_label=disc_info.label,
        )

        return DiscAnalysisResult(
            disc_info=disc_info,
            content_type=content_candidates.type,
            confidence=content_candidates.confidence,
            titles_to_rip=selected_titles,
            metadata=content_candidates,
        )

    async def identify_content_multi_api(
        self,
        disc_label: str,
        titles: list[Title],
        disc_type: str,
    ) -> ContentPattern | None:
        """Use multiple APIs for robust identification."""

        # Start with disc label analysis
        label_cleaned = self.clean_disc_label(disc_label)

        # Try TMDB identification
        tmdb_results = await self.query_tmdb(label_cleaned)

        # Content pattern analysis from titles
        pattern_analysis = self.analyze_title_patterns(titles, disc_label)

        # Combine and score results
        return self.combine_api_and_pattern_results(tmdb_results, pattern_analysis)

    def clean_disc_label(self, disc_label: str) -> str:
        """Clean up disc label for API queries."""
        import re

        # Remove common disc indicators
        cleaned = re.sub(
            r"\b(disc|disk|cd|dvd|bluray|blu-ray)\s*\d*\b",
            "",
            disc_label,
            flags=re.IGNORECASE,
        )

        # Remove special characters and normalize spaces
        cleaned = re.sub(r"[._-]", " ", cleaned)
        return re.sub(r"\s+", " ", cleaned).strip()

    async def query_tmdb(self, label: str) -> dict[str, Any] | None:
        """Query TMDB for content identification."""
        try:
            # Try both movie and TV searches
            movie_results = await self.tmdb.search_movie(label)
            tv_results = await self.tmdb.search_tv(label)

            # Return the best match
            if movie_results and tv_results:
                # Compare popularity/vote_average to determine best match
                movie_score = movie_results[0].get("popularity", 0)
                tv_score = tv_results[0].get("popularity", 0)

                if movie_score > tv_score:
                    return {"type": "movie", "data": movie_results[0]}
                return {"type": "tv", "data": tv_results[0]}
            if movie_results:
                return {"type": "movie", "data": movie_results[0]}
            if tv_results:
                return {"type": "tv", "data": tv_results[0]}

        except Exception as e:
            logger.warning(f"TMDB query failed for '{label}': {e}")

        return None

    def combine_api_and_pattern_results(
        self,
        api_result: dict | None,
        pattern_result: ContentPattern,
    ) -> ContentPattern:
        """Combine API data with pattern analysis."""

        if not api_result:
            return pattern_result

        # Use API data to enhance pattern analysis
        api_type = api_result["type"]
        api_data = api_result["data"]

        if api_type == "movie":
            genres = [g.get("name", "").lower() for g in api_data.get("genres", [])]

            if "animation" in genres:
                return ContentPattern(
                    type=ContentType.ANIMATED_MOVIE,
                    confidence=0.9,
                )
            return ContentPattern(
                type=ContentType.MOVIE,
                confidence=0.9,
            )

        if api_type == "tv":
            genres = [g.get("name", "").lower() for g in api_data.get("genres", [])]

            if "animation" in genres:
                # Check episode length to distinguish cartoon types
                episode_runtimes = api_data.get("episode_run_time", [])
                if episode_runtimes:
                    avg_runtime = sum(episode_runtimes) / len(episode_runtimes)

                    if avg_runtime <= 12:
                        return ContentPattern(
                            type=ContentType.CARTOON_SHORTS,
                            confidence=0.9,
                            episode_duration=int(avg_runtime * 60),
                        )
                    if avg_runtime <= 25:
                        return ContentPattern(
                            type=ContentType.CARTOON_SERIES,
                            confidence=0.9,
                            episode_duration=int(avg_runtime * 60),
                        )
                    return ContentPattern(
                        type=ContentType.ANIMATED_SERIES,
                        confidence=0.9,
                        episode_duration=int(avg_runtime * 60),
                    )

                return ContentPattern(
                    type=ContentType.ANIMATED_SERIES,
                    confidence=0.8,
                )
            return ContentPattern(
                type=ContentType.TV_SERIES,
                confidence=0.9,
            )

        # Fallback to pattern analysis
        return pattern_result

    def analyze_title_patterns(
        self,
        titles: list[Title],
        disc_label: str,
    ) -> ContentPattern:
        """Analyze title durations/structure to infer content type."""

        if not titles:
            return ContentPattern(type=ContentType.UNKNOWN, confidence=0.1)

        durations = [t.duration for t in titles]

        # Check for cartoon collection patterns first
        if self.has_cartoon_collection_pattern(durations, disc_label):
            return ContentPattern(
                type=ContentType.CARTOON_COLLECTION,
                confidence=0.8,
                episode_count=len([d for d in durations if 3 * 60 <= d <= 15 * 60]),
                episode_duration=(
                    int(
                        statistics.median(
                            [d for d in durations if 3 * 60 <= d <= 15 * 60],
                        ),
                    )
                    if durations
                    else 0
                ),
            )

        # TV Show patterns
        if self.has_consistent_episode_durations(durations):
            # Filter to episode-length titles for analysis
            episode_durations = [d for d in durations if d >= 10 * 60]
            median_duration = int(statistics.median(episode_durations))

            # Short episodes = likely cartoons
            if median_duration <= 15 * 60:
                return ContentPattern(
                    type=ContentType.CARTOON_SHORTS,
                    confidence=0.7,
                    episode_count=len(episode_durations),
                    episode_duration=median_duration,
                )
            # Standard TV episodes
            return ContentPattern(
                type=ContentType.TV_SERIES,
                confidence=0.8,
                episode_count=len(episode_durations),
                episode_duration=median_duration,
            )

        # Movie patterns
        if self.has_single_long_title(durations):
            return ContentPattern(
                type=ContentType.MOVIE,
                confidence=0.9,
                main_feature_duration=max(durations),
                extras_count=len([d for d in durations if d < 3600]),
            )

        # Documentary/Special patterns
        if self.has_documentary_pattern(durations):
            return ContentPattern(
                type=ContentType.DOCUMENTARY,
                confidence=0.7,
                segments=len(durations),
            )

        return ContentPattern(type=ContentType.UNKNOWN, confidence=0.3)

    def has_cartoon_collection_pattern(
        self,
        durations: list[int],
        disc_label: str,
    ) -> bool:
        """Detect cartoon collections like Looney Tunes."""

        # Label indicators
        cartoon_labels = [
            # Classic Warner Bros. cartoons
            "looney tunes",
            "merrie melodies",
            "bugs bunny",
            "daffy duck",
            "porky pig",
            "tweety",
            "sylvester",
            "pepe le pew",
            "foghorn leghorn",
            # MGM/Hanna-Barbera classics
            "tom and jerry",
            "tom & jerry",
            "droopy",
            # Disney classics
            "mickey mouse",
            "donald duck",
            "goofy",
            "pluto",
            "chip and dale",
            "chip 'n dale",
            # Other classic studios
            "betty boop",
            "popeye",
            "woody woodpecker",
            "casper",
            "felix the cat",
            # Collection indicators
            "cartoon",
            "cartoons",
            "animation collection",
            "classic cartoons",
            "golden age",
            "theatrical shorts",
            "animated shorts",
        ]

        label_indicates_cartoons = any(
            indicator in disc_label.lower() for indicator in cartoon_labels
        )

        # Duration patterns: classic theatrical cartoons are typically 3-15 minutes
        classic_shorts = [d for d in durations if 3 * 60 <= d <= 15 * 60]  # 3-15 min
        longer_cartoons = [
            d for d in durations if 15 * 60 < d <= 30 * 60
        ]  # 15-30 min (some specials)

        # Cartoon collection criteria (multiple patterns):

        # Pattern 1: Classic short cartoon collection (Tom & Jerry, Looney Tunes style)
        # - 70%+ titles are 3-15 minutes
        # - At least 4 titles
        if len(durations) >= 4 and len(classic_shorts) >= len(durations) * 0.7:
            return True

        # Pattern 2: Label-based detection with fewer titles
        # - Disc label suggests cartoons
        # - At least 3 short titles OR mix of short/medium cartoons
        if label_indicates_cartoons:
            total_cartoon_length = len(classic_shorts) + len(longer_cartoons)
            if len(classic_shorts) >= 3 or total_cartoon_length >= len(durations) * 0.6:
                return True

        # Pattern 3: Very consistent short durations (even without label hints)
        # - 80%+ are classic short length
        # - Low duration variance suggests intentional shorts collection
        if len(durations) >= 6 and len(classic_shorts) >= len(durations) * 0.8:
            # Check for low variance in the short titles (consistent cartoon length)
            if len(classic_shorts) > 1:
                import statistics

                short_variance = statistics.pvariance(classic_shorts)
                # Low variance suggests consistent cartoon episodes
                if short_variance < (2 * 60) ** 2:  # Less than 2 min variance
                    return True

        return False

    def has_consistent_episode_durations(self, durations: list[int]) -> bool:
        """Check if durations suggest consistent TV episodes."""

        if len(durations) < 3:
            return False

        # Filter out very short titles (< 10 minutes) that are likely extras/trailers
        episode_durations = [d for d in durations if d >= 10 * 60]

        if len(episode_durations) < 3:
            return False

        # Calculate standard deviation on filtered durations
        mean_duration = statistics.mean(episode_durations)
        std_dev = statistics.stdev(episode_durations)

        # Episodes should have low variation (within 15% of mean)
        coefficient_of_variation = std_dev / mean_duration

        return coefficient_of_variation < 0.15

    def has_single_long_title(self, durations: list[int]) -> bool:
        """Check if pattern suggests a movie (one long title + shorter extras)."""

        if not durations:
            return False

        # Sort by duration
        sorted_durations = sorted(durations, reverse=True)
        longest = sorted_durations[0]

        # Movie criteria:
        # - Longest title is at least 70 minutes
        # - Longest title is significantly longer than others

        if longest < 70 * 60:  # Less than 70 minutes
            return False

        if len(sorted_durations) == 1:
            return True

        # Check if longest is significantly longer than second longest
        second_longest = sorted_durations[1]
        ratio = longest / second_longest

        return ratio >= 2.0  # Main feature is at least 2x longer than extras

    def has_documentary_pattern(self, durations: list[int]) -> bool:
        """Check if pattern suggests documentary content."""

        # Documentary patterns are harder to detect
        # For now, use broad criteria

        if len(durations) < 2:
            return False

        # Multiple medium-length segments
        medium_segments = [d for d in durations if 20 * 60 <= d <= 90 * 60]

        return len(medium_segments) >= 2

    async def select_titles_intelligently(
        self,
        titles: list[Title],
        content_pattern: ContentPattern,
        disc_label: str,
    ) -> list[Title]:
        """Select appropriate titles based on detected content type."""

        content_type = content_pattern.type

        if content_type in [ContentType.CARTOON_COLLECTION, ContentType.CARTOON_SHORTS]:
            # Rip all cartoon shorts
            return [t for t in titles if 2 * 60 <= t.duration <= 20 * 60]

        if content_type in [
            ContentType.TV_SERIES,
            ContentType.CARTOON_SERIES,
            ContentType.ANIMATED_SERIES,
        ]:
            return self.select_tv_episode_titles(titles, content_pattern)

        if content_type in [ContentType.MOVIE, ContentType.ANIMATED_MOVIE]:
            return self.select_movie_titles(titles, content_pattern)

        if content_type == ContentType.DOCUMENTARY:
            return self.select_documentary_titles(titles, content_pattern)
        # Unknown - use intelligent heuristics
        return self.select_titles_by_heuristics(titles)

    def select_tv_episode_titles(
        self,
        titles: list[Title],
        content_pattern: ContentPattern,
    ) -> list[Title]:
        """Select all episode-length titles for TV series."""

        if content_pattern.episode_duration:
            episode_duration = content_pattern.episode_duration
            tolerance = 5 * 60  # 5 minute tolerance

            return [
                title
                for title in titles
                if abs(title.duration - episode_duration) <= tolerance
            ]

        # Fallback: select titles in reasonable episode range
        return [
            title
            for title in titles
            if 15 * 60 <= title.duration <= 90 * 60  # 15-90 minutes
        ]

    def select_movie_titles(
        self,
        titles: list[Title],
        content_pattern: ContentPattern,
    ) -> list[Title]:
        """Select main feature + any requested extras."""

        # Main feature: longest title that matches expected movie length
        main_candidates = [
            t for t in titles if t.duration >= 70 * 60  # Minimum movie length
        ]

        if not main_candidates:
            return []

        main_feature = max(main_candidates, key=lambda t: t.duration)
        selected_titles = [main_feature]

        # Include extras if configured
        if self.config.include_movie_extras:
            # Find potential extras: shorter titles that could be bonus content
            extra_candidates = [
                t
                for t in titles
                if (
                    t != main_feature
                    and t.duration >= self.config.max_extras_duration * 60
                    and t.duration < main_feature.duration * 0.8
                )  # Less than 80% of main movie
            ]

            # Sort extras by duration (longest first, likely more important)
            extra_candidates.sort(key=lambda t: t.duration, reverse=True)
            selected_titles.extend(extra_candidates)

            if extra_candidates:
                logger.info(f"Including {len(extra_candidates)} movie extras")

        return selected_titles

    def select_documentary_titles(
        self,
        titles: list[Title],
        content_pattern: ContentPattern,
    ) -> list[Title]:
        """Select documentary segments."""

        # Select medium-length segments (likely documentary parts)
        return [
            t for t in titles if 20 * 60 <= t.duration <= 120 * 60  # 20-120 minutes
        ]

    def select_titles_by_heuristics(self, titles: list[Title]) -> list[Title]:
        """Fallback selection using basic heuristics."""

        if not titles:
            return []

        # Remove very short titles (< 5 minutes)
        filtered = [t for t in titles if t.duration >= 5 * 60]

        if not filtered:
            return titles  # Return all if filtering removes everything

        # If we have one clearly dominant title, it's probably the main content
        sorted_titles = sorted(filtered, key=lambda t: t.duration, reverse=True)

        if len(sorted_titles) >= 2:
            longest = sorted_titles[0]
            second_longest = sorted_titles[1]

            # If longest is 3x longer than second longest, just return it
            if longest.duration >= 3 * second_longest.duration:
                return [longest]

        # Otherwise return all reasonable-length titles
        return filtered
