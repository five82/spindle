"""Specialized TV series disc analysis and episode mapping."""

import logging
import re
from typing import Any, cast

from spindle.config import SpindleConfig
from spindle.services.tmdb import TMDBClient

from .analyzer import EpisodeInfo, SeriesInfo
from .ripper import Title

logger = logging.getLogger(__name__)


class TVSeriesDiscAnalyzer:
    """Specialized analyzer for TV series discs."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBClient(config)

    def analyze_tv_disc(
        self,
        disc_label: str,
        titles: list[Title],
    ) -> dict[Title, EpisodeInfo] | None:
        """Identify TV series from disc and map episodes."""

        logger.info(f"Analyzing TV disc: {disc_label}")

        # Step 1: Identify the series from disc label
        series_info = self.identify_series_from_disc(disc_label)
        if not series_info:
            logger.warning(
                f"Could not identify TV series from disc label: {disc_label}",
            )
            return None

        logger.info(
            f"Identified series: {series_info.name} (TMDB ID: {series_info.tmdb_id})",
        )

        # Step 2: Determine season from disc label or series info
        season_number = series_info.detected_season or 1
        logger.info(f"Detected season: {season_number}")

        # Step 3: Map disc titles to specific episodes
        return self.map_titles_to_episodes(
            titles,
            series_info,
            season_number,
        )

    def identify_series_from_disc(self, disc_label: str) -> SeriesInfo | None:
        """Extract series name from disc label."""

        # Extract series name from common disc label patterns
        patterns = [
            r"(.+?)\s+Season\s+(\d+)",  # "Breaking Bad Season 1"
            r"(.+?)\s+S(\d+)",  # "Friends S01"
            r"(.+?)\s+Series\s+(\d+)",  # "Doctor Who Series 1"
            r"(.+?)\s+\(Season\s+(\d+)\)",  # "Lost (Season 1)"
            r"(.+?)\s+Complete\s+Season\s+(\d+)",  # "The Office Complete Season 1"
            r"(.+?)\s+(\d+)\s+Season",  # "Friends 1 Season"
            r"(.+?)\s+Season(\d+)",  # "BreakingBadSeason1"
        ]

        for pattern in patterns:
            match = re.search(pattern, disc_label, re.IGNORECASE)
            if match:
                series_name = match.group(1).strip()
                season_num = int(match.group(2))

                # Clean up series name
                series_name = self.clean_series_name(series_name)

                # Validate series with TMDB
                tmdb_series = self.tmdb.search_tv(series_name)
                if tmdb_series:
                    series_data = tmdb_series[0]
                    return SeriesInfo(
                        name=series_data.get("name", series_name),
                        tmdb_id=series_data["id"],
                        detected_season=season_num,
                        year=self.extract_year_from_date(
                            series_data.get("first_air_date"),
                        ),
                    )

        # Fallback: try entire disc label as series name
        cleaned_label = self.clean_series_name(disc_label)
        tmdb_results = self.tmdb.search_tv(cleaned_label)
        if tmdb_results:
            series_data = tmdb_results[0]
            return SeriesInfo(
                name=series_data.get("name", cleaned_label),
                tmdb_id=series_data["id"],
                detected_season=None,
                year=self.extract_year_from_date(series_data.get("first_air_date")),
            )

        return None

    def clean_series_name(self, name: str) -> str:
        """Clean up series name for API queries."""

        # Remove common disc/set indicators
        cleaned = re.sub(
            r"\b(complete|collection|box\s*set|dvd|blu-?ray|disc)\b",
            "",
            name,
            flags=re.IGNORECASE,
        )

        # Remove special characters and normalize spaces
        cleaned = re.sub(r"[._-]", " ", cleaned)
        return re.sub(r"\s+", " ", cleaned).strip()

    def extract_year_from_date(self, date_str: str | None) -> int | None:
        """Extract year from TMDB date string."""
        if date_str and len(date_str) >= 4:
            try:
                return int(date_str[:4])
            except ValueError as e:
                logger.debug(f"Failed to extract year from date '{date_str}': {e}")
        return None

    def map_titles_to_episodes(
        self,
        titles: list[Title],
        series_info: SeriesInfo,
        season_num: int,
    ) -> dict[Title, EpisodeInfo]:
        """Map MakeMKV titles to specific episodes using TMDB data."""

        logger.info(
            f"Mapping {len(titles)} titles to episodes for {series_info.name} Season {season_num}",
        )

        # Get complete season data from TMDB
        season_data = self.get_tv_season_details(series_info.tmdb_id, season_num)
        if not season_data:
            logger.warning(f"Could not get season {season_num} data from TMDB")
            return {}

        episodes = season_data.get("episodes", [])
        logger.info(f"Found {len(episodes)} episodes in TMDB season data")

        # Filter titles to likely episodes (skip extras, commentaries)
        episode_titles = self.filter_episode_titles(titles)
        logger.info(f"Filtered to {len(episode_titles)} likely episode titles")

        # Use configured mapping strategy
        strategy = self.config.episode_mapping_strategy.lower()

        if strategy == "duration":
            mapping = self.map_by_duration(episode_titles, episodes)
        elif strategy == "sequential":
            mapping = self.map_sequentially(episode_titles, episodes)
        elif strategy == "hybrid":
            mapping = self.map_hybrid(episode_titles, episodes)
        else:
            # Default to hybrid if strategy is unknown
            logger.warning(f"Unknown mapping strategy '{strategy}', using hybrid")
            mapping = self.map_hybrid(episode_titles, episodes)

        logger.info(f"Successfully mapped {len(mapping)} titles to episodes")
        return mapping

    def get_tv_season_details(
        self,
        tv_id: int,
        season_number: int,
    ) -> dict | None:
        """Get TV season details from TMDB."""
        try:
            params = {
                "api_key": self.tmdb.api_key,
                "language": self.tmdb.language,
            }

            response = self.tmdb.client.get(
                f"{self.tmdb.base_url}/tv/{tv_id}/season/{season_number}",
                params=params,
            )
            response.raise_for_status()
            data = response.json()
            return cast("dict[Any, Any]", data)
        except Exception as e:
            logger.exception(
                f"Failed to get season {season_number} for TV ID {tv_id}: {e}",
            )
            return None

    def map_by_duration(
        self,
        titles: list[Title],
        episodes: list[dict],
    ) -> dict[Title, EpisodeInfo]:
        """Map titles to episodes by matching durations."""

        mapping = {}
        used_episodes = set()

        for title in titles:
            best_match = None
            best_diff = float("inf")

            for i, episode in enumerate(episodes):
                if i in used_episodes:
                    continue

                episode_runtime = episode.get("runtime")
                if not episode_runtime:
                    continue

                # Convert runtime to seconds and compare
                episode_duration = episode_runtime * 60
                duration_diff = abs(title.duration - episode_duration)

                # Use flexible tolerance: 10% but at least 1 minute, max 5 minutes
                base_tolerance = episode_duration * 0.1
                tolerance = max(60, min(300, base_tolerance))  # 1-5 minute range

                if duration_diff <= tolerance and duration_diff < best_diff:
                    best_match = (i, episode)
                    best_diff = duration_diff

            if best_match:
                episode_index, episode_data = best_match
                used_episodes.add(episode_index)

                mapping[title] = EpisodeInfo(
                    season_number=episode_data.get("season_number", 1),
                    episode_number=episode_data.get(
                        "episode_number",
                        episode_index + 1,
                    ),
                    episode_title=episode_data.get("name", ""),
                    air_date=episode_data.get("air_date"),
                    overview=episode_data.get("overview", ""),
                    runtime=episode_data.get("runtime"),
                )

        return mapping

    def map_hybrid(
        self,
        titles: list[Title],
        episodes: list[dict],
    ) -> dict[Title, EpisodeInfo]:
        """Hybrid mapping: try duration matching first, fall back to sequential."""

        # First try duration matching
        duration_mapping = self.map_by_duration(titles, episodes)

        # Check if duration mapping was successful enough (80% of titles mapped)
        success_rate = len(duration_mapping) / len(titles) if titles else 0

        if success_rate >= 0.8:
            logger.info(
                f"Duration mapping successful ({success_rate:.1%}), using duration results",
            )
            return duration_mapping

        # If duration mapping wasn't good enough, try sequential mapping
        logger.info(
            f"Duration mapping only mapped {success_rate:.1%} of titles, falling back to sequential",
        )
        sequential_mapping = self.map_sequentially(titles, episodes)

        # If sequential mapping is better, use it
        if len(sequential_mapping) > len(duration_mapping):
            return sequential_mapping

        # Otherwise, combine both strategies for best results
        logger.info("Combining duration and sequential mapping results")
        combined_mapping = {}

        # Start with duration mapping results (more accurate when it works)
        combined_mapping.update(duration_mapping)

        # Fill gaps with sequential mapping for unmapped titles
        unmapped_titles = [t for t in titles if t not in combined_mapping]
        unused_episodes = [
            ep
            for i, ep in enumerate(episodes)
            if i
            not in [
                self._find_episode_index(ep, episodes)
                for ep in duration_mapping.values()
            ]
        ]

        if unmapped_titles and unused_episodes:
            gap_mapping = self.map_sequentially(unmapped_titles, unused_episodes)
            combined_mapping.update(gap_mapping)

        return combined_mapping

    def _find_episode_index(
        self,
        target_episode_info: EpisodeInfo,
        episodes: list[dict],
    ) -> int:
        """Find the index of an episode in the episodes list."""
        for i, ep in enumerate(episodes):
            if (
                ep.get("episode_number") == target_episode_info.episode_number
                and ep.get("season_number") == target_episode_info.season_number
            ):
                return i
        return -1

    def map_sequentially(
        self,
        titles: list[Title],
        episodes: list[dict],
    ) -> dict[Title, EpisodeInfo]:
        """Map titles to episodes in sequential order."""

        # Sort titles by title ID to get disc order
        sorted_titles = sorted(
            titles,
            key=lambda t: int(t.title_id) if t.title_id.isdigit() else 999,
        )

        # Take the first N titles matching episode count
        episode_count = len(episodes)
        main_titles = sorted_titles[:episode_count]

        mapping = {}
        for i, (title, episode) in enumerate(zip(main_titles, episodes, strict=False)):
            mapping[title] = EpisodeInfo(
                season_number=episode.get("season_number", 1),
                episode_number=episode.get("episode_number", i + 1),
                episode_title=episode.get("name", f"Episode {i + 1}"),
                air_date=episode.get("air_date"),
                overview=episode.get("overview", ""),
                runtime=episode.get("runtime"),
            )

        return mapping

    def filter_episode_titles(self, titles: list[Title]) -> list[Title]:
        """Filter out non-episode content (extras, previews, etc.)."""

        # Remove very short titles (< 15 minutes)
        filtered = [t for t in titles if t.duration >= 15 * 60]

        # Remove titles with "extra" indicators in name
        extra_indicators = [
            "bonus",
            "deleted",
            "preview",
            "trailer",
            "making",
            "behind",
        ]
        filtered = [
            t
            for t in filtered
            if not any(indicator in t.name.lower() for indicator in extra_indicators)
        ]

        # Group by similar durations (episodes should be similar length)
        duration_groups = self.group_by_duration(filtered)

        # Return the largest group (likely main episodes)
        if duration_groups:
            largest_group = max(duration_groups, key=len)
            # Only use this if it's significantly larger than other groups
            if len(largest_group) >= len(filtered) * 0.6:
                return largest_group

        return filtered

    def group_by_duration(self, titles: list[Title]) -> list[list[Title]]:
        """Group titles by similar durations."""

        if not titles:
            return []

        # Sort by duration
        sorted_titles = sorted(titles, key=lambda t: t.duration)

        groups = []
        current_group = [sorted_titles[0]]

        for title in sorted_titles[1:]:
            # If duration is within 15% of the current group's average, add to group
            current_avg = sum(t.duration for t in current_group) / len(current_group)

            if abs(title.duration - current_avg) <= current_avg * 0.15:
                current_group.append(title)
            else:
                # Start a new group
                groups.append(current_group)
                current_group = [title]

        # Add the last group
        groups.append(current_group)

        return groups
