"""Disc ripping specification data structure for organizing rip parameters."""

import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from spindle.disc.metadata_extractor import EnhancedDiscMetadata
from spindle.disc.monitor import DiscInfo
from spindle.disc.ripper import Title
from spindle.services.tmdb import MediaInfo
from spindle.storage.queue import QueueItem


@dataclass
class RipSpec:
    """Complete specification for ripping a disc.

    Consolidates all parameters needed for disc processing workflow:
    - Disc metadata and titles
    - Analysis results and enhanced metadata
    - Output configuration and progress tracking
    - Queue item for status updates
    """

    # Core disc information
    disc_info: DiscInfo
    titles: list[Title]
    queue_item: QueueItem

    # Analysis results
    analysis_result: Any | None = None
    enhanced_metadata: EnhancedDiscMetadata | None = None
    makemkv_output: str | None = None

    # Media identification
    media_info: MediaInfo | None = None

    # Processing configuration
    disc_path: Path | None = None
    device: str | None = None
    output_dir: Path | None = None

    # Multi-disc handling
    is_multi_disc: bool = False
    disc_set_info: Any | None = None

    # Progress tracking
    start_time: float = field(default_factory=time.time)
    progress_callback: Any = None

    # Processing state
    selected_titles: list[Title] = field(default_factory=list)
    ripped_files: list[Path] = field(default_factory=list)

    @property
    def disc_label(self) -> str:
        """Get the disc label for display purposes."""
        return self.disc_info.label

    @property
    def has_analysis(self) -> bool:
        """Check if disc has been analyzed."""
        return self.analysis_result is not None

    @property
    def has_media_info(self) -> bool:
        """Check if media has been identified."""
        return self.media_info is not None

    @property
    def is_tv_series(self) -> bool:
        """Check if this is a TV series disc."""
        if self.enhanced_metadata:
            return self.enhanced_metadata.is_tv_series()
        if self.media_info:
            return self.media_info.media_type == "tv"
        return False

    @property
    def processing_duration(self) -> float:
        """Get processing duration in seconds."""
        return time.time() - self.start_time

    def get_title_candidates(self) -> list[str]:
        """Get title candidates for identification."""
        candidates = []

        # From enhanced metadata
        if self.enhanced_metadata:
            candidates.extend(self.enhanced_metadata.get_best_title_candidates())

        # From disc info
        if self.disc_info.label:
            candidates.append(self.disc_info.label)

        # From titles
        for title in self.titles:
            if title.name and title.name not in candidates:
                candidates.append(title.name)

        return candidates

    def select_title(self, title: Title) -> None:
        """Mark a title as selected for ripping."""
        if title not in self.selected_titles:
            self.selected_titles.append(title)

    def add_ripped_file(self, file_path: Path) -> None:
        """Add a successfully ripped file."""
        if file_path not in self.ripped_files:
            self.ripped_files.append(file_path)

    def update_progress(
        self,
        stage: str,
        percent: float = 0.0,
        message: str = "",
    ) -> None:
        """Update queue item progress."""
        self.queue_item.progress_stage = stage
        self.queue_item.progress_percent = percent
        self.queue_item.progress_message = message

        # Call progress callback if available
        if self.progress_callback:
            self.progress_callback(
                {
                    "stage": stage,
                    "percent": percent,
                    "message": message,
                    "duration": self.processing_duration,
                },
            )

    def __str__(self) -> str:
        """String representation for logging."""
        return f"RipSpec(disc={self.disc_label}, titles={len(self.titles)}, type={'TV' if self.is_tv_series else 'Movie'})"
