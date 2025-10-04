"""Disc processing coordination with streamlined analysis."""

from __future__ import annotations

import json
import logging
from pathlib import Path
from typing import TYPE_CHECKING

from spindle.disc.analyzer import IntelligentDiscAnalyzer
from spindle.disc.monitor import eject_disc
from spindle.disc.rip_spec import RipSpec
from spindle.error_handling import MediaError, ToolError
from spindle.services.makemkv import MakeMKVService
from spindle.storage.queue import QueueItem, QueueItemStatus

if TYPE_CHECKING:
    from spindle.config import SpindleConfig
    from spindle.disc.analyzer import DiscAnalysisResult
    from spindle.disc.monitor import DiscInfo

logger = logging.getLogger(__name__)


class DiscHandler:
    """Coordinates disc identification and ripping operations."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.disc_analyzer = IntelligentDiscAnalyzer(config)
        self.ripper = MakeMKVService(config)
        self.queue_manager = None  # Injected by orchestrator

    def identify_disc(
        self,
        item: QueueItem,
        disc_info: DiscInfo,
        *,
        scan_result: dict | None = None,
    ) -> None:
        """Identify disc content and prepare rip specification."""
        try:
            logger.info("Starting identification for queue item %s", item.item_id)

            item.status = QueueItemStatus.IDENTIFYING
            item.progress_stage = "Analyzing disc content"
            item.progress_percent = 0
            item.progress_message = None
            self.queue_manager.update_item(item)

            if scan_result is None:
                item.progress_message = "Scanning disc with MakeMKV"
                self.queue_manager.update_item(item)
                scan_result = self.ripper.scan_disc(disc_info.device)

            titles = scan_result.get("titles") or []
            fingerprint = scan_result.get("fingerprint")

            if not fingerprint:
                msg = "MakeMKV did not provide a disc fingerprint"
                raise MediaError(msg)

            if item.disc_fingerprint != fingerprint:
                item.disc_fingerprint = fingerprint
                self.queue_manager.update_item(item)

            item.progress_percent = 25
            item.progress_message = "Classifying disc contents"
            self.queue_manager.update_item(item)

            disc_path = self._find_mount_path(disc_info.device) or Path(
                disc_info.device,
            )

            analysis_result = self.disc_analyzer.analyze_disc(
                disc_info,
                titles,
                disc_path=disc_path,
                makemkv_output=scan_result.get("makemkv_output"),
            )

            self._handle_media_identification(
                item,
                disc_info,
                analysis_result,
                fingerprint,
            )

        except Exception as exc:  # pragma: no cover - defensive
            logger.exception("Error identifying disc: %s", exc)
            item.status = QueueItemStatus.FAILED
            item.error_message = str(exc)
            self.queue_manager.update_item(item)
            raise

    def _handle_media_identification(
        self,
        item: QueueItem,
        disc_info: DiscInfo,
        analysis_result: DiscAnalysisResult,
        fingerprint: str,
    ) -> None:
        """Persist analysis output to the queue item and rip specification."""

        media_info = analysis_result.media_info
        if not media_info:
            logger.warning(
                "TMDB did not return a match for %s",
                analysis_result.primary_title,
            )
            item.status = QueueItemStatus.REVIEW
            item.error_message = "Identification requires manual review"
            item.progress_percent = 100
            item.progress_stage = "Needs review"
            item.progress_message = "Could not match disc with TMDB"
            self.queue_manager.update_item(item)
            return

        title_payloads = []
        for title in analysis_result.titles_to_rip:
            title_payloads.append(
                {
                    "title_id": title.title_id,
                    "name": title.name,
                    "duration": title.duration,
                    "chapters": title.chapters,
                    "commentary_track_ids": analysis_result.commentary_tracks.get(
                        title.title_id,
                        [],
                    ),
                },
            )

        rip_spec_data = {
            "analysis_result": {
                "content_type": analysis_result.content_type,
                "confidence": analysis_result.confidence,
                "primary_title": analysis_result.primary_title,
                "runtime_hint": analysis_result.runtime_hint,
                "titles_to_rip": title_payloads,
                "episode_mappings": analysis_result.episode_mappings,
                "commentary_tracks": analysis_result.commentary_tracks,
            },
            "disc_info": {
                "label": disc_info.label,
                "device": disc_info.device,
                "disc_type": disc_info.disc_type,
                "fingerprint": fingerprint,
            },
            "media_info": media_info.to_dict(),
            "commentary_tracks": analysis_result.commentary_tracks,
            "is_multi_disc": False,
        }

        item.rip_spec_data = json.dumps(rip_spec_data)
        item.media_info = media_info
        item.status = QueueItemStatus.IDENTIFIED
        item.progress_stage = "Ready for ripping"
        item.progress_percent = 100
        item.progress_message = f"Identified as: {media_info.title}"

        self.queue_manager.update_item(item)
        logger.info(
            "Successfully identified disc %s as %s",
            item.item_id,
            media_info.title,
        )

    def rip_identified_item(self, item: QueueItem) -> None:
        """Rip an identified disc item."""
        try:
            if not item.rip_spec_data:
                msg = "No rip specification data found"
                raise MediaError(msg)

            logger.info("Starting rip for queue item %s", item.item_id)

            item.status = QueueItemStatus.RIPPING
            item.progress_stage = "Ripping disc"
            item.progress_percent = 0
            item.progress_message = None
            self.queue_manager.update_item(item)

            rip_spec = self._reconstruct_rip_spec_from_item(item)

            def progress_callback(stage: str, percent: int, message: str) -> None:
                item.progress_stage = stage
                item.progress_percent = percent
                item.progress_message = message
                self.queue_manager.update_item(item)

            ripped_files = self.ripper.rip_disc(
                rip_spec,
                progress_callback=progress_callback,
            )

            if not ripped_files:
                msg = "MakeMKV ripping failed - no files produced"
                raise ToolError(msg)

            primary_file = ripped_files[0]
            item.ripped_file = primary_file

            try:
                disc_info = json.loads(item.rip_spec_data)["disc_info"]
                eject_disc(disc_info.get("device", self.config.optical_drive))
            except Exception as exc:  # pragma: no cover - eject best effort
                logger.warning("Failed to eject disc: %s", exc)

            item.status = QueueItemStatus.RIPPED
            item.progress_stage = "Ripping completed"
            item.progress_percent = 100
            item.progress_message = f"Ripped {len(ripped_files)} file(s)"
            self.queue_manager.update_item(item)

        except Exception as exc:  # pragma: no cover - defensive
            logger.exception("Error ripping disc: %s", exc)
            item.status = QueueItemStatus.FAILED
            item.error_message = str(exc)
            self.queue_manager.update_item(item)
            raise

    def _reconstruct_rip_spec_from_item(self, item: QueueItem) -> RipSpec:
        """Rehydrate a RipSpec from queue item stored data."""
        data = json.loads(item.rip_spec_data)

        disc_info_payload = data["disc_info"]
        from spindle.disc.monitor import DiscInfo as StoredDiscInfo

        disc_info = StoredDiscInfo(
            device=disc_info_payload["device"],
            disc_type=disc_info_payload.get("disc_type", "unknown"),
            label=disc_info_payload.get("label"),
        )

        titles = []
        for raw in data["analysis_result"]["titles_to_rip"]:
            from spindle.disc.ripper import Title

            title_obj = Title(
                title_id=str(raw["title_id"]),
                duration=int(raw.get("duration", 0)),
                size=0,
                chapters=int(raw.get("chapters", 0)),
                tracks=[],
                name=raw.get("name"),
            )
            titles.append(title_obj)

        media_info = None
        if media_info_data := data.get("media_info"):
            from spindle.services.tmdb import MediaInfo as StoredMediaInfo

            media_info = StoredMediaInfo(
                title=media_info_data.get("title", "Unknown"),
                year=media_info_data.get("year", 0),
                media_type=media_info_data.get("media_type", "movie"),
                tmdb_id=media_info_data.get("tmdb_id", 0),
                overview=media_info_data.get("overview", ""),
                genres=media_info_data.get("genres", []),
                season=media_info_data.get("season"),
                seasons=media_info_data.get("seasons"),
                episode=media_info_data.get("episode"),
                episode_title=media_info_data.get("episode_title"),
                runtime=media_info_data.get("runtime"),
                confidence=media_info_data.get("confidence", 0.0),
            )

        return RipSpec(
            disc_info=disc_info,
            titles=titles,
            queue_item=item,
            analysis_result=data.get("analysis_result"),
            media_info=media_info,
            output_dir=self.config.staging_dir / "ripped",
            commentary_tracks=data.get("commentary_tracks", {}),
        )

    def _find_mount_path(self, device: str) -> Path | None:
        """Locate the mount path for an optical device if one exists."""
        try:
            with open("/proc/mounts", encoding="utf-8") as mounts_file:
                for line in mounts_file:
                    parts = line.split()
                    if len(parts) >= 2 and parts[0] == device:
                        return Path(parts[1])
        except OSError as exc:  # pragma: no cover - platform-specific
            logger.debug("Unable to inspect /proc/mounts: %s", exc)

        common_mounts = [
            Path("/media/cdrom"),
            Path("/media/cdrom0"),
            Path("/run/media") / Path(device).name,
        ]

        for mount in common_mounts:
            if mount.exists():
                return mount

        return None


__all__ = ["DiscHandler"]
