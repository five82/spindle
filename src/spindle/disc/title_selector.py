"""Intelligent title and track selection for disc ripping."""

import logging
from dataclasses import dataclass
from enum import Enum

from spindle.disc.ripper import Title, Track
from spindle.services.tmdb_impl import MediaInfo

logger = logging.getLogger(__name__)


class ContentType(Enum):
    """Content type classification."""

    MOVIE = "movie"
    TV_SERIES = "tv_series"
    UNKNOWN = "unknown"


@dataclass
class SelectionCriteria:
    """Criteria for title and track selection."""

    # Title selection
    include_extras: bool = False  # Whether to include extras/special features
    max_extras: int = 3
    min_extra_duration: int = 300  # 5 minutes
    max_extra_duration: int = 1800  # 30 minutes
    prefer_extended_versions: bool = True
    max_versions_to_rip: int = 2
    version_duration_tolerance: float = 0.40  # 40% duration variance

    # Track selection
    include_commentary: bool = True
    max_commentary_tracks: int = 2
    preferred_audio_codecs: list[str] | None = None
    include_subtitles: bool = False

    def __post_init__(self) -> None:
        if self.preferred_audio_codecs is None:
            self.preferred_audio_codecs = ["DTS-HD", "TrueHD", "AC3", "AAC"]


@dataclass
class RipSelection:
    """Selected titles and tracks for ripping."""

    main_titles: list[Title]
    extra_titles: list[Title]
    selected_tracks: dict[Title, list[Track]]  # Title -> selected tracks
    content_type: ContentType
    confidence: float


class IntelligentTitleSelector:
    """Intelligent selection of disc titles and tracks based on content analysis."""

    def __init__(self, criteria: SelectionCriteria | None = None):
        self.criteria = criteria or SelectionCriteria()
        self.logger = logging.getLogger(__name__)

    def select_content(
        self,
        titles: list[Title],
        media_info: MediaInfo | None = None,
        content_type: ContentType = ContentType.UNKNOWN,
    ) -> RipSelection:
        """Select titles and tracks based on content analysis."""

        if media_info and media_info.is_movie:
            content_type = ContentType.MOVIE
        elif media_info and media_info.is_tv_show:
            content_type = ContentType.TV_SERIES

        self.logger.info(
            f"Selecting content for {content_type.value} with {len(titles)} titles",
        )

        if content_type == ContentType.MOVIE:
            return self._select_movie_content(titles, media_info)
        if content_type == ContentType.TV_SERIES:
            return self._select_tv_content(titles, media_info)
        return self._select_unknown_content(titles)

    def _select_movie_content(
        self,
        titles: list[Title],
        media_info: MediaInfo | None,
    ) -> RipSelection:
        """Select movie titles and tracks."""
        # Find main feature (longest title > 20 minutes)
        movie_candidates = [t for t in titles if t.duration > 1200]  # > 20 minutes

        if not movie_candidates:
            self.logger.warning("No suitable movie titles found (all < 20 minutes)")
            return RipSelection(
                main_titles=[],
                extra_titles=[],
                selected_tracks={},
                content_type=ContentType.MOVIE,
                confidence=0.1,
            )

        # Sort by duration to find main feature
        movie_candidates.sort(key=lambda t: t.duration, reverse=True)

        # Detect multiple versions (theatrical, director's cut, extended)
        main_titles = self._detect_movie_versions(movie_candidates)

        # Select extras (special features) only if enabled
        extra_titles = []
        if self.criteria.include_extras:
            self.logger.info("include_extras is True, selecting extras")
            extra_titles = self._select_extras(
                titles,
                exclude_titles=main_titles,
                min_duration=self.criteria.min_extra_duration,
                max_duration=self.criteria.max_extra_duration,
            )
        else:
            self.logger.info("include_extras is False, skipping extras")

        self.logger.info(f"Selected {len(extra_titles)} extras from title selector")

        # Select tracks for each title
        self.logger.info("Track selection for selected titles:")
        selected_tracks = {}
        for title in main_titles + extra_titles:
            selected_tracks[title] = self._select_tracks_for_title(
                title,
                is_main_content=title in main_titles,
            )

        confidence = self._calculate_movie_confidence(main_titles, extra_titles)

        return RipSelection(
            main_titles=main_titles,
            extra_titles=extra_titles,
            selected_tracks=selected_tracks,
            content_type=ContentType.MOVIE,
            confidence=confidence,
        )

    def _detect_movie_versions(self, candidates: list[Title]) -> list[Title]:
        """Detect different versions of the same movie (theatrical, director's cut, extended)."""
        # First detect versions by title names and duration
        versions = self._classify_versions(candidates)

        # Apply user preference and selection logic
        selected = self._select_preferred_versions(versions)

        self.logger.info(
            f"Detected {len(versions)} versions, selected {len(selected)} for ripping",
        )
        for title in selected:
            version_type = self._get_version_type(title)
            self.logger.info(
                f"Selected: {title.name} ({version_type}, {title.duration//60}min)",
            )

        return selected

    def _classify_versions(self, candidates: list[Title]) -> dict[str, Title]:
        """Classify titles by version type based on name and duration."""
        versions: dict[str, Title] = {}

        # Sort by duration for analysis
        sorted_candidates = sorted(candidates, key=lambda t: t.duration, reverse=True)

        for title in sorted_candidates:
            version_type = self._get_version_type(title)

            # Only consider titles with similar durations (configurable variance)
            if versions:
                base_duration = next(iter(versions.values())).duration
                duration_ratio = abs(title.duration - base_duration) / base_duration
                if duration_ratio > self.criteria.version_duration_tolerance:
                    continue  # Too different, likely not the same movie

            # Store the version if not already found or if this one is better
            if version_type not in versions or self._is_better_version(
                title,
                versions[version_type],
                version_type,
            ):
                versions[version_type] = title

        return versions

    def _get_version_type(self, title: Title) -> str:
        """Determine version type from title name."""
        if not title.name:
            return "main"

        title_lower = title.name.lower()

        # Check for specific version indicators
        if any(
            indicator in title_lower
            for indicator in ["director", "director's", "directors"]
        ):
            return "directors_cut"
        if any(
            indicator in title_lower for indicator in ["extended", "uncut", "unrated"]
        ):
            return "extended"
        if any(indicator in title_lower for indicator in ["theatrical", "theater"]):
            return "theatrical"
        if any(
            indicator in title_lower
            for indicator in ["ultimate", "final", "definitive"]
        ):
            return "ultimate"
        if any(
            indicator in title_lower
            for indicator in ["special", "collector", "anniversary"]
        ):
            return "special"
        return "main"

    def _is_better_version(
        self,
        new_title: Title,
        existing_title: Title,
        version_type: str,
    ) -> bool:
        """Determine if new title is a better choice for this version type."""
        # For most versions, prefer longer duration
        if version_type in ["directors_cut", "extended", "ultimate", "special"]:
            return new_title.duration > existing_title.duration
        # For theatrical, prefer the one explicitly labeled as such
        if version_type == "theatrical":
            return "theatrical" in new_title.name.lower() if new_title.name else False
        # For main version, prefer longer unless explicitly theatrical
        return new_title.duration > existing_title.duration

    def _select_preferred_versions(self, versions: dict[str, Title]) -> list[Title]:
        """Select the single best version based on user preferences."""
        if not versions:
            return []

        # Get priority order based on user preferences
        version_priority = self._get_version_priority()

        # Find the highest priority version available
        for version_type in version_priority:
            if version_type in versions:
                self.logger.info(f"Selected single best version: {version_type}")
                return [versions[version_type]]

        # Fallback: if no preferred versions found, select longest title
        longest_title = max(versions.values(), key=lambda t: t.duration)
        self.logger.info("No preferred versions found, selecting longest title")
        return [longest_title]

    def _get_version_priority(self) -> list[str]:
        """Get version priority order based on user preferences."""
        if self.criteria.prefer_extended_versions:
            # Prefer extended content: Final/Ultimate > Director's > Extended > Special > Main > Theatrical
            return [
                "ultimate",
                "directors_cut",
                "extended",
                "special",
                "main",
                "theatrical",
            ]
        # Prefer original content: Theatrical > Main > others
        return [
            "theatrical",
            "main",
            "directors_cut",
            "extended",
            "ultimate",
            "special",
        ]

    def _select_extras(
        self,
        all_titles: list[Title],
        exclude_titles: list[Title],
        min_duration: int,
        max_duration: int,
    ) -> list[Title]:
        """Select extra features (behind-the-scenes, deleted scenes, etc.)."""
        extras = []

        for title in all_titles:
            if title in exclude_titles:
                continue

            # Filter by duration (typical special features are 5-30 minutes)
            if min_duration <= title.duration <= max_duration:
                extras.append(title)

        # Sort by duration (longer extras first, likely more important)
        extras.sort(key=lambda t: t.duration, reverse=True)

        # Limit number of extras
        return extras[: self.criteria.max_extras]

    def _select_tv_content(
        self,
        titles: list[Title],
        media_info: MediaInfo | None,
    ) -> RipSelection:
        """Select TV series titles and tracks."""
        # Group titles by similar duration (likely episodes)
        episode_groups = self._group_titles_by_duration(titles, tolerance_percent=0.15)

        # Select episode group based on expected count or duration
        main_titles = self._select_best_episode_group(episode_groups, media_info)

        # Look for extras (shorter or longer than episodes)
        extra_titles = []
        if main_titles:
            avg_episode_duration = sum(t.duration for t in main_titles) / len(
                main_titles,
            )
            for title in titles:
                if title not in main_titles:
                    # Include as extra if significantly different duration
                    duration_ratio = (
                        abs(title.duration - avg_episode_duration)
                        / avg_episode_duration
                    )
                    if duration_ratio > 0.5:  # 50% different
                        extra_titles.append(title)

        # Limit extras
        extra_titles = extra_titles[: self.criteria.max_extras]

        # Select tracks
        self.logger.info("Track selection for selected titles:")
        selected_tracks = {}
        for title in main_titles + extra_titles:
            selected_tracks[title] = self._select_tracks_for_title(
                title,
                is_main_content=title in main_titles,
            )

        confidence = self._calculate_tv_confidence(main_titles, extra_titles)

        return RipSelection(
            main_titles=main_titles,
            extra_titles=extra_titles,
            selected_tracks=selected_tracks,
            content_type=ContentType.TV_SERIES,
            confidence=confidence,
        )

    def _group_titles_by_duration(
        self,
        titles: list[Title],
        tolerance_percent: float = 0.15,
    ) -> list[list[Title]]:
        """Group titles by similar duration."""
        groups: list[list[Title]] = []

        for title in titles:
            # Find existing group with similar duration
            placed = False
            for group in groups:
                avg_duration = sum(t.duration for t in group) / len(group)
                tolerance = avg_duration * tolerance_percent

                if abs(title.duration - avg_duration) <= tolerance:
                    group.append(title)
                    placed = True
                    break

            if not placed:
                groups.append([title])

        return groups

    def _select_best_episode_group(
        self,
        groups: list[list[Title]],
        media_info: MediaInfo | None,
    ) -> list[Title]:
        """Select the best group of episodes."""
        if not groups:
            return []

        # If we have media info, try to match expected episode count/duration
        if media_info and hasattr(media_info, "expected_episodes"):
            for group in groups:
                if len(group) == media_info.expected_episodes:
                    return group

        # Fallback: use the largest group
        return max(groups, key=len)

    def _select_unknown_content(self, titles: list[Title]) -> RipSelection:
        """Select content when type is unknown."""
        # Conservative approach: select longest titles as main content
        main_candidates = sorted(titles, key=lambda t: t.duration, reverse=True)

        # Select up to 3 longest titles
        main_titles = main_candidates[:3]
        extra_titles: list[Title] = []

        # Select tracks
        self.logger.info("Track selection for selected titles:")
        selected_tracks = {}
        for title in main_titles:
            selected_tracks[title] = self._select_tracks_for_title(
                title,
                is_main_content=True,
            )

        return RipSelection(
            main_titles=main_titles,
            extra_titles=extra_titles,
            selected_tracks=selected_tracks,
            content_type=ContentType.UNKNOWN,
            confidence=0.5,
        )

    def _select_tracks_for_title(
        self,
        title: Title,
        is_main_content: bool = True,
    ) -> list[Track]:
        """Select appropriate tracks for a title."""
        selected = []

        # Always include all video tracks
        selected.extend(title.video_tracks)
        if title.video_tracks:
            self.logger.info(
                f"  Including {len(title.video_tracks)} video track(s) for {title.name}",
            )

        # Select audio tracks
        audio_tracks = self._select_audio_tracks(title, is_main_content)
        selected.extend(audio_tracks)

        # Select subtitle tracks (if enabled)
        if self.criteria.include_subtitles:
            subtitle_tracks = self._select_subtitle_tracks(title)
            selected.extend(subtitle_tracks)
            if subtitle_tracks:
                self.logger.info(
                    f"  Including {len(subtitle_tracks)} subtitle track(s) for {title.name}",
                )

        return selected

    def _select_audio_tracks(self, title: Title, is_main_content: bool) -> list[Track]:
        """Select audio tracks based on criteria."""
        selected = []
        all_audio = title.audio_tracks

        self.logger.info(
            f"  Analyzing {len(all_audio)} audio track(s) for {title.name}",
        )
        for track in all_audio:
            self.logger.info(
                f"    Available: {track.codec} {track.language} - {track.title or 'No title'}",
            )

        # Get main audio tracks (non-commentary)
        main_audio = title.get_main_audio_tracks()
        self.logger.info(
            f"  Found {len(main_audio)} main audio track(s) (non-commentary)",
        )

        if main_audio:
            # Select highest quality main audio
            best_main = self._get_highest_quality_audio(main_audio)
            if best_main:
                selected.append(best_main)
                self.logger.info(
                    f"  ✓ Selected main audio: {best_main.codec} {best_main.language} - {best_main.title or 'Primary audio'}",
                )

        # Include commentary tracks if enabled and this is main content
        if self.criteria.include_commentary and is_main_content:
            commentary_tracks = title.get_commentary_tracks()
            if commentary_tracks:
                # Limit commentary tracks
                selected_commentary = commentary_tracks[
                    : self.criteria.max_commentary_tracks
                ]
                selected.extend(selected_commentary)
                self.logger.info(
                    f"  ✓ Selected {len(selected_commentary)} commentary track(s)",
                )
                for track in selected_commentary:
                    self.logger.info(
                        f"    Commentary: {track.codec} {track.language} - {track.title or 'Commentary'}",
                    )
        elif self.criteria.include_commentary:
            self.logger.info("  Commentary tracks disabled for extras")

        if not selected:
            self.logger.warning(f"  ⚠ No audio tracks selected for {title.name}")

        return selected

    def _get_highest_quality_audio(self, audio_tracks: list[Track]) -> Track | None:
        """Get the highest quality audio track based on codec preference."""
        if not audio_tracks:
            return None

        # Score tracks based on codec preference
        scored_tracks = []
        for track in audio_tracks:
            score = self._score_audio_track(track)
            scored_tracks.append((score, track))

        # Sort by score (higher is better)
        scored_tracks.sort(key=lambda x: x[0], reverse=True)
        return scored_tracks[0][1]

    def _score_audio_track(self, track: Track) -> int:
        """Score an audio track based on codec and quality."""
        score = 0

        # Codec preference scoring
        if self.criteria.preferred_audio_codecs:
            for i, preferred_codec in enumerate(self.criteria.preferred_audio_codecs):
                if preferred_codec.lower() in track.codec.lower():
                    score += 100 - (i * 10)  # Higher score for more preferred codecs
                    break

        # Bonus for higher bitrates (if available)
        # This would need bitrate information from track metadata

        # Bonus for more channels (surround sound)
        # This would need channel information from track metadata

        return score

    def _calculate_movie_confidence(
        self,
        main_titles: list[Title],
        extra_titles: list[Title],
    ) -> float:
        """Calculate confidence score for movie selection."""
        if not main_titles:
            return 0.1

        confidence = 0.7  # Base confidence for having main titles

        # Bonus for reasonable number of titles
        total_titles = len(main_titles) + len(extra_titles)
        if 1 <= total_titles <= 10:
            confidence += 0.2

        # Bonus for main title duration (typical movie length)
        main_duration = main_titles[0].duration
        if 3600 <= main_duration <= 10800:  # 1-3 hours
            confidence += 0.1

        return min(confidence, 1.0)

    def _calculate_tv_confidence(
        self,
        main_titles: list[Title],
        extra_titles: list[Title],
    ) -> float:
        """Calculate confidence score for TV selection."""
        if not main_titles:
            return 0.1

        confidence = 0.6  # Base confidence

        # Bonus for multiple episodes
        if len(main_titles) > 1:
            confidence += 0.2

        # Bonus for consistent episode durations
        if len(main_titles) > 1:
            durations = [t.duration for t in main_titles]
            avg_duration = sum(durations) / len(durations)
            variance = sum((d - avg_duration) ** 2 for d in durations) / len(durations)
            if variance < (avg_duration * 0.1) ** 2:  # Low variance
                confidence += 0.1

        return min(confidence, 1.0)
