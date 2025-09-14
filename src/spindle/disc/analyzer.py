"""Intelligent disc content analysis and classification."""

import logging
import re
import statistics
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from spindle.config import SpindleConfig
from spindle.services.tmdb_impl import MediaIdentifier, TMDBClient

from .metadata_extractor import EnhancedDiscMetadataExtractor
from .monitor import DiscInfo
from .ripper import Title
from .title_selector import ContentType, IntelligentTitleSelector, SelectionCriteria

logger = logging.getLogger(__name__)


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
    enhanced_metadata: Any | None = None  # Enhanced metadata from bd_info/bdmt/mcmf


class IntelligentDiscAnalyzer:
    """Comprehensive disc analysis and content detection."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBClient(config)
        self.media_identifier = MediaIdentifier(config)
        # New enhanced metadata extractor
        self.metadata_extractor = EnhancedDiscMetadataExtractor(config)
        # Intelligent title selector
        selection_criteria = SelectionCriteria(
            include_extras=config.include_extras,
            max_extras=config.max_extras_to_rip,
            include_commentary=config.include_commentary_tracks,
            prefer_extended_versions=config.prefer_extended_versions,
            max_versions_to_rip=config.max_versions_to_rip,
            version_duration_tolerance=config.version_duration_tolerance,
            max_commentary_tracks=config.max_commentary_tracks,
            preferred_audio_codecs=config.preferred_audio_codecs,
            include_subtitles=config.include_subtitles,
        )
        self.title_selector = IntelligentTitleSelector(selection_criteria)
        # Cache for TMDB search results to avoid redundant API calls
        self._tmdb_search_cache: dict[str, dict[str, list[dict]]] = {}

    async def analyze_disc(
        self,
        disc_info: DiscInfo,
        titles: list[Title],
        disc_path: Path | None = None,
        makemkv_output: str | None = None,
    ) -> DiscAnalysisResult:
        """Complete disc analysis workflow with enhanced multi-source identification."""

        logger.info(f"Analyzing disc: {disc_info}")
        logger.info("Starting enhanced multi-tier content identification process...")

        # Phase 1: Enhanced metadata extraction from all sources
        enhanced_metadata = None
        if disc_path:
            logger.info("Phase 1: Extracting metadata from all available sources")
            enhanced_metadata = self.metadata_extractor.extract_all_metadata(
                disc_path,
                disc_info.device,
            )
            # Populate MakeMKV data with raw output for enhanced CINFO parsing
            if makemkv_output:
                enhanced_metadata = (
                    self.metadata_extractor.populate_makemkv_data_from_output(
                        enhanced_metadata,
                        makemkv_output,
                        titles,
                    )
                )
            else:
                # Fallback to basic method if raw output not available
                enhanced_metadata = self.metadata_extractor.populate_makemkv_data(
                    enhanced_metadata,
                    disc_info.label,
                    titles,
                )
            logger.info(f"Enhanced metadata extracted: {enhanced_metadata}")
        else:
            logger.info(
                "Phase 1 SKIPPED: Disc not mounted - using basic disc info only",
            )

        # Phase 2: Intelligent content identification using enhanced metadata
        identified_media = None
        title_candidates = []

        if enhanced_metadata:
            title_candidates = enhanced_metadata.get_best_title_candidates()
            logger.info(f"Title candidates from enhanced metadata: {title_candidates}")
        # Fallback to basic disc info
        elif not self.is_generic_disc_label(disc_info.label):
            title_candidates = [disc_info.label]

        # Phase 2: Determine content type for intelligent TMDB search
        logger.info("Phase 2: Determining content type from disc structure")
        content_type = ContentType.UNKNOWN

        # Try enhanced metadata first (volume ID patterns, etc.)
        if enhanced_metadata and enhanced_metadata.is_tv_series():
            content_type = ContentType.TV_SERIES
            logger.info("Content type determined from volume ID/metadata: TV series")
        else:
            # Use title pattern analysis (duration patterns, etc.)
            pattern_analysis = self.analyze_title_patterns(titles, disc_info.label)
            if pattern_analysis.type == ContentType.TV_SERIES:
                content_type = ContentType.TV_SERIES
                logger.info(
                    f"Content type determined from title patterns: TV series ({pattern_analysis.episode_count} episodes)",
                )
            elif pattern_analysis.type == ContentType.MOVIE:
                content_type = ContentType.MOVIE
                logger.info(
                    f"Content type determined from title patterns: Movie (main: {pattern_analysis.main_feature_duration//60}min)",
                )
            else:
                content_type = ContentType.MOVIE  # Default fallback
                logger.info("Content type undetermined, defaulting to movie")

        logger.info(f"Determined content type: {content_type.value}")

        # Phase 3: Targeted TMDB identification based on content type
        identified_media = None
        if title_candidates:
            logger.info(
                "Phase 3: Attempting intelligent content identification with targeted search",
            )
            runtime_minutes = None
            main_title = self.get_main_title(titles)
            if main_title:
                runtime_minutes = main_title.duration // 60

            identified_media = await self.media_identifier.identify_disc_content(
                title_candidates,
                runtime_minutes,
                content_type.value,  # Pass content type for targeted search
            )

            if identified_media:
                logger.info(
                    f"✓ Phase 3 SUCCESS: Identified as '{identified_media.title}' ({identified_media.year})",
                )
                # Update content type based on TMDB result if it was unknown
                if content_type == ContentType.UNKNOWN:
                    if identified_media.is_movie:
                        content_type = ContentType.MOVIE
                    elif identified_media.is_tv_show:
                        content_type = ContentType.TV_SERIES
            else:
                logger.info(
                    "✗ Phase 3 FAILED: No matches found via intelligent identification",
                )
        else:
            logger.info("Phase 3 SKIPPED: No usable title candidates found")

        # Phase 4: Intelligent title and track selection
        logger.info("Phase 4: Performing intelligent title and track selection")
        selection = self.title_selector.select_content(
            titles,
            identified_media,
            content_type,
        )

        logger.info(
            f"Selected {len(selection.main_titles)} main titles, "
            f"{len(selection.extra_titles)} extras (confidence: {selection.confidence:.2f})",
        )

        # Convert selection to title list
        selected_titles = selection.main_titles + selection.extra_titles

        return DiscAnalysisResult(
            disc_info=disc_info,
            content_type=content_type,
            confidence=selection.confidence,
            titles_to_rip=selected_titles,
            metadata=identified_media,
            episode_mappings=None,
            enhanced_metadata=enhanced_metadata,  # Include enhanced metadata from bd_info
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
        """Query TMDB for content identification, using cache if available."""
        try:
            cache_key = self.clean_disc_label(label)
            cached_results = self._tmdb_search_cache.get(cache_key)

            if cached_results:
                logger.info(
                    f"Using cached TMDB results for '{label}' (avoiding redundant API calls)",
                )
                movie_results = cached_results.get("movie", [])
                tv_results = cached_results.get("tv", [])
            else:
                # Make fresh API calls
                logger.debug(f"Making fresh TMDB API calls for '{label}'")
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
                    type=ContentType.MOVIE,
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
                            type=ContentType.TV_SERIES,
                            confidence=0.9,
                            episode_duration=int(avg_runtime * 60),
                        )
                    if avg_runtime <= 25:
                        return ContentPattern(
                            type=ContentType.TV_SERIES,
                            confidence=0.9,
                            episode_duration=int(avg_runtime * 60),
                        )
                    return ContentPattern(
                        type=ContentType.TV_SERIES,
                        confidence=0.9,
                        episode_duration=int(avg_runtime * 60),
                    )

                return ContentPattern(
                    type=ContentType.TV_SERIES,
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

        # TV Show patterns
        if self.has_consistent_episode_durations(durations):
            # Filter to episode-length titles for analysis
            episode_durations = [d for d in durations if d >= 10 * 60]

            # Standard TV episodes
            if episode_durations:
                median_duration = int(statistics.median(episode_durations))
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

        return ContentPattern(type=ContentType.UNKNOWN, confidence=0.3)

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

        # Main feature should be at least 2.5x longer than extras for clear movie pattern
        return ratio >= 2.5

    async def select_titles_intelligently(
        self,
        titles: list[Title],
        content_pattern: ContentPattern,
        disc_label: str,
    ) -> list[Title]:
        """Select appropriate titles based on detected content type."""

        content_type = content_pattern.type

        if content_type == ContentType.TV_SERIES:
            return self.select_tv_episode_titles(titles, content_pattern)

        if content_type == ContentType.MOVIE:
            return self.select_movie_titles(titles, content_pattern)

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

        # Fallback: select titles in reasonable episode range (includes cartoon shorts)
        return [
            title
            for title in titles
            if 2 * 60
            <= title.duration
            <= 90 * 60  # 2-90 minutes (includes cartoon shorts)
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
        if self.config.include_extras:
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

    async def try_runtime_verification(
        self,
        disc_label: str,
        main_title_duration: int,
    ) -> tuple[Any | None, dict[str, list[dict]] | None]:
        """Try to identify content using disc label and runtime verification."""
        try:
            return await self.media_identifier.identify_with_runtime_verification(
                disc_title=disc_label,
                main_title_runtime_seconds=main_title_duration,
            )
        except Exception as e:
            logger.warning(f"Runtime verification failed: {e}")
            return None, None

    def get_main_title(self, titles: list[Title]) -> Title | None:
        """Get the main title (longest duration) from the titles list."""
        if not titles:
            return None
        return max(titles, key=lambda t: t.duration)

    def is_generic_disc_label(self, label: str) -> bool:
        """Check if disc label is too generic for meaningful TMDB search."""
        if not label:
            return True

        label_lower = label.lower().strip()

        # Common generic disc labels that won't match anything in TMDB
        generic_labels = [
            "logical_volume_id",
            "untitled",
            "no_name",
            "unnamed",
            "disc",
            "disk",
            "dvd",
            "bluray",
            "blu-ray",
            "bd",
            "volume",
            "data",
            "audio_ts",
            "video_ts",
            "bdmv",
            "dvd_video",
            "movie",
            "film",
            "title",
            "content",
            "media",
            "unknown",
            "",
        ]

        # Check exact matches
        if label_lower in generic_labels:
            return True

        # Check patterns that are clearly generic
        # Single letters or numbers
        if len(label_lower) <= 2:
            return True

        # All digits
        if label_lower.isdigit():
            return True

        # Common timestamp/ID patterns
        if re.match(r"^(disc|disk|volume)\d+$", label_lower):
            return True

        # Date patterns (YYYY-MM-DD, YYYYMMDD, etc.)
        return bool(re.match(r"^\d{4}[-_]?\d{2}[-_]?\d{2}$", label_lower))

    def media_info_to_content_pattern(self, media_info: Any) -> ContentPattern:
        """Convert MediaInfo to ContentPattern."""
        # Check if this is a MediaInfo object (from tmdb.py)
        if hasattr(media_info, "is_movie") and hasattr(media_info, "is_tv_show"):
            if media_info.is_movie:
                return ContentPattern(type=ContentType.MOVIE, confidence=0.95)
            if media_info.is_tv_show:
                return ContentPattern(type=ContentType.TV_SERIES, confidence=0.95)

        # Fallback for unknown media info types
        return ContentPattern(type=ContentType.UNKNOWN, confidence=0.3)
