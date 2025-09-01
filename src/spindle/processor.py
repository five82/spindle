"""Continuous queue processor for automated workflow."""

import asyncio
import logging
import shutil
import time
from collections.abc import Callable
from pathlib import Path
from typing import TYPE_CHECKING, Any

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

        # Check if udisksctl is available for disc mounting
        self.udisks_available = shutil.which("udisksctl") is not None

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

        # Check for existing disc in drive
        existing_disc = detect_disc(self.config.optical_drive)
        if existing_disc:
            logger.info("Found existing disc: %s", existing_disc)
            self._on_disc_detected(existing_disc)

        # Start background queue processing
        loop = asyncio.get_event_loop()
        self.processing_task = loop.create_task(self._process_queue_continuously())

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

            # Start ripping immediately (schedule on existing event loop)
            loop = asyncio.get_event_loop()
            task = loop.create_task(self._rip_disc(item, disc_info))
            # Store reference to prevent "never awaited" warnings and handle exceptions
            task.add_done_callback(
                lambda t: t.exception() if not t.cancelled() else None,
            )

        except Exception as e:
            logger.exception("Error handling disc detection")
            self.notifier.notify_error(
                f"Failed to process disc: {e}",
                context=disc_info.label,
            )

    async def _rip_disc(self, item: QueueItem, disc_info: DiscInfo) -> None:
        """Rip the detected disc using intelligent analysis with multi-disc support."""
        try:
            logger.info("Starting intelligent analysis and rip: %s", disc_info.label)
            self.notifier.notify_rip_started(disc_info.label)

            # Update status
            item.status = QueueItemStatus.RIPPING
            self.queue_manager.update_item(item)

            # First, scan the disc to get title information with raw MakeMKV output
            titles, makemkv_output = self.ripper.scan_disc_with_output()
            logger.info("Found %s titles on disc", len(titles))

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

            # Check for multi-disc set handling
            if self.config.auto_detect_multi_disc:
                rip_spec.enhanced_metadata = (
                    self.disc_analyzer.metadata_extractor.extract_all_metadata(
                        rip_spec.disc_path,
                    )
                    if rip_spec.disc_path
                    else None
                )
                if rip_spec.enhanced_metadata and rip_spec.makemkv_output:
                    rip_spec.enhanced_metadata = self.disc_analyzer.metadata_extractor.populate_makemkv_data_from_output(
                        rip_spec.enhanced_metadata,
                        rip_spec.makemkv_output,
                        rip_spec.titles,
                    )

                if rip_spec.enhanced_metadata:
                    is_multi_disc, disc_set_info = (
                        self.multi_disc_manager.detect_multi_disc_set(
                            rip_spec.disc_info,
                            rip_spec.enhanced_metadata,
                        )
                    )

                    if is_multi_disc:
                        logger.info("Multi-disc set detected, managing session")
                        rip_spec.is_multi_disc = True
                        rip_spec.disc_set_info = disc_set_info
                        return await self._handle_multi_disc_processing(rip_spec)

            # Single disc processing
            await self._process_single_disc(rip_spec)

        except Exception as e:
            # Classify and handle the error appropriately
            self._handle_rip_error(e, item, disc_info.label)

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
        """Handle processing of a multi-disc set."""

        # Find or create session for this disc set
        session = self.multi_disc_manager.find_or_create_session(
            rip_spec.disc_set_info,
            rip_spec.disc_info,
            rip_spec.enhanced_metadata,
        )

        # Add this disc to the session
        disc_num = self.multi_disc_manager.add_disc_to_session(
            session,
            rip_spec.disc_info,
            rip_spec.enhanced_metadata,
            rip_spec.titles,
        )

        # Update progress
        rip_spec.update_progress(
            stage=f"Multi-disc set: {session.session_id} (Disc {disc_num})",
            percent=5,
        )
        self.queue_manager.update_item(rip_spec.queue_item)

        # Determine if we should wait for more discs
        if self.multi_disc_manager.should_wait_for_next_disc(session):
            logger.info(
                f"Waiting {self.config.multi_disc_timeout_minutes} minutes for next disc...",
            )

            rip_spec.update_progress(
                stage=f"Waiting for next disc (Disc {disc_num + 1})",
                percent=15,
                message=f"Insert next disc within {self.config.multi_disc_timeout_minutes} minutes",
            )
            self.queue_manager.update_item(rip_spec.queue_item)

            # Notify user to insert next disc
            self.notifier.notify_multi_disc_waiting(
                session.session_id,
                disc_num,
                self.config.multi_disc_timeout_minutes,
            )

            # Wait for timeout or next disc
            await self._wait_for_next_disc_or_timeout(session, rip_spec.queue_item)

        # Check if waiting timeout expired - if so, process what we have
        if self.multi_disc_manager.is_waiting_timeout_expired(session):
            logger.info("Multi-disc wait timeout expired, processing collected discs")

        # Finalize the session and get all titles
        all_titles, disc_map = self.multi_disc_manager.finalize_session(session)

        # Create enhanced analysis result for the complete set
        enhanced_analysis = DiscAnalysisResult(
            disc_info=rip_spec.disc_info,  # Use disc info from RipSpec
            content_type=rip_spec.analysis_result.content_type,
            confidence=rip_spec.analysis_result.confidence,
            titles_to_rip=all_titles,
            metadata=rip_spec.analysis_result.metadata,
            episode_mappings=None,
        )

        # Process the complete multi-disc set
        await self._process_complete_disc_set(
            rip_spec.queue_item,
            session,
            enhanced_analysis,
        )

    async def _wait_for_next_disc_or_timeout(
        self,
        session: Any,
        item: QueueItem,
    ) -> None:
        """Wait for next disc insertion or timeout."""
        import asyncio

        timeout_seconds = self.config.multi_disc_timeout_minutes * 60
        start_time = time.time()

        while time.time() - start_time < timeout_seconds:
            # Check if a new disc has been inserted
            current_disc = detect_disc(self.config.optical_drive)

            if current_disc and current_disc.label != item.disc_title:
                logger.info(f"New disc detected: {current_disc.label}")
                # New disc detected, break out of waiting
                return

            # Update progress countdown
            elapsed = time.time() - start_time
            remaining_minutes = (timeout_seconds - elapsed) / 60
            item.progress_message = (
                f"Waiting for next disc ({remaining_minutes:.1f}min remaining)"
            )
            self.queue_manager.update_item(item)

            # Wait 10 seconds before checking again
            await asyncio.sleep(10)

        logger.info("Multi-disc timeout reached")

    async def _process_complete_disc_set(
        self,
        item: QueueItem,
        session: Any,
        enhanced_analysis: DiscAnalysisResult,
    ) -> None:
        """Process the complete multi-disc set."""

        start_time = time.time()

        # Update status
        item.progress_stage = (
            f"Processing {len(session.disc_set.processed_discs)} disc set"
        )
        item.progress_percent = 20
        self.queue_manager.update_item(item)

        # Create progress callback for multi-disc ripping
        total_titles = len(enhanced_analysis.titles_to_rip)
        processed_titles = 0

        def multi_disc_progress_callback(progress_data: dict) -> None:
            nonlocal processed_titles
            if progress_data.get("type") == "ripping_progress":
                percentage = progress_data.get("percentage", 0)
                # Calculate overall progress across all discs
                title_progress = (
                    processed_titles / total_titles
                ) * 80  # 80% for all titles
                current_title_progress = (percentage / 100) * (80 / total_titles)
                overall_progress = 20 + title_progress + current_title_progress

                item.progress_stage = (
                    f"Ripping disc set ({processed_titles + 1}/{total_titles})"
                )
                item.progress_percent = min(overall_progress, 90)
                item.progress_message = (
                    f"Title {processed_titles + 1}/{total_titles}: {percentage:.0f}%"
                )
                self.queue_manager.update_item(item)

            elif progress_data.get("type") == "title_completed":
                processed_titles += 1
                logger.info(f"Completed title {processed_titles}/{total_titles}")

        # Process the enhanced analysis result
        output_files = await self._handle_content_type_with_analysis(
            session.disc_set.processed_discs[1],  # Use first disc info
            enhanced_analysis,
            multi_disc_progress_callback,
        )

        # Store primary output file for queue tracking
        output_file = output_files[0] if output_files else None

        # Update item with ripped file
        item.ripped_file = output_file
        item.status = QueueItemStatus.RIPPED
        self.queue_manager.update_item(item)

        # Calculate duration
        duration = time.strftime("%H:%M:%S", time.gmtime(time.time() - start_time))

        logger.info(f"Multi-disc set completed: {output_file}")
        self.notifier.notify_multi_disc_completed(
            session.session_id,
            len(session.disc_set.processed_discs),
            duration,
        )

        # Clean up session
        if session.session_id in self.multi_disc_manager.active_sessions:
            del self.multi_disc_manager.active_sessions[session.session_id]

        # Eject final disc
        eject_disc(self.config.optical_drive)
        logger.info("Final disc ejected - multi-disc set complete")

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
