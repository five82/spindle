"""Continuous queue processor for automated workflow."""

import asyncio
import logging
import time
from typing import TYPE_CHECKING

from .config import SpindleConfig

if TYPE_CHECKING:
    from .disc.analyzer import ContentPattern
from .disc.analyzer import IntelligentDiscAnalyzer
from .disc.monitor import DiscInfo, DiscMonitor, detect_disc, eject_disc
from .disc.ripper import MakeMKVRipper
from .disc.tv_analyzer import TVSeriesDiscAnalyzer
from .encode.drapto_wrapper import DraptoEncoder
from .identify.tmdb import MediaIdentifier
from .notify.ntfy import NtfyNotifier
from .organize.library import LibraryOrganizer
from .queue.manager import QueueItem, QueueItemStatus, QueueManager

logger = logging.getLogger(__name__)


class ContinuousProcessor:
    """Manages continuous processing of discs and queue items."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.queue_manager = QueueManager(config)
        self.ripper = MakeMKVRipper(config)
        self.identifier = MediaIdentifier(config)
        self.encoder = DraptoEncoder(config)
        self.organizer = LibraryOrganizer(config)
        self.notifier = NtfyNotifier(config)

        # Enhanced analysis components
        self.disc_analyzer = IntelligentDiscAnalyzer(config)
        self.tv_analyzer = TVSeriesDiscAnalyzer(config)

        self.disc_monitor: DiscMonitor | None = None
        self.processing_task: asyncio.Task | None = None
        self.is_running = False

    def start(self) -> None:
        """Start the continuous processor."""
        if self.is_running:
            logger.warning("Processor is already running")
            return

        logger.info("Starting continuous processor")
        self.is_running = True

        # Start disc monitoring
        self.disc_monitor = DiscMonitor(
            device=self.config.optical_drive,
            callback=self._on_disc_detected,
        )
        self.disc_monitor.start_monitoring()

        # Start background queue processing
        loop = asyncio.get_event_loop()
        self.processing_task = loop.create_task(self._process_queue_continuously())

        logger.info("Continuous processor started - insert discs to begin")

    def stop(self) -> None:
        """Stop the continuous processor."""
        if not self.is_running:
            return

        logger.info("Stopping continuous processor")
        self.is_running = False

        # Stop disc monitoring
        if self.disc_monitor:
            self.disc_monitor.stop_monitoring()

        # Cancel background processing
        if self.processing_task:
            self.processing_task.cancel()

        logger.info("Continuous processor stopped")

    def _on_disc_detected(self, disc_info: DiscInfo) -> None:
        """Handle disc detection by automatically ripping."""
        logger.info(f"Detected disc: {disc_info}")
        self.notifier.notify_disc_detected(disc_info.label, disc_info.disc_type)

        try:
            # Add to queue
            item = self.queue_manager.add_disc(disc_info.label)
            logger.info(f"Added to queue: {item}")

            # Start ripping immediately
            self._rip_disc(item, disc_info)

        except Exception as e:
            logger.exception(f"Error handling disc detection: {e}")
            self.notifier.notify_error(
                f"Failed to process disc: {e}",
                context=disc_info.label,
            )

    def _rip_disc(self, item: QueueItem, disc_info: DiscInfo) -> None:
        """Rip the detected disc using intelligent analysis."""
        try:
            logger.info(f"Starting intelligent analysis and rip: {disc_info.label}")
            self.notifier.notify_rip_started(disc_info.label)

            # Update status
            item.status = QueueItemStatus.RIPPING
            self.queue_manager.update_item(item)

            start_time = time.time()

            # First, scan the disc to get title information
            titles = self.ripper.scan_disc()
            logger.info(f"Found {len(titles)} titles on disc")

            # Analyze disc content to determine type and strategy
            # TODO: The disc analyzer should have both sync and async versions
            # For now, use a basic fallback pattern until this is properly designed
            from .disc.analyzer import ContentPattern, ContentType

            content_pattern = ContentPattern(type=ContentType.MOVIE, confidence=0.95)
            logger.info(
                f"Detected content type: {content_pattern.type} (confidence: {content_pattern.confidence:.2f})",
            )

            # Update progress with analysis results
            item.progress_stage = f"Detected: {content_pattern.type.value}"
            item.progress_percent = 10
            self.queue_manager.update_item(item)

            # Handle different content types
            output_files = self._handle_content_type(disc_info, titles, content_pattern)

            # Store primary output file for queue tracking
            output_file = output_files[0] if output_files else None

            # Update item with ripped file
            item.ripped_file = output_file
            item.status = QueueItemStatus.RIPPED
            self.queue_manager.update_item(item)

            # Calculate duration
            duration = time.strftime("%H:%M:%S", time.gmtime(time.time() - start_time))

            logger.info(f"Rip completed: {output_file}")
            self.notifier.notify_rip_completed(disc_info.label, duration)

            # Eject disc
            eject_disc(self.config.optical_drive)
            logger.info("Disc ejected - ready for next disc")

        except Exception as e:
            logger.exception(f"Error ripping disc: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            self.notifier.notify_error(f"Ripping failed: {e}", context=disc_info.label)

    def _handle_content_type(
        self,
        disc_info: DiscInfo,
        titles: list,
        content_pattern: "ContentPattern",
    ) -> list:
        """Handle different content types with appropriate strategies."""
        from .disc.analyzer import ContentType

        content_type = content_pattern.type

        if content_type in [ContentType.TV_SERIES, ContentType.ANIMATED_SERIES]:
            # Handle TV series with episode mapping
            return self._handle_tv_series(disc_info, titles)

        if content_type in [ContentType.CARTOON_COLLECTION, ContentType.CARTOON_SHORTS]:
            # Handle cartoon collections - rip all shorts
            return self._handle_cartoon_collection(disc_info, titles, content_pattern)

        if content_type in [ContentType.MOVIE, ContentType.ANIMATED_MOVIE]:
            # Handle movies - select main title and optionally extras
            return self._handle_movie(disc_info, titles, content_pattern)

        # Unknown content type - use basic strategy
        logger.warning(f"Unknown content type {content_type}, using basic rip strategy")
        return self._handle_basic_rip(disc_info, titles)

    def _handle_tv_series(self, disc_info: DiscInfo, titles: list) -> list:
        """Handle TV series using intelligent episode mapping."""
        try:
            # Use TV analyzer for episode mapping
            episode_mapping = asyncio.run(
                self.tv_analyzer.analyze_tv_disc(disc_info.label, titles),
            )

            output_files = []
            if episode_mapping:
                logger.info(f"Mapped {len(episode_mapping)} episodes")

                for i, (title, episode_info) in enumerate(episode_mapping.items()):
                    20 + int((i / len(episode_mapping)) * 60)  # 20-80% for ripping

                    # Generate filename for episode
                    episode_filename = f"S{episode_info.season_number:02d}E{episode_info.episode_number:02d}"
                    if episode_info.episode_title:
                        safe_title = episode_info.episode_title.replace(
                            " ",
                            "_",
                        ).replace("/", "_")
                        episode_filename += f" - {safe_title}"

                    output_file = self.ripper.rip_title(
                        title,
                        self.config.staging_dir / "episodes",
                    )
                    output_files.append(output_file)

            else:
                logger.warning("No episode mapping found, using basic rip")
                output_files = self._handle_basic_rip(disc_info, titles)

            return output_files

        except Exception as e:
            logger.exception(f"Error handling TV series: {e}")
            return self._handle_basic_rip(disc_info, titles)

    def _handle_cartoon_collection(
        self,
        disc_info: DiscInfo,
        titles: list,
        content_pattern: "ContentPattern",
    ) -> list:
        """Handle cartoon collections - rip all cartoon shorts."""
        try:
            logger.info("Ripping cartoon collection - processing all shorts")
            output_files = []

            # Filter for cartoon-length titles
            cartoon_titles = [
                t
                for t in titles
                if self.config.cartoon_min_duration * 60
                <= t.duration
                <= self.config.cartoon_max_duration * 60
            ]

            for _i, title in enumerate(cartoon_titles):
                output_file = self.ripper.rip_title(
                    title,
                    self.config.staging_dir / "cartoons",
                )
                output_files.append(output_file)

            return output_files

        except Exception as e:
            logger.exception(f"Error handling cartoon collection: {e}")
            return self._handle_basic_rip(disc_info, titles)

    def _handle_movie(
        self,
        disc_info: DiscInfo,
        titles: list,
        content_pattern: "ContentPattern",
    ) -> list:
        """Handle movies - select main title and optionally extras."""
        try:
            # Select main movie title using intelligent analysis
            main_title = self.ripper.select_main_title(titles, disc_info.label)

            if not main_title:
                msg = "No suitable movie title found"
                raise RuntimeError(msg)

            logger.info(f"Selected main movie title: {main_title.name}")

            # Rip main movie
            output_file = self.ripper.rip_title(
                main_title,
                self.config.staging_dir,
            )

            output_files = [output_file]

            # Handle extras if configured
            if self.config.include_movie_extras:
                logger.info("Processing movie extras")
                extra_titles = [
                    t
                    for t in titles
                    if (
                        t != main_title
                        and t.duration >= self.config.max_extras_duration * 60
                    )
                ]

                for extra_title in extra_titles:
                    extra_file = self.ripper.rip_title(
                        extra_title,
                        self.config.staging_dir / "extras",
                    )
                    output_files.append(extra_file)

            return output_files

        except Exception as e:
            logger.exception(f"Error handling movie: {e}")
            return self._handle_basic_rip(disc_info, titles)

    def _handle_basic_rip(self, disc_info: DiscInfo, titles: list) -> list:
        """Fallback to basic rip strategy."""
        try:
            logger.info("Using basic rip strategy")
            main_title = self.ripper.select_main_title(titles, disc_info.label)

            if not main_title:
                msg = "No suitable title found"
                raise RuntimeError(msg)

            output_file = self.ripper.rip_title(
                main_title,
                self.config.staging_dir,
            )

            return [output_file]

        except Exception as e:
            logger.exception(f"Basic rip failed: {e}")
            raise

    async def _process_queue_continuously(self) -> None:
        """Continuously process queue items in the background."""
        logger.info("Started background queue processor")

        while self.is_running:
            try:
                # Get next item to process
                item = self._get_next_processable_item()

                if item:
                    await self._process_single_item(item)
                else:
                    # No items to process, wait a bit
                    await asyncio.sleep(self.config.queue_poll_interval)

            except asyncio.CancelledError:
                logger.info("Queue processor cancelled")
                break
            except Exception as e:
                logger.exception(f"Error in queue processor: {e}")
                await asyncio.sleep(
                    self.config.error_retry_interval,
                )  # Wait before retrying

        logger.info("Background queue processor stopped")

    def _get_next_processable_item(self) -> QueueItem | None:
        """Get the next item that needs processing."""
        # Get items that are ready for the next step
        processable_statuses = [
            QueueItemStatus.RIPPED,  # Ready for identification
            QueueItemStatus.IDENTIFIED,  # Ready for encoding
            QueueItemStatus.ENCODED,  # Ready for organization
        ]

        for status in processable_statuses:
            items = self.queue_manager.get_items_by_status(status)
            if items:
                return items[0]  # Return oldest item

        return None

    async def _process_single_item(self, item: QueueItem) -> None:
        """Process a single queue item through its next stage."""
        try:
            logger.info(f"Processing: {item}")

            if item.status == QueueItemStatus.RIPPED:
                await self._identify_item(item)
            elif item.status == QueueItemStatus.IDENTIFIED:
                await self._encode_item(item)
            elif item.status == QueueItemStatus.ENCODED:
                await self._organize_item(item)

        except Exception as e:
            logger.exception(f"Error processing {item}: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            self.notifier.notify_error(f"Processing failed: {e}", context=str(item))

    async def _identify_item(self, item: QueueItem) -> None:
        """Identify media for a ripped item."""
        if not item.ripped_file:
            logger.error("No ripped file found for item")
            item.status = QueueItemStatus.FAILED
            item.error_message = "No ripped file found"
            self.queue_manager.update_item(item)
            return

        logger.info(f"Identifying: {item.ripped_file.name}")

        item.status = QueueItemStatus.IDENTIFYING
        self.queue_manager.update_item(item)

        # Identify the media
        media_info = await self.identifier.identify_media(item.ripped_file)

        if media_info:
            item.media_info = media_info
            item.status = QueueItemStatus.IDENTIFIED
            logger.info(f"Identified: {media_info}")
        else:
            # Move to review directory
            self.organizer.create_review_directory(item.ripped_file, "unidentified")
            item.status = QueueItemStatus.REVIEW
            self.notifier.notify_unidentified_media(item.ripped_file.name)
            logger.warning(f"Could not identify: {item.ripped_file.name}")

        self.queue_manager.update_item(item)

    async def _encode_item(self, item: QueueItem) -> None:
        """Encode a identified item."""
        logger.info(f"Encoding: {item.media_info}")
        # Drapto handles encoding notifications, so spindle doesn't send duplicates

        item.status = QueueItemStatus.ENCODING
        self.queue_manager.update_item(item)

        # Encode the file with progress callback
        def encoding_progress_callback(progress_data: dict) -> None:
            """Handle progress updates from drapto encoding."""
            progress_type = progress_data.get("type", "")

            if progress_type == "stage_progress":
                stage = progress_data.get("stage", "")
                percent = progress_data.get("percent", 0)
                message = progress_data.get("message", "")
                eta_seconds = progress_data.get("eta_seconds")

                logger.info(f"Encoding {stage}: {percent:.1f}% - {message}")

                # Update item with current progress
                item.progress_stage = stage
                item.progress_percent = percent
                item.progress_message = message
                self.queue_manager.update_item(item)

            elif progress_type == "encoding_progress":
                percent = progress_data.get("percent", 0)
                speed = progress_data.get("speed", 0)
                fps = progress_data.get("fps", 0)
                eta_seconds = progress_data.get("eta_seconds", 0)

                logger.info(
                    f"Encoding: {percent:.1f}% (speed: {speed:.1f}x, fps: {fps:.1f}, ETA: {eta_seconds}s)",
                )

                # Update item with encoding progress
                item.progress_stage = "encoding"
                item.progress_percent = percent
                item.progress_message = f"Speed: {speed:.1f}x, FPS: {fps:.1f}"
                self.queue_manager.update_item(item)

            elif progress_type == "encoding_complete":
                size_reduction = progress_data.get("size_reduction_percent", 0)
                logger.info(
                    f"Encoding complete - size reduction: {size_reduction:.1f}%",
                )

            elif progress_type == "validation_complete":
                validation_passed = progress_data.get("validation_passed", False)
                if validation_passed:
                    logger.info("Encoding validation passed")
                else:
                    logger.warning("Encoding validation failed")

            elif progress_type == "error":
                error_msg = progress_data.get("message", "Unknown error")
                logger.error(f"Drapto error: {error_msg}")

            elif progress_type == "warning":
                warning_msg = progress_data.get("message", "Unknown warning")
                logger.warning(f"Drapto warning: {warning_msg}")

        if not item.ripped_file:
            logger.error("No ripped file found for encoding")
            item.status = QueueItemStatus.FAILED
            item.error_message = "No ripped file found for encoding"
            self.queue_manager.update_item(item)
            return

        result = self.encoder.encode_file(
            item.ripped_file,
            self.config.staging_dir / "encoded",
            progress_callback=encoding_progress_callback,
        )

        if result.success:
            item.encoded_file = result.output_file
            item.status = QueueItemStatus.ENCODED
            # Drapto already sent encoding completion notification
            logger.info(f"Encoded: {result.output_file}")
        else:
            item.status = QueueItemStatus.FAILED
            item.error_message = result.error_message
            self.notifier.notify_error(
                f"Encoding failed: {result.error_message}",
                context=str(item.media_info),
            )
            logger.error(f"Encoding failed: {result.error_message}")

        self.queue_manager.update_item(item)

    async def _organize_item(self, item: QueueItem) -> None:
        """Organize and import an encoded item."""
        if not item.encoded_file:
            logger.error("No encoded file found for organizing")
            item.status = QueueItemStatus.FAILED
            item.error_message = "No encoded file found for organizing"
            self.queue_manager.update_item(item)
            return

        if not item.media_info:
            logger.error("No media info found for organizing")
            item.status = QueueItemStatus.FAILED
            item.error_message = "No media info found for organizing"
            self.queue_manager.update_item(item)
            return

        logger.info(f"Organizing: {item.media_info}")

        item.status = QueueItemStatus.ORGANIZING
        self.queue_manager.update_item(item)

        # Organize and import to Plex
        if self.organizer.add_to_plex(item.encoded_file, item.media_info):
            item.status = QueueItemStatus.COMPLETED
            self.notifier.notify_media_added(
                str(item.media_info),
                item.media_info.media_type,
            )
            logger.info(f"Added to Plex: {item.media_info}")
        else:
            item.status = QueueItemStatus.FAILED
            item.error_message = "Failed to organize/import to Plex"
            self.notifier.notify_error(
                "Failed to organize/import to Plex",
                context=str(item.media_info),
            )
            logger.error("Failed to organize/import to Plex")

        self.queue_manager.update_item(item)

    def get_status(self) -> dict:
        """Get current processor status."""
        stats = self.queue_manager.get_queue_stats()

        # Check for current disc
        current_disc = detect_disc(self.config.optical_drive)

        return {
            "running": self.is_running,
            "current_disc": str(current_disc) if current_disc else None,
            "queue_stats": stats,
            "total_items": sum(stats.values()) if stats else 0,
        }
