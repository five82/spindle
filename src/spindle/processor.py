"""Continuous queue processor for automated workflow."""

import asyncio
import logging
import time
from collections.abc import Callable
from pathlib import Path
from typing import TYPE_CHECKING

from .config import SpindleConfig
from .error_handling import (
    ErrorCategory,
    ExternalToolError,
    HardwareError,
    MediaError,
    SpindleError,
)

if TYPE_CHECKING:
    from .disc.analyzer import ContentPattern
from .disc.analyzer import DiscAnalysisResult, IntelligentDiscAnalyzer
from .disc.monitor import DiscInfo, DiscMonitor, detect_disc, eject_disc
from .disc.multi_disc import SimpleMultiDiscManager
from .disc.rip_spec import RipSpec
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
        self.multi_disc_manager = SimpleMultiDiscManager(config)

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

        # Reset any items stuck in processing status from previous run
        reset_count = self.queue_manager.reset_stuck_processing_items()
        if reset_count > 0:
            logger.info("Reset %s stuck items to pending status", reset_count)

        # Start disc monitoring
        self.disc_monitor = DiscMonitor(
            device=self.config.optical_drive,
            callback=self._on_disc_detected,
        )
        self.disc_monitor.start_monitoring()

        # Start background queue processing
        loop = asyncio.get_event_loop()
        self.processing_task = loop.create_task(self._process_queue_continuously())

        # Check for existing disc in drive after event loop is set up
        existing_disc = detect_disc(self.config.optical_drive)
        if existing_disc:
            logger.info("Found existing disc: %s", existing_disc)
            self._on_disc_detected(existing_disc)

        if existing_disc:
            logger.info("Continuous processor started - processing existing disc")
        else:
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
        logger.info("Detected disc: %s", disc_info)
        self.notifier.notify_disc_detected(disc_info.label, disc_info.disc_type)

        try:
            # Add to queue
            item = self.queue_manager.add_disc(disc_info.label)
            logger.info("Added to queue: %s", item)

            # Start identification immediately (schedule on existing event loop)
            logger.info("Creating identification task for %s", disc_info.label)
            loop = asyncio.get_event_loop()
            task = loop.create_task(self._identify_disc(item, disc_info))
            logger.info("Identification task created: %s", task)

            # Store reference to prevent "never awaited" warnings and handle exceptions
            def handle_task_completion(t):
                logger.info("Identification task completed: %s", t)
                if not t.cancelled() and t.exception():
                    logger.exception(
                        "Identification task failed",
                        exc_info=t.exception(),
                    )
                    self.notifier.notify_error(
                        f"Failed to identify disc: {t.exception()}",
                        context=disc_info.label,
                    )
                else:
                    logger.info("Identification task succeeded")

            task.add_done_callback(handle_task_completion)

        except Exception as e:
            logger.exception("Error handling disc detection")
            self.notifier.notify_error(
                f"Failed to process disc: {e}",
                context=disc_info.label,
            )

    async def _identify_disc(self, item: QueueItem, disc_info: DiscInfo) -> None:
        """Identify the disc content and determine what to rip."""
        logger.info("=== _identify_disc CALLED for %s ===", disc_info.label)
        try:
            logger.info("Starting disc identification: %s", disc_info.label)

            # Update status to IDENTIFYING
            item.status = QueueItemStatus.IDENTIFYING
            item.progress_stage = "Scanning disc"
            item.progress_percent = 0.0
            item.progress_message = f"Analyzing {disc_info.label}"
            self.queue_manager.update_item(item)

            # First, scan the disc to get title information with raw MakeMKV output
            item.progress_stage = "MakeMKV scanning disc"
            item.progress_message = (
                "Reading disc structure with MakeMKV... (this may take 1-3 minutes)"
            )
            self.queue_manager.update_item(item)

            titles, makemkv_output = self.ripper.scan_disc_with_output()
            logger.info("Found %s titles on disc", len(titles))

            item.progress_percent = 25.0
            item.progress_message = f"Found {len(titles)} titles, analyzing content..."
            self.queue_manager.update_item(item)

            # Create RipSpec to organize all disc processing data
            rip_spec = RipSpec(
                disc_info=disc_info,
                titles=titles,
                queue_item=item,
                makemkv_output=makemkv_output,
                disc_path=self._get_disc_path(disc_info),
                device=self.config.optical_drive,
            )

            # Analyze disc content using TMDB and intelligent pattern analysis
            logger.info("Running intelligent disc analysis with TMDB lookup...")
            item.progress_percent = 50.0
            item.progress_message = "Identifying content with TMDB..."
            self.queue_manager.update_item(item)

            rip_spec.analysis_result = await self.disc_analyzer.analyze_disc(
                rip_spec.disc_info,
                rip_spec.titles,
                rip_spec.disc_path,
                rip_spec.makemkv_output,
            )

            logger.info(
                "Analysis complete - Content type: %s (confidence: %.2f)",
                rip_spec.analysis_result.content_type.value,
                rip_spec.analysis_result.confidence,
            )

            item.progress_percent = 75.0
            item.progress_message = (
                f"Identified as {rip_spec.analysis_result.content_type.value}"
            )
            self.queue_manager.update_item(item)

            # Check for multi-disc set handling
            if self.config.auto_detect_multi_disc:
                item.progress_stage = "Analyzing multi-disc sets"
                item.progress_message = (
                    "Using cached disc metadata for multi-disc detection"
                )
                item.progress_percent = 50.0
                self.queue_manager.update_item(item)

                # Use enhanced_metadata from analysis result instead of re-scanning
                rip_spec.enhanced_metadata = rip_spec.analysis_result.enhanced_metadata

                # Only populate MakeMKV data if we have both enhanced metadata and makemkv output
                if rip_spec.enhanced_metadata and rip_spec.makemkv_output:
                    rip_spec.enhanced_metadata = self.disc_analyzer.metadata_extractor.populate_makemkv_data_from_output(
                        rip_spec.enhanced_metadata,
                        rip_spec.makemkv_output,
                        rip_spec.titles,
                    )

                if rip_spec.enhanced_metadata:
                    tv_info = self.multi_disc_manager.detect_tv_series_disc(
                        rip_spec.disc_info,
                        rip_spec.enhanced_metadata,
                    )

                    if tv_info:
                        logger.info("TV series disc detected, caching metadata")
                        rip_spec.is_multi_disc = True
                        rip_spec.tv_series_info = tv_info
                        return await self._handle_multi_disc_identification(rip_spec)

            # Update progress after metadata extraction
            item.progress_stage = "Finalizing identification"
            item.progress_message = "Completing content analysis..."
            item.progress_percent = 90.0
            self.queue_manager.update_item(item)

            # Store the analysis result in the queue item for the ripping phase
            await self._complete_disc_identification(rip_spec)

        except Exception as e:
            # Classify and handle the error appropriately
            self._handle_identification_error(e, item, disc_info.label)

    async def _process_single_disc(self, rip_spec: RipSpec) -> None:
        """Process a single disc (non-multi-disc)."""

        # Log metadata if found
        if rip_spec.analysis_result.metadata and hasattr(
            rip_spec.analysis_result.metadata,
            "title",
        ):
            logger.info("Identified as: %s", rip_spec.analysis_result.metadata.title)
            if hasattr(rip_spec.analysis_result.metadata, "year"):
                logger.info(f"Year: {rip_spec.analysis_result.metadata.year}")
            if hasattr(rip_spec.analysis_result.metadata, "overview"):
                logger.info(
                    f"Overview: {rip_spec.analysis_result.metadata.overview[:200]}...",
                )

        # Update progress with analysis results
        rip_spec.update_progress(
            stage=f"Identified: {rip_spec.analysis_result.content_type.value}",
            percent=10,
        )
        self.queue_manager.update_item(rip_spec.queue_item)

        # Create progress callback for ripping
        last_logged_percent = -1

        def ripping_progress_callback(progress_data: dict) -> None:
            """Handle progress updates from MakeMKV ripping."""
            nonlocal last_logged_percent
            if progress_data.get("type") == "ripping_progress":
                percentage = progress_data.get("percentage", 0)

                # Only log if percentage changed significantly
                if abs(percentage - last_logged_percent) >= 5:
                    logger.info(f"Ripping progress: {percentage:.0f}%")
                    last_logged_percent = percentage

                # Update progress via RipSpec
                rip_spec.update_progress(
                    stage=f"Ripping - {percentage:.0f}%",
                    percent=10 + (percentage * 0.8),  # 10-90% for ripping
                    message=f"{percentage:.0f}% complete",
                )
                self.queue_manager.update_item(rip_spec.queue_item)

            elif progress_data.get("type") == "ripping_status":
                message = progress_data.get("message", "")
                logger.debug(f"MakeMKV: {message}")

        # Store progress callback in RipSpec
        rip_spec.progress_callback = ripping_progress_callback

        # Handle different content types with progress callback
        output_files = await self._handle_content_type_with_analysis(
            rip_spec.disc_info,
            rip_spec.analysis_result,
            rip_spec.progress_callback,
        )

        # Store output files in RipSpec
        if output_files:
            for file_path in output_files:
                rip_spec.add_ripped_file(file_path)

        # Update item with ripped file
        rip_spec.queue_item.ripped_file = (
            rip_spec.ripped_files[0] if rip_spec.ripped_files else None
        )
        rip_spec.queue_item.status = QueueItemStatus.RIPPED
        self.queue_manager.update_item(rip_spec.queue_item)

        # Calculate duration
        duration = time.strftime("%H:%M:%S", time.gmtime(rip_spec.processing_duration))

        logger.info(
            f"Rip completed: {rip_spec.ripped_files[0] if rip_spec.ripped_files else 'No files'}",
        )
        self.notifier.notify_rip_completed(rip_spec.disc_label, duration)

        # Eject disc
        eject_disc(self.config.optical_drive)
        logger.info("Disc ejected - ready for next disc")

    async def _handle_multi_disc_processing(self, rip_spec: RipSpec) -> None:
        """Handle processing of a TV series disc with metadata caching."""

        if hasattr(rip_spec, "tv_series_info") and rip_spec.tv_series_info:
            tv_info = rip_spec.tv_series_info

            # Process TV series disc individually with metadata caching
            tv_info, cached_media_info = self.multi_disc_manager.process_tv_series_disc(
                rip_spec.disc_info,
                rip_spec.enhanced_metadata,
                rip_spec.media_info,
            )

            if cached_media_info:
                rip_spec.media_info = cached_media_info

            # Update progress
            rip_spec.update_progress(
                stage=f"TV series disc: {tv_info.series_title} S{tv_info.season_number} D{tv_info.disc_number}",
                percent=10,
            )
            self.queue_manager.update_item(rip_spec.queue_item)

        # For the simplified approach, just proceed with single disc completion
        await self._complete_disc_identification(rip_spec)

    async def _handle_content_type_with_analysis(
        self,
        disc_info: DiscInfo,
        analysis_result: DiscAnalysisResult,
        progress_callback: Callable | None = None,
    ) -> list:
        """Handle content based on disc analysis result."""

        # Store metadata if available for later identification
        if analysis_result.metadata:
            logger.info("Using pre-identified metadata from disc analysis")

        # Use the intelligently selected titles from the analysis
        titles_to_rip = analysis_result.titles_to_rip
        logger.info(f"Ripping {len(titles_to_rip)} intelligently selected titles")

        output_files = []
        for title in titles_to_rip:
            logger.info(f"Ripping: {title}")

            # Check for episode mapping for TV shows
            if (
                analysis_result.episode_mappings
                and title in analysis_result.episode_mappings
            ):
                episode_info = analysis_result.episode_mappings[title]
                logger.info(
                    f"Title mapped to: S{episode_info.season_number:02d}E{episode_info.episode_number:02d} - "
                    f"{episode_info.episode_title}",
                )

            output_path = self.ripper.rip_title(
                title,
                self.config.staging_dir / "ripped",
                progress_callback=progress_callback,
            )
            output_files.append(output_path)
            logger.info(f"Ripped to: {output_path}")

        return output_files

    async def _handle_content_type(
        self,
        disc_info: DiscInfo,
        titles: list,
        content_pattern: "ContentPattern",
        progress_callback: Callable | None = None,
    ) -> list:
        """Handle different content types with appropriate strategies."""
        from .disc.analyzer import ContentType

        content_type = content_pattern.type

        if content_type == ContentType.TV_SERIES:
            # Handle TV series (includes cartoon shorts since Plex organizes them as TV shows)
            return await self._handle_tv_series(disc_info, titles, progress_callback)

        if content_type == ContentType.MOVIE:
            # Handle movies - select main title and optionally extras
            return self._handle_movie(
                disc_info,
                titles,
                content_pattern,
                progress_callback,
            )

        # Unknown content type - use basic strategy
        logger.warning(f"Unknown content type {content_type}, using basic rip strategy")
        return self._handle_basic_rip(disc_info, titles, progress_callback)

    async def _handle_tv_series(
        self,
        disc_info: DiscInfo,
        titles: list,
        progress_callback: Callable | None = None,
    ) -> list:
        """Handle TV series using intelligent episode mapping."""
        try:
            # Use TV analyzer for episode mapping
            episode_mapping = await self.tv_analyzer.analyze_tv_disc(
                disc_info.label,
                titles,
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
                        progress_callback=progress_callback,
                    )
                    output_files.append(output_file)

            else:
                logger.warning("No episode mapping found, using basic rip")
                output_files = self._handle_basic_rip(
                    disc_info,
                    titles,
                    progress_callback,
                )

            return output_files

        except Exception as e:
            logger.exception(f"Error handling TV series: {e}")
            return self._handle_basic_rip(disc_info, titles, progress_callback)

    def _handle_cartoon_collection(
        self,
        disc_info: DiscInfo,
        titles: list,
        content_pattern: "ContentPattern",
        progress_callback: Callable | None = None,
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
                    progress_callback=progress_callback,
                )
                output_files.append(output_file)

            return output_files

        except Exception as e:
            logger.exception(f"Error handling cartoon collection: {e}")
            return self._handle_basic_rip(disc_info, titles, progress_callback)

    def _handle_movie(
        self,
        disc_info: DiscInfo,
        titles: list,
        content_pattern: "ContentPattern",
        progress_callback: Callable | None = None,
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
                progress_callback=progress_callback,
            )

            output_files = [output_file]

            # Handle extras if configured
            if self.config.include_extras:
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
                        progress_callback=progress_callback,
                    )
                    output_files.append(extra_file)

            return output_files

        except Exception as e:
            logger.exception(f"Error handling movie: {e}")
            return self._handle_basic_rip(disc_info, titles, progress_callback)

    def _handle_basic_rip(
        self,
        disc_info: DiscInfo,
        titles: list,
        progress_callback: Callable | None = None,
    ) -> list:
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
                progress_callback=progress_callback,
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
            QueueItemStatus.IDENTIFIED,  # Ready for ripping
            QueueItemStatus.RIPPED,  # Ready for encoding (background)
            QueueItemStatus.ENCODED,  # Ready for organization (background)
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

            if item.status == QueueItemStatus.IDENTIFIED:
                await self._rip_identified_item(item)
            elif item.status == QueueItemStatus.RIPPED:
                await self._encode_item(item)
            elif item.status == QueueItemStatus.ENCODED:
                await self._organize_item(item)

        except Exception as e:
            logger.exception(f"Error processing {item}: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)

    async def _rip_identified_item(self, item: QueueItem) -> None:
        """Rip an identified disc item."""
        logger.info("Starting rip phase for %s", item.disc_title)

        # Reconstruct RipSpec from stored data
        rip_spec = self._reconstruct_rip_spec_from_item(item)
        if not rip_spec:
            error_msg = f"Could not reconstruct rip specification for {item.disc_title}"
            raise RuntimeError(error_msg)

        # Update status to RIPPING
        item.status = QueueItemStatus.RIPPING
        item.progress_stage = "Ripping"
        item.progress_percent = 0.0
        item.progress_message = "Starting disc rip"
        self.queue_manager.update_item(item)

        # Execute the ripping
        await self._rip_single_disc_titles(rip_spec)

        # Update item with results
        if rip_spec.ripped_files:
            item.ripped_file = rip_spec.ripped_files[0]
            item.status = QueueItemStatus.RIPPED
            item.progress_stage = "Ripping complete"
            item.progress_percent = 100.0
            item.progress_message = f"Ripped {len(rip_spec.ripped_files)} files"
            self.queue_manager.update_item(item)
            logger.info("Rip phase complete for %s", item.disc_title)
        else:
            error_msg = f"No files were ripped for {item.disc_title}"
            raise RuntimeError(error_msg)

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
        logger.info("Encoding: %s", item.media_info)
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

                logger.info("Encoding %s: %.1f%% - %s", stage, percent, message)

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
                    "Encoding: %.1f%% (speed: %.1fx, fps: %.1f, ETA: %ss)",
                    percent,
                    speed,
                    fps,
                    eta_seconds,
                )

                # Update item with encoding progress
                item.progress_stage = "encoding"
                item.progress_percent = percent
                item.progress_message = f"Speed: {speed:.1f}x, FPS: {fps:.1f}"
                self.queue_manager.update_item(item)

            elif progress_type == "encoding_complete":
                size_reduction = progress_data.get("size_reduction_percent", 0)
                logger.info(
                    "Encoding complete - size reduction: %.1f%%",
                    size_reduction,
                )

            elif progress_type == "validation_complete":
                validation_passed = progress_data.get("validation_passed", False)
                if validation_passed:
                    logger.info("Encoding validation passed")
                else:
                    logger.warning("Encoding validation failed")

            elif progress_type == "error":
                error_msg = progress_data.get("message", "Unknown error")
                logger.error("Drapto error: %s", error_msg)

            elif progress_type == "warning":
                warning_msg = progress_data.get("message", "Unknown warning")
                logger.warning("Drapto warning: %s", warning_msg)

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
            logger.info("Encoded: %s", result.output_file)
        else:
            item.status = QueueItemStatus.FAILED
            item.error_message = result.error_message
            self.notifier.notify_error(
                f"Encoding failed: {result.error_message}",
                context=str(item.media_info),
            )
            logger.error("Encoding failed: %s", result.error_message)

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

        logger.info("Organizing: %s", item.media_info)

        item.status = QueueItemStatus.ORGANIZING
        self.queue_manager.update_item(item)

        # Organize and import to Plex
        try:
            if self.organizer.add_to_plex(item.encoded_file, item.media_info):
                item.status = QueueItemStatus.COMPLETED
                self.notifier.notify_media_added(
                    str(item.media_info),
                    item.media_info.media_type,
                )
                logger.info("Added to Plex: %s", item.media_info)
            else:
                item.status = QueueItemStatus.FAILED
                item.error_message = "Failed to organize/import to Plex"
                self.notifier.notify_error(
                    "Failed to organize/import to Plex",
                    context=str(item.media_info),
                )
                logger.error("Failed to organize/import to Plex")
        except Exception as e:
            logger.exception(f"Error organizing item: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.notifier.notify_error(
                f"Organization failed: {e}",
                context=str(item.media_info),
            )

        self.queue_manager.update_item(item)

    async def _complete_disc_identification(self, rip_spec: RipSpec) -> None:
        """Complete the disc identification phase and prepare for ripping."""
        item = rip_spec.queue_item

        try:
            # Extract media information from analysis result
            if rip_spec.analysis_result.metadata:
                item.media_info = rip_spec.analysis_result.metadata

            # Store the analysis result data for the ripping phase
            # We'll serialize key data to the queue item for persistence
            rip_spec_data = {
                "analysis_result": {
                    "content_type": rip_spec.analysis_result.content_type.value,
                    "confidence": rip_spec.analysis_result.confidence,
                    "titles_to_rip": (
                        [
                            {
                                "index": t.title_id,
                                "name": t.name,
                                "duration": t.duration,
                                "size": t.size,
                                "chapters": t.chapters,
                                "tracks": (
                                    [
                                        {"id": tr.track_id, "type": tr.track_type}
                                        for tr in t.tracks
                                    ]
                                    if t.tracks
                                    else []
                                ),
                            }
                            for t in rip_spec.analysis_result.titles_to_rip
                        ]
                        if rip_spec.analysis_result.titles_to_rip
                        else []
                    ),
                    "episode_mappings": (
                        {
                            str(k.index): {
                                "season_number": v.season_number,
                                "episode_number": v.episode_number,
                                "episode_title": v.episode_title,
                                "air_date": v.air_date,
                                "overview": v.overview,
                                "runtime": v.runtime,
                            }
                            for k, v in rip_spec.analysis_result.episode_mappings.items()
                        }
                        if rip_spec.analysis_result.episode_mappings
                        else {}
                    ),
                },
                "disc_info": {
                    "label": rip_spec.disc_info.label,
                    "device": rip_spec.disc_info.device,
                },
                "is_multi_disc": rip_spec.is_multi_disc,
                "disc_set_info": (
                    rip_spec.disc_set_info.__dict__ if rip_spec.disc_set_info else None
                ),
            }

            # Store in a field we'll add to QueueItem (we'll need to update the schema)
            item.rip_spec_data = rip_spec_data

            # Mark as identified and ready for ripping
            item.status = QueueItemStatus.IDENTIFIED
            item.progress_stage = "Content identified"
            item.progress_percent = 100.0

            # Update disc title to use the identified media name
            if item.media_info and item.media_info.title:
                item.disc_title = item.media_info.title
                item.progress_message = f"Ready to rip: {item.media_info.title}"
            else:
                item.progress_message = (
                    f"Ready to rip: {rip_spec.analysis_result.content_type.value}"
                )

            self.queue_manager.update_item(item)

            logger.info("Disc identification complete: %s", item)

        except Exception as e:
            self._handle_identification_error(e, item, rip_spec.disc_info.label)

    async def _handle_multi_disc_identification(self, rip_spec: RipSpec) -> None:
        """Handle multi-disc set identification."""
        await self._complete_disc_identification(rip_spec)

    def _reconstruct_rip_spec_from_item(self, item: QueueItem) -> "RipSpec":
        """Reconstruct a RipSpec from stored queue item data."""
        if not item.rip_spec_data:
            logger.error(f"No rip_spec_data found for item {item.disc_title}")
            return None

        try:
            from .disc.analyzer import ContentType, DiscAnalysisResult
            from .disc.monitor import DiscInfo
            from .disc.rip_spec import RipSpec
            from .disc.ripper import Title

            # Reconstruct DiscInfo
            disc_data = item.rip_spec_data["disc_info"]
            disc_info = DiscInfo(
                device=disc_data["device"],
                disc_type="Blu-ray",  # Could store this if needed
                label=disc_data["label"],
            )

            # Reconstruct titles from analysis result
            analysis_data = item.rip_spec_data["analysis_result"]
            titles = []
            for title_data in analysis_data["titles_to_rip"]:
                from .disc.ripper import Track

                # Reconstruct tracks if available
                tracks = []
                for track_data in title_data.get("tracks", []):
                    # Create minimal track objects with default values
                    track = Track(
                        track_id=track_data["id"],
                        track_type=track_data["type"],
                        codec="Unknown",  # Default codec
                        language="Unknown",  # Default language
                        duration=title_data["duration"],  # Use title duration
                        size=0,  # Default size
                        title=f"Track {track_data['id']}",  # Default title
                    )
                    tracks.append(track)

                title = Title(
                    title_id=title_data["index"],
                    duration=title_data["duration"],
                    size=title_data.get("size", 0),  # Default to 0 if missing
                    chapters=title_data.get("chapters", 1),  # Default to 1 if missing
                    tracks=tracks,
                    name=title_data["name"],
                )
                titles.append(title)

            # Reconstruct DiscAnalysisResult
            content_type = ContentType(analysis_data["content_type"])
            analysis_result = DiscAnalysisResult(
                disc_info=disc_info,
                content_type=content_type,
                confidence=analysis_data["confidence"],
                titles_to_rip=titles,
                metadata=item.media_info,
                episode_mappings=None,  # Will be reconstructed if needed for TV series
                enhanced_metadata=None,  # Will be populated later if needed
            )

            # Create RipSpec
            rip_spec = RipSpec(
                disc_info=disc_info,
                titles=titles,
                queue_item=item,
                analysis_result=analysis_result,
                disc_path=self._get_disc_path(disc_info),
                device=self.config.optical_drive,
                is_multi_disc=item.rip_spec_data.get("is_multi_disc", False),
            )

            logger.info(f"Successfully reconstructed RipSpec for {item.disc_title}")
            return rip_spec

        except Exception as e:
            logger.exception(
                f"Failed to reconstruct RipSpec for {item.disc_title}: {e}",
            )
            return None

    async def _rip_single_disc_titles(self, rip_spec: RipSpec) -> None:
        """Rip the selected titles for a single disc."""
        if not rip_spec.analysis_result or not rip_spec.analysis_result.titles_to_rip:
            msg = "No titles selected for ripping"
            raise RuntimeError(msg)

        # Create output directory for this disc
        output_dir = self.config.staging_dir / "ripped"
        output_dir.mkdir(parents=True, exist_ok=True)

        logger.info(
            f"Starting to rip {len(rip_spec.analysis_result.titles_to_rip)} titles",
        )

        # Progress tracking
        total_titles = len(rip_spec.analysis_result.titles_to_rip)
        completed_titles = 0

        def update_progress(progress_data: dict):
            """Update queue item progress during ripping."""
            try:
                # Only show progress for actual ripping progress, not status messages
                if progress_data.get("type") == "ripping_progress":
                    title_progress = progress_data.get(
                        "percentage",
                        progress_data.get("percent", 0.0),
                    )
                    overall_progress = (completed_titles / total_titles * 100) + (
                        title_progress / total_titles
                    )

                    # Only show meaningful progress updates (> 0%)
                    if title_progress > 0:
                        operation = progress_data.get(
                            "stage",
                            progress_data.get("operation", "Ripping"),
                        )
                        logger.info(
                            f"Ripping progress: {title_progress:.1f}% - {operation}",
                        )

                    if rip_spec.queue_item:
                        rip_spec.queue_item.progress_percent = min(
                            overall_progress,
                            100.0,
                        )
                        rip_spec.queue_item.progress_message = (
                            f"Ripping title {completed_titles + 1}/{total_titles}: {operation} ({title_progress:.1f}%)"
                            if title_progress > 0
                            else f"Ripping title {completed_titles + 1}/{total_titles}: Starting..."
                        )

                        # Simple direct update - queue manager should be thread-safe
                        try:
                            self.queue_manager.update_item(rip_spec.queue_item)
                        except Exception as update_error:
                            logger.debug(f"Queue update error: {update_error}")

            except Exception as e:
                logger.exception(f"Error in progress callback: {e}")

        # Rip each selected title
        for title in rip_spec.analysis_result.titles_to_rip:
            logger.info(f"Ripping title: {title.name} ({title.duration//60}min)")

            try:
                output_file = await asyncio.get_event_loop().run_in_executor(
                    None,
                    self.ripper.rip_title,
                    title,
                    output_dir,
                    rip_spec.device,
                    update_progress,
                )

                rip_spec.ripped_files.append(output_file)
                completed_titles += 1

                logger.info(f"Successfully ripped: {output_file}")

            except Exception as e:
                logger.exception(f"Failed to rip title {title.name}: {e}")
                # Could implement retry logic or mark as failed
                raise

        logger.info(
            f"Successfully ripped {len(rip_spec.ripped_files)} titles to {output_dir}",
        )

        # Update rip spec with final output directory
        rip_spec.output_dir = output_dir

    async def _update_queue_item_async(self, item: QueueItem):
        """Thread-safe async queue item update."""
        self.queue_manager.update_item(item)

    async def _handle_multi_disc_ripping(self, rip_spec: RipSpec) -> None:
        """Handle ripping for multi-disc sets."""
        logger.warning("Multi-disc ripping not fully implemented yet")

    def _handle_identification_error(
        self,
        error: Exception,
        item: QueueItem,
        disc_label: str,
    ) -> None:
        """Handle identification errors."""

        # Use enhanced error handling for user-friendly messages
        logger.exception(f"Identification failed for {disc_label}: {error}")

        # Get user-friendly error message and solution
        if hasattr(error, "solution"):
            # ExternalToolError with solution
            user_message = str(error)
            progress_message = getattr(error, "solution", "See logs for details")
        else:
            # Generic error
            user_message = f"Identification failed: {error!s}"
            progress_message = "Check disc and try again"

        item.status = QueueItemStatus.FAILED
        item.error_message = user_message
        item.progress_stage = "Identification failed"
        item.progress_percent = 0.0
        item.progress_message = progress_message
        self.queue_manager.update_item(item)

        self.notifier.notify_error(
            f"Failed to identify disc: {user_message}",
            context=disc_label,
        )

    def get_status(self) -> dict:
        """Get current processor status."""
        stats = self.queue_manager.get_queue_stats()

        # Check for current disc - prefer identified title if available
        current_disc_name = None
        current_disc = detect_disc(self.config.optical_drive)
        if current_disc:
            # Check if we have a current processing item with identified title
            processing_items = []
            for status in [
                QueueItemStatus.PENDING,
                QueueItemStatus.IDENTIFYING,
                QueueItemStatus.IDENTIFIED,
                QueueItemStatus.RIPPING,
            ]:
                processing_items.extend(self.queue_manager.get_items_by_status(status))
            for item in processing_items:
                if item.disc_title != current_disc.label and item.media_info:
                    # This item has been identified with a different title
                    current_disc_name = item.disc_title
                    break

            # Fall back to raw disc label if no identified name found
            if not current_disc_name:
                current_disc_name = str(current_disc)

        return {
            "running": self.is_running,
            "current_disc": current_disc_name,
            "queue_stats": stats,
            "total_items": sum(stats.values()) if stats else 0,
        }

    def _get_disc_path(self, disc_info: DiscInfo) -> Path | None:
        """Get the disc path for metadata parsing."""
        try:
            # Check standard automounting locations
            standard_mount_points = [
                "/media/cdrom",
                "/media/cdrom0",
            ]

            for mount_point in standard_mount_points:
                mount_path = Path(mount_point)
                if mount_path.exists() and any(mount_path.iterdir()):
                    logger.debug(f"Found disc content at: {mount_path}")
                    return mount_path

            logger.info(
                "Disc not found at standard mount points",
            )
            logger.debug(
                "Ensure your system has automounting configured (desktop) or fstab entry (server)",
            )
            return None

        except Exception as e:
            logger.debug(f"Error determining disc path: {e}")
            return None

    def _handle_rip_error(
        self,
        error: Exception,
        item: QueueItem,
        disc_label: str,
    ) -> None:
        """Handle ripping errors with appropriate classification and user guidance."""
        error_str = str(error).lower()

        # Classify the error type for better user experience
        if any(
            keyword in error_str
            for keyword in ["license", "registration key", "too old", "expired"]
        ):
            # MakeMKV license issues
            enhanced_error = ExternalToolError(
                "MakeMKV",
                details=str(error),
                solution="Check MakeMKV license status. Free version has limitations on disc types and may require beta key updates.",
                recoverable=True,
            )
        elif any(
            keyword in error_str
            for keyword in ["no disc", "disc not found", "device not ready"]
        ):
            # Hardware/media issues
            enhanced_error = HardwareError(
                "No disc detected or drive not ready",
                solution="Ensure disc is properly inserted and drive is accessible",
            )
        elif any(
            keyword in error_str
            for keyword in ["read error", "bad sector", "corrupted"]
        ):
            # Media quality issues
            enhanced_error = MediaError(
                "Disc reading failed due to media quality issues",
                solution="Try cleaning the disc, checking for scratches, or using a different copy",
            )
        elif "permission denied" in error_str:
            # Filesystem permissions

            enhanced_error = SpindleError(
                str(error),
                ErrorCategory.FILESYSTEM,
                solution=f"Check file permissions for staging directory: {self.config.staging_dir}",
                recoverable=True,
            )
        else:
            # Generic system error
            enhanced_error = SpindleError(
                str(error),
                ErrorCategory.SYSTEM,
                solution="Check system resources and disc drive status",
                recoverable=True,
            )

        # Update queue item with enhanced error info
        item.status = QueueItemStatus.FAILED
        item.error_message = enhanced_error.message
        item.progress_message = f"Failed: {enhanced_error.category.value.title()} error"
        self.queue_manager.update_item(item)

        # Display user-friendly error
        enhanced_error.display_to_user()

        # Send notification with category context
        self.notifier.notify_error(
            f"Ripping failed ({enhanced_error.category.value}): {enhanced_error.message}",
            context=disc_label,
        )
