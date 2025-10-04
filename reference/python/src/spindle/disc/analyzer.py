"""Simplified disc content analysis used by the orchestrator."""

from __future__ import annotations

import logging
import re
import statistics
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any

from spindle.disc.metadata_extractor import (
    EnhancedDiscMetadata,
    EnhancedDiscMetadataExtractor,
)
from spindle.services.tmdb import TMDBService

if TYPE_CHECKING:
    from pathlib import Path

    from spindle.config import SpindleConfig
    from spindle.disc.monitor import DiscInfo
    from spindle.disc.ripper import Title
    from spindle.services.tmdb import MediaInfo

logger = logging.getLogger(__name__)


TV_CONFIDENCE_BASE = 0.55
MOVIE_CONFIDENCE_BASE = 0.60


@dataclass
class DiscAnalysisResult:
    """Result of the simplified disc analysis."""

    disc_info: DiscInfo
    primary_title: str
    content_type: str  # "movie" or "tv_series"
    confidence: float
    titles_to_rip: list[Title]
    commentary_tracks: dict[str, list[str]]
    episode_mappings: dict[str, dict[str, Any]]
    media_info: MediaInfo | None
    runtime_hint: int | None
    enhanced_metadata: EnhancedDiscMetadata | None = None


class IntelligentDiscAnalyzer:
    """Leaner analyzer focusing on MakeMKV scan data with optional metadata fallback."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb_service = TMDBService(config)
        self.metadata_extractor = EnhancedDiscMetadataExtractor(config)
        self.enable_enhanced_metadata = getattr(
            config,
            "enable_enhanced_disc_metadata",
            True,
        )

    def analyze_disc(
        self,
        disc_info: DiscInfo,
        titles: list[Title],
        *,
        disc_path: Path | None = None,
        makemkv_output: str | None = None,
    ) -> DiscAnalysisResult:
        if not titles:
            msg = "No titles discovered by MakeMKV"
            raise ValueError(msg)

        cleaned_label = self._clean_label(disc_info.label)
        main_title = self._select_main_title(titles)

        if main_title is None:
            msg = "Unable to determine main title"
            raise ValueError(msg)

        runtime_hint = int(main_title.duration / 60) if main_title.duration else None

        tv_candidates = self._find_tv_candidates(titles)
        is_tv = bool(tv_candidates)

        titles_to_rip: list[Title]
        confidence = MOVIE_CONFIDENCE_BASE
        if is_tv:
            confidence = TV_CONFIDENCE_BASE
            titles_to_rip = tv_candidates
        else:
            titles_to_rip = [main_title]
            if self.config.include_extras:
                extras = self._select_extras(titles, main_title)
                titles_to_rip.extend(extras)

        commentary = self._collect_commentary_tracks(titles_to_rip)

        enhanced_metadata: EnhancedDiscMetadata | None = None
        if (
            self.enable_enhanced_metadata
            and self._needs_enhanced_metadata(cleaned_label)
            and disc_path
        ):
            enhanced_metadata = self._run_enhanced_metadata(
                disc_path,
                disc_info,
                titles,
                makemkv_output,
            )
            metadata_title = self._metadata_title_candidate(enhanced_metadata)
            cleaned_label = metadata_title or cleaned_label
            if enhanced_metadata and enhanced_metadata.is_tv_series():
                is_tv = True
                if not tv_candidates:
                    titles_to_rip = self._fallback_tv_candidates(titles)
                confidence = max(confidence, TV_CONFIDENCE_BASE)

        season_hint = self._detect_season_hint(cleaned_label, enhanced_metadata)

        content_type = "tv" if is_tv else "movie"
        media_info = self.tmdb_service.identify_media(
            query=cleaned_label or main_title.name,
            content_type="tv" if is_tv else "movie",
            runtime_hint=runtime_hint,
            season_hint=season_hint,
        )

        if media_info:
            confidence = max(confidence, media_info.confidence)
            if media_info.is_tv_show:
                content_type = "tv"
                if media_info.season and not season_hint:
                    season_hint = media_info.season

        episode_mappings = {}
        if content_type == "tv":
            season_number = season_hint or (media_info.season if media_info else 1)
            episode_mappings = self._build_episode_mapping(
                titles_to_rip,
                media_info,
                season_number,
            )

        return DiscAnalysisResult(
            disc_info=disc_info,
            primary_title=cleaned_label or main_title.name,
            content_type="tv_series" if content_type == "tv" else "movie",
            confidence=min(confidence, 0.99),
            titles_to_rip=titles_to_rip,
            commentary_tracks=commentary,
            episode_mappings=episode_mappings,
            media_info=media_info,
            runtime_hint=runtime_hint,
            enhanced_metadata=enhanced_metadata,
        )

    def _collect_commentary_tracks(self, titles: list[Title]) -> dict[str, list[str]]:
        commentary_map: dict[str, list[str]] = {}
        if not self.config.include_commentary_tracks:
            return commentary_map

        for title in titles:
            track_ids = [track.track_id for track in title.get_commentary_tracks()]
            if track_ids:
                commentary_map[title.title_id] = track_ids
        return commentary_map

    def _select_main_title(self, titles: list[Title]) -> Title | None:
        sorted_titles = sorted(titles, key=lambda t: t.duration, reverse=True)
        return sorted_titles[0] if sorted_titles else None

    def _find_tv_candidates(self, titles: list[Title]) -> list[Title]:
        tv_min = self.config.tv_episode_min_duration * 60
        tv_max = self.config.tv_episode_max_duration * 60

        candidates = [t for t in titles if tv_min <= t.duration <= tv_max]

        if len(candidates) < 3:
            return []

        durations = [title.duration for title in candidates]
        median_duration = statistics.median(durations)
        clustered = [
            t for t in candidates if abs(t.duration - median_duration) <= 10 * 60
        ]

        if len(clustered) >= 3:
            return clustered

        return []

    def _fallback_tv_candidates(self, titles: list[Title]) -> list[Title]:
        tv_min = self.config.tv_episode_min_duration * 60
        tv_max = self.config.tv_episode_max_duration * 60
        return [t for t in titles if tv_min <= t.duration <= tv_max] or titles

    def _select_extras(self, titles: list[Title], main_title: Title) -> list[Title]:
        extras: list[Title] = []
        max_extras = self.config.max_extras_to_rip
        max_duration = self.config.max_extras_duration * 60

        for title in titles:
            if title is main_title:
                continue
            if title.duration <= max_duration and len(extras) < max_extras:
                extras.append(title)

        return extras

    def _needs_enhanced_metadata(self, cleaned_label: str) -> bool:
        return not cleaned_label or self._is_generic(cleaned_label)

    def _metadata_title_candidate(
        self,
        metadata: EnhancedDiscMetadata | None,
    ) -> str | None:
        if not metadata:
            return None
        candidates = metadata.get_best_title_candidates()
        return candidates[0] if candidates else None

    def _run_enhanced_metadata(
        self,
        disc_path: Path,
        disc_info: DiscInfo,
        titles: list[Title],
        makemkv_output: str | None,
    ) -> EnhancedDiscMetadata | None:
        metadata = self.metadata_extractor.extract_all_metadata(
            disc_path,
            disc_info.device,
        )

        if makemkv_output:
            metadata = self.metadata_extractor.populate_makemkv_data_from_output(
                metadata,
                makemkv_output,
                titles,
            )
        else:
            metadata = self.metadata_extractor.populate_makemkv_data(
                metadata,
                disc_info.label or "",
                titles,
            )

        return metadata

    def _detect_season_hint(
        self,
        label: str,
        metadata: EnhancedDiscMetadata | None,
    ) -> int | None:
        patterns = [
            r"season\s+(\d+)",
            r"s(\d{1,2})",
        ]
        working_label = label.lower() if label else ""
        for pattern in patterns:
            match = re.search(pattern, working_label)
            if match:
                try:
                    return int(match.group(1))
                except ValueError:
                    continue

        if metadata:
            season, _disc = metadata.get_season_disc_info()
            if season:
                return season

        return None

    def _build_episode_mapping(
        self,
        titles: list[Title],
        media_info: MediaInfo | None,
        season_number: int | None,
    ) -> dict[str, dict[str, Any]]:
        mapping: dict[str, dict[str, Any]] = {}
        season = season_number or 1

        available = []
        if media_info and media_info.episodes:
            available = list(media_info.episodes)

        for idx, title in enumerate(titles, start=1):
            episode_info = {
                "season_number": season,
                "episode_number": idx,
                "episode_title": None,
            }

            if available:
                best_match = self._match_episode_by_runtime(title, available)
                if best_match:
                    episode_info = {
                        "season_number": best_match.get("season_number", season),
                        "episode_number": best_match.get("episode_number", idx),
                        "episode_title": best_match.get("name"),
                    }
                    available.remove(best_match)

            mapping[title.title_id] = episode_info

        return mapping

    def _match_episode_by_runtime(
        self,
        title: Title,
        episodes: list[dict[str, Any]],
    ) -> dict[str, Any] | None:
        if not episodes:
            return None

        title_minutes = title.duration / 60 if title.duration else None

        if title_minutes is None:
            return episodes[0]

        best_match = None
        best_delta = None
        for episode in episodes:
            runtime = episode.get("runtime")
            if not runtime:
                continue
            delta = abs(runtime - title_minutes)
            if best_delta is None or delta < best_delta:
                best_delta = delta
                best_match = episode

        return best_match or episodes[0]

    def _clean_label(self, label: str | None) -> str:
        if not label:
            return ""

        cleaned = re.sub(
            r"\b(disc|dvd|bluray|blu-ray)\b",
            " ",
            label,
            flags=re.IGNORECASE,
        )
        cleaned = cleaned.replace("_", " ")
        cleaned = re.sub(r"\s+", " ", cleaned)
        return cleaned.strip()

    def _is_generic(self, label: str) -> bool:
        generic_patterns = [
            r"^\d+$",
            r"^unknown$",
            r"^untitled$",
            r"^bluray$",
            r"^dvd_video$",
            r"^logical_volume_id$",
        ]
        return any(
            re.match(pattern, label, flags=re.IGNORECASE)
            for pattern in generic_patterns
        )


__all__ = ["DiscAnalysisResult", "IntelligentDiscAnalyzer"]
