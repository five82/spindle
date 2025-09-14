"""Disc processing coordination."""

import json
import logging

from spindle.config import SpindleConfig
from spindle.disc.analyzer import IntelligentDiscAnalyzer
from spindle.disc.monitor import DiscInfo, eject_disc
from spindle.disc.multi_disc import SimpleMultiDiscManager
from spindle.disc.rip_spec import RipSpec
from spindle.disc.tv_analyzer import TVSeriesDiscAnalyzer
from spindle.error_handling import MediaError, ToolError
from spindle.services.makemkv import MakeMKVService
from spindle.services.tmdb import TMDBService
from spindle.storage.queue import QueueItem, QueueItemStatus

logger = logging.getLogger(__name__)


class DiscHandler:
    """Coordinates disc identification and ripping operations."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.disc_analyzer = IntelligentDiscAnalyzer(config)
        self.tv_analyzer = TVSeriesDiscAnalyzer(config)
        self.multi_disc_manager = SimpleMultiDiscManager(config)
        self.ripper = MakeMKVService(config)
        self.tmdb_service = TMDBService(config)
        self.queue_manager = None  # Will be injected by orchestrator

    async def identify_disc(self, item: QueueItem, disc_info: DiscInfo) -> None:
        """Identify disc content and prepare rip specification."""
        try:
            logger.info(f"Starting identification for: {item}")

            # Update status to identifying
            item.status = QueueItemStatus.IDENTIFYING
            item.progress_stage = "Analyzing disc content"
            item.progress_percent = 0
            self.queue_manager.update_item(item)

            # Analyze disc content
            logger.info("Analyzing disc content...")
            item.progress_percent = 20
            item.progress_message = "Scanning disc titles"
            self.queue_manager.update_item(item)

            analysis_result = await self.disc_analyzer.analyze_disc(disc_info.device)

            if not analysis_result:
                msg = "Failed to analyze disc content"
                raise MediaError(msg)

            logger.info(f"Analysis result: {analysis_result}")

            # Enhanced TV series detection
            item.progress_percent = 40
            item.progress_message = "Detecting content type"
            self.queue_manager.update_item(item)

            if analysis_result.content_type == "tv_series":
                logger.info("TV series detected, performing enhanced analysis")
                enhanced_result = await self.tv_analyzer.analyze_tv_disc(
                    disc_info.device,
                    analysis_result,
                )
                if enhanced_result:
                    analysis_result = enhanced_result

            # TMDB identification
            item.progress_percent = 60
            item.progress_message = "Identifying via TMDB"
            self.queue_manager.update_item(item)

            media_info = await self.tmdb_service.identify_media(
                analysis_result.primary_title,
                analysis_result.content_type,
                year=analysis_result.year,
            )

            if not media_info:
                logger.warning(
                    f"Could not identify disc: {analysis_result.primary_title}",
                )
                item.status = QueueItemStatus.REVIEW
                item.error_message = "Could not identify content via TMDB"
                self.queue_manager.update_item(item)
                return

            # Store analysis results
            item.progress_percent = 80
            item.progress_message = "Preparing rip specification"
            self.queue_manager.update_item(item)

            # Create rip specification
            rip_spec_data = {
                "analysis_result": {
                    "content_type": analysis_result.content_type,
                    "confidence": analysis_result.confidence,
                    "titles_to_rip": [
                        {
                            "index": title.index,
                            "name": title.name,
                            "duration": title.duration_seconds,
                            "chapters": title.chapter_count,
                        }
                        for title in analysis_result.titles_to_rip
                    ],
                    "episode_mappings": analysis_result.episode_mappings or {},
                },
                "disc_info": {
                    "label": disc_info.label,
                    "device": disc_info.device,
                    "disc_type": disc_info.disc_type,
                },
                "media_info": media_info.to_dict(),
                "is_multi_disc": False,  # Will be updated by multi-disc manager
            }

            # Multi-disc detection
            is_multi_disc = await self.multi_disc_manager.detect_multi_disc_series(
                media_info,
                analysis_result,
            )
            rip_spec_data["is_multi_disc"] = is_multi_disc

            # Store complete specification
            item.rip_spec_data = json.dumps(rip_spec_data)
            item.media_info = media_info
            item.status = QueueItemStatus.IDENTIFIED
            item.progress_stage = "Ready for ripping"
            item.progress_percent = 100
            item.progress_message = f"Identified as: {media_info.title}"

            self.queue_manager.update_item(item)
            logger.info(f"Successfully identified: {media_info.title}")

        except Exception as e:
            logger.exception(f"Error identifying disc: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            raise

    async def rip_identified_item(self, item: QueueItem) -> None:
        """Rip an identified disc item."""
        try:
            logger.info(f"Starting rip for: {item}")

            # Update status to ripping
            item.status = QueueItemStatus.RIPPING
            item.progress_stage = "Ripping disc"
            item.progress_percent = 0
            self.queue_manager.update_item(item)

            # Reconstruct rip specification
            if not item.rip_spec_data:
                msg = "No rip specification data found"
                raise MediaError(msg)

            rip_spec = self._reconstruct_rip_spec_from_item(item)

            # Progress callback for ripping
            def progress_callback(stage: str, percent: int, message: str) -> None:
                item.progress_stage = stage
                item.progress_percent = percent
                item.progress_message = message
                self.queue_manager.update_item(item)

            # Perform the rip
            logger.info("Starting MakeMKV rip...")
            ripped_files = await self.ripper.rip_disc(
                rip_spec,
                progress_callback=progress_callback,
            )

            if not ripped_files:
                msg = "MakeMKV ripping failed - no files produced"
                raise ToolError(msg)

            # Store ripped file paths
            if len(ripped_files) == 1:
                item.ripped_file = ripped_files[0]
            else:
                # For multi-file rips, store as JSON array
                item.ripped_file = ripped_files[0]  # Primary file
                # Could store additional files in metadata if needed

            # Eject disc after successful rip
            try:
                disc_info_data = json.loads(item.rip_spec_data)["disc_info"]
                eject_disc(disc_info_data["device"])
                logger.info("Disc ejected successfully")
            except Exception as e:
                logger.warning(f"Failed to eject disc: {e}")

            # Update to ripped status
            item.status = QueueItemStatus.RIPPED
            item.progress_stage = "Ripping completed"
            item.progress_percent = 100
            item.progress_message = f"Ripped {len(ripped_files)} file(s)"

            self.queue_manager.update_item(item)
            logger.info(f"Successfully ripped: {ripped_files}")

        except Exception as e:
            logger.exception(f"Error ripping disc: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            raise

    def _reconstruct_rip_spec_from_item(self, item: QueueItem) -> RipSpec:
        """Reconstruct RipSpec from stored queue item data."""
        spec_data = json.loads(item.rip_spec_data)

        # Create DiscInfo from stored data
        from spindle.disc.monitor import DiscInfo

        disc_info = DiscInfo(
            device=spec_data["disc_info"]["device"],
            disc_type="dvd",  # Default disc type
            label=spec_data["disc_info"].get("label", "Unknown"),
        )

        # Create Title objects from stored titles_to_rip data
        from spindle.disc.ripper import Title

        titles = []
        for title_data in spec_data["analysis_result"]["titles_to_rip"]:
            title = Title(
                title_id=str(title_data["index"]),
                duration=title_data.get("duration", 0),
                size=title_data.get("size", 0),
                chapters=title_data.get("chapters", 1),
                tracks=[],  # Empty tracks list for test
                name=title_data.get("name", f"Title {title_data['index']}"),
            )
            titles.append(title)

        return RipSpec(
            disc_info=disc_info,
            titles=titles,
            queue_item=item,
            device=spec_data["disc_info"]["device"],
            output_dir=self.config.staging_dir / "ripped",
            media_info=item.media_info,
            analysis_result=spec_data["analysis_result"],
        )

    def set_queue_manager(self, queue_manager):
        """Inject queue manager dependency."""
        self.queue_manager = queue_manager
