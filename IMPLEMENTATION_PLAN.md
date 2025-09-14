# Spindle Architecture Refactoring: Phase 1 & Phase 2 Implementation Plan

## Executive Summary

This document outlines the detailed implementation plan for refactoring Spindle's architecture into a more modular, daemon-only system. The plan is divided into two phases that maintain architectural consistency while enhancing maintainability and user experience.

**Core Goals:**
- Remove confusing foreground mode that conflicts with two-phase workflow
- Implement clean daemon-only operation with monitoring capability
- Improve internal module organization without over-engineering
- Maintain "insert and forget" user experience philosophy

## Current Architecture Analysis

### Existing Structure
```
src/spindle/
├── __init__.py
├── cli.py                    # Main CLI with both daemon/foreground modes
├── processor.py              # ContinuousProcessor orchestrates workflow
├── config.py                # Configuration management
├── error_handling.py        # Enhanced error handling system
├── system_check.py          # System dependency validation
├── process_lock.py          # Process locking for single instance
├── disc/                    # Disc processing components
│   ├── monitor.py           # Disc detection and monitoring
│   ├── analyzer.py          # Content analysis and identification
│   ├── ripper.py           # MakeMKV wrapper
│   ├── rip_spec.py         # Rip specification data structures
│   └── [other disc modules]
├── identify/                # TMDB identification
├── encode/                  # Drapto encoding wrapper
├── organize/                # Plex library organization
├── queue/                   # SQLite queue management
└── notify/                  # ntfy.sh notifications
```

### Current Workflow Issues
1. **Foreground mode confusion**: Tries to display ongoing status for both disc-dependent (ripping) and background (encoding) processes
2. **Mixed responsibilities**: CLI handles both orchestration and display concerns
3. **Monitoring limitations**: No way to observe daemon without disrupting it

## Phase 1: Remove Foreground Mode & Add Daemon Monitoring

### 1.1 Remove Foreground Mode

**Files to Modify:**
- `src/spindle/cli.py`

**Changes:**
1. **Remove foreground option from start command**
   ```python
   @cli.command()
   @click.option("--systemd", is_flag=True, help="Running under systemd (internal)")
   @click.pass_context
   def start(ctx: click.Context, systemd: bool) -> None:
       """Start continuous processing daemon - auto-rip discs and process queue."""
       config: SpindleConfig = ctx.obj["config"]
       
       # Check system dependencies before starting
       console.print("Checking system dependencies...")
       check_system_dependencies(validate_required=True)
       
       # Always run as daemon unless systemd (which needs foreground for logging)
       is_systemd = systemd or os.getenv("INVOCATION_ID") is not None
       
       if is_systemd:
           start_systemd_mode(config)
       else:
           start_daemon(config)
   ```

2. **Remove `start_foreground()` function entirely**
3. **Simplify `start_daemon()` function** - remove foreground mode detection logic
4. **Add new `start_systemd_mode()` function** for systemd compatibility that runs in foreground but with appropriate logging

### 1.2 Implement `spindle show` Command (Hybrid Approach)

**Hybrid "tail -f" + Colorizer Implementation:**
```python
@cli.command()
@click.option("--follow", "-f", is_flag=True, help="Follow log output")
@click.option("--lines", "-n", type=int, default=10, help="Number of lines to show")
@click.pass_context
def show(ctx: click.Context, follow: bool, lines: int) -> None:
    """Show Spindle daemon log output with colors."""
    config: SpindleConfig = ctx.obj["config"]
    log_file = config.log_dir / "spindle.log"
    
    if not log_file.exists():
        console.print("[yellow]No log file found[/yellow]")
        console.print(f"Expected location: {log_file}")
        sys.exit(1)
    
    # Use subprocess to stream tail output with real-time coloring
    import subprocess
    
    try:
        if follow:
            cmd = ["tail", "-f", str(log_file)]
        else:
            cmd = ["tail", "-n", str(lines), str(log_file)]
            
        # Stream output and colorize in real-time
        with subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, 
                             text=True, bufsize=1, universal_newlines=True) as proc:
            try:
                for line in proc.stdout:
                    _colorize_log_line(line.rstrip())
            except KeyboardInterrupt:
                proc.terminate()
                sys.exit(0)
                
        if proc.returncode != 0:
            console.print("[red]Error running tail command[/red]")
            sys.exit(1)
            
    except FileNotFoundError:
        console.print("[red]tail command not found - install coreutils[/red]")
        sys.exit(1)

def _colorize_log_line(line: str) -> None:
    """Colorize a single log line based on log level."""
    if " ERROR " in line:
        console.print(f"[red]{line}[/red]")
    elif " WARNING " in line:
        console.print(f"[yellow]{line}[/yellow]")
    elif " INFO " in line:
        console.print(f"[blue]{line}[/blue]")
    elif " DEBUG " in line:
        console.print(f"[dim]{line}[/dim]")
    else:
        # Default color for unrecognized log lines
        console.print(line)
```

**Key Benefits of Hybrid Approach:**
- **Unix Philosophy**: Uses battle-tested `tail` for file streaming
- **Enhanced UX**: Adds helpful color coding for log levels
- **Real-time Performance**: Streams and colors line-by-line, no buffering
- **Reliable**: `tail` handles file rotation, truncation, etc.
- **Familiar**: Standard tail behavior with visual enhancements
- **Minimal Overhead**: Only adds simple regex-based coloring
- **Interruptible**: Clean Ctrl+C handling

### 1.3 Update Help Text and Documentation

**Changes:**
1. Update CLI help text to reflect daemon-only operation
2. Update README/documentation to explain new `spindle show` usage
3. Remove references to foreground mode from user-facing documentation

### 1.4 Testing Requirements for Phase 1

**Test Updates Needed:**
- `tests/test_cli.py`: Remove foreground mode tests, add `show` command tests
- Integration tests: Verify daemon starts properly without foreground option
- Manual testing: Verify `spindle show` works with running daemon

## Phase 2: Internal Module Organization

### 2.1 Create Core Module Structure

**Objective:** Create the complete directory structure with stub module files to establish the new architecture foundation.

**New Structure:**
```
src/spindle/
├── core/                    # Core orchestration and workflow
│   ├── __init__.py
│   ├── daemon.py           # Daemon management (extracted from cli.py)
│   ├── orchestrator.py     # Main workflow orchestration (refactored processor.py)
│   └── workflow.py         # Workflow state management
├── components/             # Individual processing components
│   ├── __init__.py
│   ├── disc_handler.py     # Disc processing coordination
│   ├── identifier.py       # Content identification coordination
│   ├── encoder.py          # Encoding coordination
│   └── organizer.py        # Library organization coordination
├── services/               # External service integrations
│   ├── __init__.py
│   ├── makemkv.py         # MakeMKV integration (from disc/ripper.py)
│   ├── tmdb.py            # TMDB service (moved from identify/)
│   ├── drapto.py          # Drapto integration (from encode/)
│   ├── plex.py            # Plex integration (from organize/)
│   └── ntfy.py            # Notification service (from notify/)
├── storage/                # Data persistence
│   ├── __init__.py
│   ├── queue.py           # Queue management (from queue/)
│   └── cache.py           # Caching systems
├── disc/                   # Keep existing disc analysis modules
├── cli.py                  # Simplified CLI interface
├── config.py              # Keep existing
├── error_handling.py      # Keep existing
└── [other existing files]
```

**Implementation Details:**
- Create all directories with `__init__.py` files containing module documentation
- Create stub/placeholder files for all modules listed in the structure
- Stub files should contain basic class/function signatures but no implementation
- Maintain 100% backward compatibility - existing imports continue to work
- All tests must pass after structure creation

**Files to Create:**
- `core/daemon.py` - Stub SpindleDaemon class
- `core/orchestrator.py` - Stub SpindleOrchestrator class
- `core/workflow.py` - Stub workflow management classes
- `components/*.py` - Stub coordinator classes (DiscHandler, etc.)
- `services/*.py` - Stub service wrapper classes
- `storage/*.py` - Stub storage management classes

### 2.2 Implement Core Modules

**Scope:** Implement full functionality for stub classes created in Phase 2.1 with complete code migration from existing modules.

**Migration Strategy:**
1. Extract existing logic from `processor.py`, `cli.py`, and component modules
2. Preserve all functionality with cleaner separation of concerns
3. Maintain 100% backward compatibility during migration
4. Update imports progressively to use new module structure

#### 2.2.1 `src/spindle/core/daemon.py`
**Source:** Extract daemon management from `cli.py`

**Full Implementation:**
```python
"""Daemon management for Spindle."""

import logging
import os
import signal
import sys
import time
from pathlib import Path

try:
    import daemon
    import daemon.pidfile
except ImportError:
    daemon = None

from ..config import SpindleConfig
from ..process_lock import ProcessLock
from .orchestrator import SpindleOrchestrator

logger = logging.getLogger(__name__)

class SpindleDaemon:
    """Manages Spindle daemon lifecycle."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.orchestrator: SpindleOrchestrator | None = None
        self.lock: ProcessLock | None = None

    def start_daemon(self) -> None:
        """Start Spindle as a background daemon."""
        if daemon is None:
            raise RuntimeError("python-daemon package not installed")

        # Set up paths
        log_file_path = self.config.log_dir / "spindle.log"
        self.config.ensure_directories()

        # Check if already running
        process_info = ProcessLock.find_spindle_process()
        if process_info:
            pid, mode = process_info
            raise RuntimeError(f"Spindle is already running in {mode} mode (PID {pid})")

        logger.info("Starting Spindle daemon...")
        logger.info(f"Log file: {log_file_path}")
        logger.info(f"Monitoring: {self.config.optical_drive}")

        # Create daemon context
        daemon_context = daemon.DaemonContext(
            working_directory=Path.cwd(),
            umask=0o002,
        )

        with daemon_context:
            self._run_daemon(log_file_path)

    def start_systemd_mode(self) -> None:
        """Start for systemd (foreground with proper logging)."""
        self._run_daemon(None)  # systemd handles logging

    def _run_daemon(self, log_file_path: Path | None) -> None:
        """Run the actual daemon process."""
        if log_file_path:
            self._setup_daemon_logging(log_file_path)

        # Acquire the process lock
        self.lock = ProcessLock(self.config)
        if not self.lock.acquire():
            logger.error("Failed to acquire process lock - another instance may be running")
            sys.exit(1)

        # Create and start orchestrator
        self.orchestrator = SpindleOrchestrator(self.config)

        # Set up signal handlers
        def signal_handler(signum: int, frame: object) -> None:
            logger.info("Received signal %s, stopping daemon", signum)
            self.stop()
            sys.exit(0)

        signal.signal(signal.SIGTERM, signal_handler)
        signal.signal(signal.SIGINT, signal_handler)

        try:
            logger.info("Starting Spindle orchestrator")
            self.orchestrator.start()

            # Keep daemon alive
            while self.orchestrator.is_running:
                time.sleep(self.config.status_display_interval)

        except Exception as e:
            logger.exception("Error in daemon: %s", e)
            self.stop()
            sys.exit(1)
        finally:
            if self.lock:
                self.lock.release()

    def stop(self) -> None:
        """Stop the daemon."""
        if self.orchestrator:
            self.orchestrator.stop()
        if self.lock:
            self.lock.release()

    def _setup_daemon_logging(self, log_file_path: Path) -> None:
        """Set up logging for daemon mode."""
        logger = logging.getLogger()
        logger.setLevel(logging.INFO)

        # File handler
        file_handler = logging.FileHandler(log_file_path)
        file_handler.setLevel(logging.INFO)
        formatter = logging.Formatter(
            "%(asctime)s - %(name)s - %(levelname)s - %(message)s",
        )
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)
```

#### 2.2.2 `src/spindle/core/orchestrator.py`
**Source:** Refactor from `processor.py` with cleaner separation of concerns

**Full Implementation:**
```python
"""Main workflow orchestration for Spindle."""

import asyncio
import logging
from pathlib import Path

from ..config import SpindleConfig
from ..disc.monitor import DiscInfo, DiscMonitor, detect_disc
from ..storage.queue import QueueManager, QueueItem, QueueItemStatus
from ..components.disc_handler import DiscHandler
from ..components.encoder import EncoderComponent
from ..components.organizer import OrganizerComponent
from ..services.ntfy import NotificationService

logger = logging.getLogger(__name__)

class SpindleOrchestrator:
    """Orchestrates the complete Spindle workflow."""

    def __init__(self, config: SpindleConfig):
        self.config = config

        # Core components
        self.queue_manager = QueueManager(config)
        self.disc_handler = DiscHandler(config)
        self.encoder = EncoderComponent(config)
        self.organizer = OrganizerComponent(config)
        self.notifier = NotificationService(config)

        # Monitoring
        self.disc_monitor: DiscMonitor | None = None
        self.processing_task: asyncio.Task | None = None
        self.is_running = False

    def start(self) -> None:
        """Start the orchestrator."""
        if self.is_running:
            logger.warning("Orchestrator is already running")
            return

        logger.info("Starting Spindle orchestrator")
        self.is_running = True

        # Reset any stuck items from previous runs
        reset_count = self.queue_manager.reset_stuck_processing_items()
        if reset_count > 0:
            logger.info("Reset %s stuck items to pending status", reset_count)

        # Start disc monitoring
        self.disc_monitor = DiscMonitor(
            device=self.config.optical_drive,
            callback=self._on_disc_detected,
        )
        self.disc_monitor.start_monitoring()

        # Start background processing
        loop = asyncio.get_event_loop()
        self.processing_task = loop.create_task(self._process_queue_continuously())

        # Check for existing disc
        existing_disc = detect_disc(self.config.optical_drive)
        if existing_disc:
            logger.info("Found existing disc: %s", existing_disc)
            self._on_disc_detected(existing_disc)

        logger.info("Orchestrator started - ready for discs")

    def stop(self) -> None:
        """Stop the orchestrator."""
        if not self.is_running:
            return

        logger.info("Stopping orchestrator")
        self.is_running = False

        if self.disc_monitor:
            self.disc_monitor.stop_monitoring()

        if self.processing_task:
            self.processing_task.cancel()

        logger.info("Orchestrator stopped")

    def _on_disc_detected(self, disc_info: DiscInfo) -> None:
        """Handle disc detection."""
        logger.info("Detected disc: %s", disc_info)
        self.notifier.notify_disc_detected(disc_info.label, disc_info.disc_type)

        try:
            # Add to queue and start identification
            item = self.queue_manager.add_disc(disc_info.label)
            logger.info("Added to queue: %s", item)

            # Schedule identification task
            loop = asyncio.get_event_loop()
            task = loop.create_task(self.disc_handler.identify_disc(item, disc_info))

            def handle_completion(t):
                if not t.cancelled() and t.exception():
                    logger.exception("Disc identification failed", exc_info=t.exception())
                    self.notifier.notify_error(f"Failed to identify disc: {t.exception()}", context=disc_info.label)

            task.add_done_callback(handle_completion)

        except Exception as e:
            logger.exception("Error handling disc detection")
            self.notifier.notify_error(f"Failed to process disc: {e}", context=disc_info.label)

    async def _process_queue_continuously(self) -> None:
        """Continuously process queue items."""
        logger.info("Started background queue processor")

        while self.is_running:
            try:
                item = self._get_next_processable_item()

                if item:
                    await self._process_single_item(item)
                else:
                    await asyncio.sleep(self.config.queue_poll_interval)

            except asyncio.CancelledError:
                logger.info("Queue processor cancelled")
                break
            except Exception as e:
                logger.exception(f"Error in queue processor: {e}")
                await asyncio.sleep(self.config.error_retry_interval)

        logger.info("Background queue processor stopped")

    def _get_next_processable_item(self) -> QueueItem | None:
        """Get the next item that needs processing."""
        processable_statuses = [
            QueueItemStatus.IDENTIFIED,  # Ready for ripping
            QueueItemStatus.RIPPED,      # Ready for encoding
            QueueItemStatus.ENCODED,     # Ready for organization
        ]

        for status in processable_statuses:
            items = self.queue_manager.get_items_by_status(status)
            if items:
                return items[0]

        return None

    async def _process_single_item(self, item: QueueItem) -> None:
        """Process a single queue item through its next stage."""
        try:
            logger.info(f"Processing: {item}")

            if item.status == QueueItemStatus.IDENTIFIED:
                await self.disc_handler.rip_identified_item(item)
            elif item.status == QueueItemStatus.RIPPED:
                await self.encoder.encode_item(item)
            elif item.status == QueueItemStatus.ENCODED:
                await self.organizer.organize_item(item)

        except Exception as e:
            logger.exception(f"Error processing {item}: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)

    def get_status(self) -> dict:
        """Get current orchestrator status."""
        stats = self.queue_manager.get_queue_stats()

        current_disc_name = None
        current_disc = detect_disc(self.config.optical_drive)
        if current_disc:
            # Try to find identified name from processing items
            processing_items = []
            for status in [QueueItemStatus.PENDING, QueueItemStatus.IDENTIFYING,
                          QueueItemStatus.IDENTIFIED, QueueItemStatus.RIPPING]:
                processing_items.extend(self.queue_manager.get_items_by_status(status))

            for item in processing_items:
                if item.media_info and item.disc_title != current_disc.label:
                    current_disc_name = item.disc_title
                    break

            if not current_disc_name:
                current_disc_name = str(current_disc)

        return {
            "running": self.is_running,
            "current_disc": current_disc_name,
            "queue_stats": stats,
            "total_items": sum(stats.values()) if stats else 0,
        }
```

#### 2.2.3 `src/spindle/components/disc_handler.py`
**Source:** Extract disc processing logic from `processor.py`

**Full Implementation:**
```python
"""Disc processing coordination."""

import json
import logging
from typing import Any

from ..config import SpindleConfig
from ..disc.analyzer import IntelligentDiscAnalyzer, DiscAnalysisResult
from ..disc.monitor import DiscInfo, eject_disc
from ..disc.multi_disc import SimpleMultiDiscManager
from ..disc.ripper import MakeMKVRipper
from ..disc.rip_spec import RipSpec
from ..disc.tv_analyzer import TVSeriesDiscAnalyzer
from ..error_handling import MediaError, ToolError
from ..services.tmdb import TMDBService
from ..storage.queue import QueueItem, QueueItemStatus

logger = logging.getLogger(__name__)

class DiscHandler:
    """Coordinates disc identification and ripping operations."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.disc_analyzer = IntelligentDiscAnalyzer(config)
        self.tv_analyzer = TVSeriesDiscAnalyzer(config)
        self.multi_disc_manager = SimpleMultiDiscManager(config)
        self.ripper = MakeMKVRipper(config)
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
                raise MediaError("Failed to analyze disc content")

            logger.info(f"Analysis result: {analysis_result}")

            # Enhanced TV series detection
            item.progress_percent = 40
            item.progress_message = "Detecting content type"
            self.queue_manager.update_item(item)

            if analysis_result.content_type == "tv_series":
                logger.info("TV series detected, performing enhanced analysis")
                enhanced_result = await self.tv_analyzer.analyze_tv_disc(disc_info.device, analysis_result)
                if enhanced_result:
                    analysis_result = enhanced_result

            # TMDB identification
            item.progress_percent = 60
            item.progress_message = "Identifying via TMDB"
            self.queue_manager.update_item(item)

            media_info = await self.tmdb_service.identify_media(
                analysis_result.primary_title,
                analysis_result.content_type,
                year=analysis_result.year
            )

            if not media_info:
                logger.warning(f"Could not identify disc: {analysis_result.primary_title}")
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
                media_info, analysis_result
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
                raise MediaError("No rip specification data found")

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
                progress_callback=progress_callback
            )

            if not ripped_files:
                raise ToolError("MakeMKV ripping failed - no files produced")

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

        return RipSpec(
            disc_device=spec_data["disc_info"]["device"],
            titles_to_rip=spec_data["analysis_result"]["titles_to_rip"],
            output_directory=self.config.staging_dir / "ripped",
            media_info=item.media_info,
            episode_mappings=spec_data["analysis_result"]["episode_mappings"],
        )

    def set_queue_manager(self, queue_manager):
        """Inject queue manager dependency."""
        self.queue_manager = queue_manager
```

#### 2.2.4 `src/spindle/components/encoder.py`
**Source:** Extract encoding logic from `processor.py` and wrap `drapto_wrapper.py`

**Full Implementation:**
```python
"""Encoding component coordination."""

import logging
from pathlib import Path

from ..config import SpindleConfig
from ..error_handling import ToolError
from ..services.drapto import DraptoService
from ..storage.queue import QueueItem, QueueItemStatus

logger = logging.getLogger(__name__)

class EncoderComponent:
    """Coordinates video encoding operations."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.drapto_service = DraptoService(config)
        self.queue_manager = None  # Will be injected by orchestrator

    async def encode_item(self, item: QueueItem) -> None:
        """Encode a ripped item."""
        try:
            logger.info(f"Starting encoding for: {item}")

            if not item.ripped_file or not item.ripped_file.exists():
                raise ToolError(f"Ripped file not found: {item.ripped_file}")

            # Update status to encoding
            item.status = QueueItemStatus.ENCODING
            item.progress_stage = "Encoding video"
            item.progress_percent = 0
            self.queue_manager.update_item(item)

            # Determine output file path
            encoded_dir = self.config.staging_dir / "encoded"
            encoded_dir.mkdir(parents=True, exist_ok=True)

            # Generate output filename (replace extension)
            output_file = encoded_dir / f"{item.ripped_file.stem}_encoded.mkv"

            # Progress callback for encoding
            def progress_callback(stage: str, percent: int, message: str) -> None:
                item.progress_stage = stage
                item.progress_percent = percent
                item.progress_message = message
                self.queue_manager.update_item(item)

            # Perform encoding
            logger.info(f"Encoding {item.ripped_file} -> {output_file}")
            encode_result = await self.drapto_service.encode_file(
                input_file=item.ripped_file,
                output_file=output_file,
                progress_callback=progress_callback
            )

            if not encode_result.success:
                raise ToolError(f"Encoding failed: {encode_result.error_message}")

            # Store encoded file path
            item.encoded_file = output_file
            item.status = QueueItemStatus.ENCODED
            item.progress_stage = "Encoding completed"
            item.progress_percent = 100
            item.progress_message = f"Encoded to {output_file.name}"

            self.queue_manager.update_item(item)
            logger.info(f"Successfully encoded: {output_file}")

        except Exception as e:
            logger.exception(f"Error encoding item: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            raise

    def set_queue_manager(self, queue_manager):
        """Inject queue manager dependency."""
        self.queue_manager = queue_manager
```

#### 2.2.5 `src/spindle/components/organizer.py`
**Source:** Extract organization logic from `processor.py` and wrap `library.py`

**Full Implementation:**
```python
"""Library organization component coordination."""

import logging
from pathlib import Path

from ..config import SpindleConfig
from ..error_handling import ToolError
from ..services.plex import PlexService
from ..storage.queue import QueueItem, QueueItemStatus

logger = logging.getLogger(__name__)

class OrganizerComponent:
    """Coordinates library organization and Plex integration."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.plex_service = PlexService(config)
        self.queue_manager = None  # Will be injected by orchestrator

    async def organize_item(self, item: QueueItem) -> None:
        """Organize an encoded item into the library."""
        try:
            logger.info(f"Starting organization for: {item}")

            if not item.encoded_file or not item.encoded_file.exists():
                raise ToolError(f"Encoded file not found: {item.encoded_file}")

            # Update status to organizing
            item.status = QueueItemStatus.ORGANIZING
            item.progress_stage = "Organizing library"
            item.progress_percent = 0
            self.queue_manager.update_item(item)

            # Determine library organization structure
            if not item.media_info:
                raise ToolError("No media info available for organization")

            item.progress_percent = 20
            item.progress_message = "Creating library structure"
            self.queue_manager.update_item(item)

            # Organize into library
            final_file_path = await self.plex_service.organize_media(
                source_file=item.encoded_file,
                media_info=item.media_info,
                progress_callback=self._create_progress_callback(item)
            )

            # Store final file path
            item.final_file = final_file_path

            # Trigger Plex library scan
            item.progress_percent = 80
            item.progress_message = "Updating Plex library"
            self.queue_manager.update_item(item)

            await self.plex_service.refresh_library(item.media_info.content_type)

            # Mark as completed
            item.status = QueueItemStatus.COMPLETED
            item.progress_stage = "Organization completed"
            item.progress_percent = 100
            item.progress_message = f"Available in library: {final_file_path.name}"

            self.queue_manager.update_item(item)
            logger.info(f"Successfully organized: {final_file_path}")

        except Exception as e:
            logger.exception(f"Error organizing item: {e}")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            self.queue_manager.update_item(item)
            raise

    def _create_progress_callback(self, item: QueueItem):
        """Create progress callback for organization operations."""
        def progress_callback(stage: str, percent: int, message: str) -> None:
            # Scale progress to 20-80% range (organization portion)
            scaled_percent = 20 + int(percent * 0.6)
            item.progress_stage = stage
            item.progress_percent = scaled_percent
            item.progress_message = message
            self.queue_manager.update_item(item)

        return progress_callback

    def set_queue_manager(self, queue_manager):
        """Inject queue manager dependency."""
        self.queue_manager = queue_manager
```

#### 2.2.6 Integration Validation

**Scope:** Validate that core orchestrator works with implemented components.

**Integration Tests:**
```bash
# Test orchestrator can be instantiated with all components
uv run python -c "
from src.spindle.core.orchestrator import SpindleOrchestrator
from src.spindle.config import SpindleConfig
import tempfile
from pathlib import Path

# Test full integration
with tempfile.TemporaryDirectory() as tmp_dir:
    config = SpindleConfig(
        log_dir=Path(tmp_dir) / 'logs',
        staging_dir=Path(tmp_dir) / 'staging',
        library_dir=Path(tmp_dir) / 'library'
    )

    # Should create orchestrator with all components
    orchestrator = SpindleOrchestrator(config)
    print('✅ Orchestrator integration: OK')

    # Test status method works
    status = orchestrator.get_status()
    assert 'running' in status
    print('✅ Status method: OK')

    # Test component dependency injection
    assert orchestrator.queue_manager is not None
    assert orchestrator.disc_handler is not None
    assert orchestrator.encoder is not None
    assert orchestrator.organizer is not None
    assert orchestrator.notifier is not None
    print('✅ Component injection: OK')
"
```

**Functional Validation:**
```bash
# Test daemon can start orchestrator (integration test)
uv run python -c "
from src.spindle.core.daemon import SpindleDaemon
from src.spindle.config import SpindleConfig
import tempfile
from pathlib import Path

# Test daemon -> orchestrator integration
with tempfile.TemporaryDirectory() as tmp_dir:
    config = SpindleConfig(
        log_dir=Path(tmp_dir) / 'logs',
        staging_dir=Path(tmp_dir) / 'staging',
        library_dir=Path(tmp_dir) / 'library'
    )

    # Should create daemon with orchestrator
    daemon = SpindleDaemon(config)
    print('✅ Daemon -> Orchestrator integration: OK')
"
```

**Component Interface Validation:**
- Verify all components have required methods that orchestrator calls
- Test that dependency injection works properly
- Confirm async methods are properly awaitable
- Validate error handling propagates correctly

**Success Criteria:**
- ✅ SpindleOrchestrator instantiates with all components
- ✅ All component interfaces match orchestrator expectations
- ✅ SpindleDaemon can create and manage orchestrator
- ✅ Status reporting works without errors
- ✅ No import errors or missing dependencies

### 2.3 Service Module Implementations

**Scope:** Create complete service wrapper implementations with clean interfaces.

**Migration Strategy:**
1. Move existing service files to new locations with renamed classes
2. Preserve all existing functionality
3. Add consistent interfaces and error handling
4. Update all imports to use new service modules

#### 2.3.1 `src/spindle/services/tmdb.py`
**Source:** Move and refactor from `identify/tmdb.py`

**Full Implementation:**
```python
"""TMDB API integration service."""

import logging
from typing import Any

from ..config import SpindleConfig
from ..identify.tmdb import MediaIdentifier, MediaInfo

logger = logging.getLogger(__name__)

class TMDBService:
    """Clean wrapper for TMDB media identification."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.identifier = MediaIdentifier(config)

    async def identify_media(
        self,
        title: str,
        content_type: str = "movie",
        year: int | None = None
    ) -> MediaInfo | None:
        """Identify media via TMDB API."""
        try:
            logger.info(f"Identifying {content_type}: {title} ({year})")

            if content_type == "movie":
                return await self.identifier.identify_movie(title, year)
            elif content_type == "tv_series":
                return await self.identifier.identify_tv_series(title, year)
            else:
                logger.warning(f"Unknown content type: {content_type}")
                return None

        except Exception as e:
            logger.exception(f"TMDB identification failed: {e}")
            return None

    async def search_movies(self, query: str, year: int | None = None) -> list[dict]:
        """Search for movies via TMDB."""
        return await self.identifier.search_movies(query, year)

    async def search_tv_series(self, query: str, year: int | None = None) -> list[dict]:
        """Search for TV series via TMDB."""
        return await self.identifier.search_tv_series(query, year)

    def get_cache_stats(self) -> dict[str, int]:
        """Get TMDB cache statistics."""
        return self.identifier.cache.get_stats()
```

#### 2.3.2 `src/spindle/services/drapto.py`
**Source:** Move and refactor from `encode/drapto_wrapper.py`

**Full Implementation:**
```python
"""Drapto encoding service wrapper."""

import logging
from collections.abc import Callable
from pathlib import Path

from ..config import SpindleConfig
from ..encode.drapto_wrapper import DraptoEncoder, EncodeResult

logger = logging.getLogger(__name__)

class DraptoService:
    """Clean wrapper for Drapto AV1 encoding."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.encoder = DraptoEncoder(config)

    async def encode_file(
        self,
        input_file: Path,
        output_file: Path,
        progress_callback: Callable[[str, int, str], None] | None = None
    ) -> EncodeResult:
        """Encode video file with AV1."""
        try:
            logger.info(f"Starting encoding: {input_file} -> {output_file}")

            # Validate input
            if not input_file.exists():
                raise FileNotFoundError(f"Input file not found: {input_file}")

            # Ensure output directory exists
            output_file.parent.mkdir(parents=True, exist_ok=True)

            # Perform encoding
            result = await self.encoder.encode_file(
                input_file=input_file,
                output_file=output_file,
                progress_callback=progress_callback
            )

            if result.success:
                logger.info(f"Encoding completed: {output_file}")
            else:
                logger.error(f"Encoding failed: {result.error_message}")

            return result

        except Exception as e:
            logger.exception(f"Encoding service error: {e}")
            return EncodeResult(
                success=False,
                input_file=input_file,
                output_file=output_file,
                error_message=str(e)
            )

    def validate_drapto_available(self) -> bool:
        """Check if drapto is available and working."""
        return self.encoder.validate_drapto_installation()

    def get_encoder_info(self) -> dict[str, Any]:
        """Get information about the encoder."""
        return {
            "drapto_available": self.validate_drapto_available(),
            "config": {
                "quality": self.config.drapto_quality,
                "preset": self.config.drapto_preset,
            }
        }
```

#### 2.3.3 `src/spindle/services/plex.py`
**Source:** Move and refactor from `organize/library.py`

**Full Implementation:**
```python
"""Plex media server integration service."""

import logging
from collections.abc import Callable
from pathlib import Path

from ..config import SpindleConfig
from ..identify.tmdb import MediaInfo
from ..organize.library import LibraryOrganizer

logger = logging.getLogger(__name__)

class PlexService:
    """Clean wrapper for Plex library management."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.organizer = LibraryOrganizer(config)

    async def organize_media(
        self,
        source_file: Path,
        media_info: MediaInfo,
        progress_callback: Callable[[str, int, str], None] | None = None
    ) -> Path:
        """Organize media file into Plex library structure."""
        try:
            logger.info(f"Organizing media: {source_file} -> {media_info.title}")

            if progress_callback:
                progress_callback("Organizing", 10, "Determining library path")

            # Organize into appropriate library structure
            final_path = await self.organizer.organize_media_file(
                source_file=source_file,
                media_info=media_info,
                progress_callback=progress_callback
            )

            logger.info(f"Media organized to: {final_path}")
            return final_path

        except Exception as e:
            logger.exception(f"Library organization failed: {e}")
            raise

    async def refresh_library(self, content_type: str) -> None:
        """Trigger Plex library refresh for content type."""
        try:
            logger.info(f"Refreshing Plex library for: {content_type}")

            if content_type == "movie":
                await self.organizer.refresh_movie_library()
            elif content_type == "tv_series":
                await self.organizer.refresh_tv_library()
            else:
                logger.warning(f"Unknown content type for refresh: {content_type}")

        except Exception as e:
            logger.warning(f"Plex library refresh failed: {e}")
            # Don't raise - library refresh is not critical

    def test_connection(self) -> bool:
        """Test Plex server connection."""
        return self.organizer.test_plex_connection()

    def get_library_stats(self) -> dict[str, Any]:
        """Get Plex library statistics."""
        return self.organizer.get_library_info()
```

#### 2.3.4 `src/spindle/services/ntfy.py`
**Source:** Move and refactor from `notify/ntfy.py`

**Full Implementation:**
```python
"""Notification service via ntfy.sh."""

import logging
from typing import Any

from ..config import SpindleConfig
from ..notify.ntfy import NtfyNotifier

logger = logging.getLogger(__name__)

class NotificationService:
    """Clean wrapper for ntfy.sh notifications."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.notifier = NtfyNotifier(config)

    def notify_disc_detected(self, disc_label: str, disc_type: str) -> None:
        """Send notification when disc is detected."""
        try:
            message = f"📀 Disc detected: {disc_label} ({disc_type})"
            self.notifier.send_notification(
                title="Spindle - Disc Detected",
                message=message,
                priority="default"
            )
        except Exception as e:
            logger.warning(f"Failed to send disc detection notification: {e}")

    def notify_identification_complete(self, title: str, media_type: str) -> None:
        """Send notification when identification is complete."""
        try:
            message = f"🎬 Identified: {title} ({media_type})"
            self.notifier.send_notification(
                title="Spindle - Identified",
                message=message,
                priority="default"
            )
        except Exception as e:
            logger.warning(f"Failed to send identification notification: {e}")

    def notify_rip_complete(self, title: str) -> None:
        """Send notification when ripping is complete."""
        try:
            message = f"💿 Rip complete: {title}"
            self.notifier.send_notification(
                title="Spindle - Rip Complete",
                message=message,
                priority="default"
            )
        except Exception as e:
            logger.warning(f"Failed to send rip notification: {e}")

    def notify_encode_complete(self, title: str) -> None:
        """Send notification when encoding is complete."""
        try:
            message = f"🎞️ Encoding complete: {title}"
            self.notifier.send_notification(
                title="Spindle - Encoded",
                message=message,
                priority="default"
            )
        except Exception as e:
            logger.warning(f"Failed to send encoding notification: {e}")

    def notify_processing_complete(self, title: str) -> None:
        """Send notification when full processing is complete."""
        try:
            message = f"✅ Ready to watch: {title}"
            self.notifier.send_notification(
                title="Spindle - Complete",
                message=message,
                priority="high"
            )
        except Exception as e:
            logger.warning(f"Failed to send completion notification: {e}")

    def notify_error(self, error_message: str, context: str | None = None) -> None:
        """Send error notification."""
        try:
            title = "Spindle - Error"
            if context:
                message = f"❌ Error with {context}: {error_message}"
            else:
                message = f"❌ Error: {error_message}"

            self.notifier.send_notification(
                title=title,
                message=message,
                priority="high"
            )
        except Exception as e:
            logger.error(f"Failed to send error notification: {e}")

    def test_notifications(self) -> bool:
        """Test notification system."""
        try:
            self.notifier.send_notification(
                title="Spindle - Test",
                message="🧪 Notification system test",
                priority="low"
            )
            return True
        except Exception as e:
            logger.error(f"Notification test failed: {e}")
            return False
```

#### 2.3.5 `src/spindle/services/makemkv.py`
**Source:** Extract MakeMKV integration from `disc/ripper.py`

**Full Implementation:**
```python
"""MakeMKV service wrapper."""

import logging
import subprocess
from collections.abc import Callable
from pathlib import Path

from ..config import SpindleConfig
from ..disc.ripper import MakeMKVRipper

logger = logging.getLogger(__name__)

class MakeMKVService:
    """Clean wrapper for MakeMKV disc ripping."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.ripper = MakeMKVRipper(config)

    async def scan_disc(self, device: str) -> dict[str, Any]:
        """Scan disc and return title information."""
        try:
            logger.info(f"Scanning disc: {device}")

            scan_result = await self.ripper.scan_disc(device)

            if not scan_result:
                raise RuntimeError("MakeMKV disc scan failed")

            return {
                "device": device,
                "titles": scan_result.get("titles", []),
                "disc_info": scan_result.get("disc_info", {}),
            }

        except Exception as e:
            logger.exception(f"Disc scan failed: {e}")
            raise

    async def rip_titles(
        self,
        device: str,
        titles: list[dict],
        output_directory: Path,
        progress_callback: Callable[[str, int, str], None] | None = None
    ) -> list[Path]:
        """Rip specified titles from disc."""
        try:
            logger.info(f"Ripping {len(titles)} titles from {device}")

            # Ensure output directory exists
            output_directory.mkdir(parents=True, exist_ok=True)

            ripped_files = await self.ripper.rip_titles(
                device=device,
                titles=titles,
                output_directory=output_directory,
                progress_callback=progress_callback
            )

            logger.info(f"Ripped {len(ripped_files)} files")
            return ripped_files

        except Exception as e:
            logger.exception(f"Title ripping failed: {e}")
            raise

    def validate_makemkv_available(self) -> bool:
        """Check if MakeMKV is available and licensed."""
        try:
            result = subprocess.run(
                ["makemkvcon", "--version"],
                capture_output=True,
                text=True,
                timeout=10
            )
            return result.returncode == 0
        except (subprocess.SubprocessError, FileNotFoundError):
            return False

    def get_disc_info(self, device: str) -> dict[str, Any] | None:
        """Get basic disc information."""
        try:
            return self.ripper.get_disc_info(device)
        except Exception as e:
            logger.warning(f"Failed to get disc info: {e}")
            return None
```

### 2.4 Storage Module Implementations

**Scope:** Consolidate all data persistence and caching into unified storage layer.

#### 2.4.1 `src/spindle/storage/queue.py`
**Source:** Move and refactor from `queue/manager.py`

**Full Implementation:**
```python
"""Queue management for batch processing."""

import json
import logging
import sqlite3
from collections.abc import Iterator
from contextlib import contextmanager
from datetime import UTC, datetime
from enum import Enum
from pathlib import Path
from typing import Any

from ..config import SpindleConfig
from ..identify.tmdb import MediaInfo

logger = logging.getLogger(__name__)

class QueueItemStatus(Enum):
    """Status of items in the processing queue."""

    PENDING = "pending"
    IDENTIFYING = "identifying"
    IDENTIFIED = "identified"
    RIPPING = "ripping"
    RIPPED = "ripped"
    ENCODING = "encoding"
    ENCODED = "encoded"
    ORGANIZING = "organizing"
    COMPLETED = "completed"
    FAILED = "failed"
    REVIEW = "review"

class QueueItem:
    """Represents an item in the processing queue."""

    def __init__(
        self,
        item_id: int | None = None,
        source_path: Path | None = None,
        disc_title: str | None = None,
        status: QueueItemStatus = QueueItemStatus.PENDING,
        media_info: MediaInfo | None = None,
        ripped_file: Path | None = None,
        encoded_file: Path | None = None,
        final_file: Path | None = None,
        error_message: str | None = None,
        created_at: datetime | None = None,
        updated_at: datetime | None = None,
        progress_stage: str | None = None,
        progress_percent: int = 0,
        progress_message: str | None = None,
        rip_spec_data: str | None = None,
    ):
        self.id = item_id
        self.source_path = source_path
        self.disc_title = disc_title
        self.status = status
        self.media_info = media_info
        self.ripped_file = ripped_file
        self.encoded_file = encoded_file
        self.final_file = final_file
        self.error_message = error_message
        self.created_at = created_at or datetime.now(UTC)
        self.updated_at = updated_at or datetime.now(UTC)
        self.progress_stage = progress_stage
        self.progress_percent = progress_percent
        self.progress_message = progress_message
        self.rip_spec_data = rip_spec_data

    def __str__(self) -> str:
        title = self.disc_title or "Unknown"
        return f"QueueItem({self.id}: {title} - {self.status.value})"

class QueueManager:
    """Manages the processing queue with SQLite backend."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.db_path = config.log_dir / "queue.db"
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        self._initialize_database()

    def _initialize_database(self) -> None:
        """Initialize database and apply migrations."""
        with self._get_connection() as conn:
            # Create main table
            conn.execute("""
                CREATE TABLE IF NOT EXISTS queue_items (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    source_path TEXT,
                    disc_title TEXT,
                    status TEXT NOT NULL DEFAULT 'pending',
                    media_info_json TEXT,
                    ripped_file TEXT,
                    encoded_file TEXT,
                    final_file TEXT,
                    error_message TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    progress_stage TEXT,
                    progress_percent INTEGER DEFAULT 0,
                    progress_message TEXT,
                    rip_spec_data TEXT
                )
            """)

            # Create schema version table
            conn.execute("""
                CREATE TABLE IF NOT EXISTS schema_version (
                    version INTEGER PRIMARY KEY
                )
            """)

            self._apply_migrations(conn)

    def _apply_migrations(self, conn: sqlite3.Connection) -> None:
        """Apply database migrations."""
        current_version = self._get_schema_version(conn)

        migrations = [
            self._migration_001_add_progress_fields,
            self._migration_002_add_rip_spec_data,
        ]

        for i, migration in enumerate(migrations, start=1):
            if current_version < i:
                logger.info(f"Applying migration {i}")
                migration(conn)
                self._set_schema_version(conn, i)

    def _get_schema_version(self, conn: sqlite3.Connection) -> int:
        """Get current schema version."""
        try:
            cursor = conn.execute("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1")
            result = cursor.fetchone()
            return result[0] if result else 0
        except sqlite3.OperationalError:
            return 0

    def _set_schema_version(self, conn: sqlite3.Connection, version: int) -> None:
        """Set schema version."""
        conn.execute("INSERT OR REPLACE INTO schema_version (version) VALUES (?)", (version,))

    def _migration_001_add_progress_fields(self, conn: sqlite3.Connection) -> None:
        """Add progress tracking fields."""
        try:
            conn.execute("ALTER TABLE queue_items ADD COLUMN progress_stage TEXT")
            conn.execute("ALTER TABLE queue_items ADD COLUMN progress_percent INTEGER DEFAULT 0")
            conn.execute("ALTER TABLE queue_items ADD COLUMN progress_message TEXT")
        except sqlite3.OperationalError as e:
            if "duplicate column name" not in str(e).lower():
                raise

    def _migration_002_add_rip_spec_data(self, conn: sqlite3.Connection) -> None:
        """Add rip specification data field."""
        try:
            conn.execute("ALTER TABLE queue_items ADD COLUMN rip_spec_data TEXT")
        except sqlite3.OperationalError as e:
            if "duplicate column name" not in str(e).lower():
                raise

    @contextmanager
    def _get_connection(self) -> Iterator[sqlite3.Connection]:
        """Get database connection with proper cleanup."""
        conn = sqlite3.connect(self.db_path, timeout=30)
        conn.row_factory = sqlite3.Row
        try:
            yield conn
            conn.commit()
        except Exception:
            conn.rollback()
            raise
        finally:
            conn.close()

    def add_disc(self, disc_title: str, source_path: Path | None = None) -> QueueItem:
        """Add a new disc to the processing queue."""
        item = QueueItem(
            source_path=source_path,
            disc_title=disc_title,
            status=QueueItemStatus.PENDING,
        )

        with self._get_connection() as conn:
            cursor = conn.execute("""
                INSERT INTO queue_items (
                    source_path, disc_title, status, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?)
            """, (
                str(source_path) if source_path else None,
                disc_title,
                item.status.value,
                item.created_at,
                item.updated_at,
            ))

            item.id = cursor.lastrowid

        logger.info(f"Added to queue: {item}")
        return item

    def update_item(self, item: QueueItem) -> None:
        """Update an existing queue item."""
        item.updated_at = datetime.now(UTC)

        with self._get_connection() as conn:
            conn.execute("""
                UPDATE queue_items SET
                    status = ?,
                    media_info_json = ?,
                    ripped_file = ?,
                    encoded_file = ?,
                    final_file = ?,
                    error_message = ?,
                    updated_at = ?,
                    progress_stage = ?,
                    progress_percent = ?,
                    progress_message = ?,
                    rip_spec_data = ?
                WHERE id = ?
            """, (
                item.status.value,
                json.dumps(item.media_info.to_dict()) if item.media_info else None,
                str(item.ripped_file) if item.ripped_file else None,
                str(item.encoded_file) if item.encoded_file else None,
                str(item.final_file) if item.final_file else None,
                item.error_message,
                item.updated_at,
                item.progress_stage,
                item.progress_percent,
                item.progress_message,
                item.rip_spec_data,
                item.id,
            ))

    def get_items_by_status(self, status: QueueItemStatus) -> list[QueueItem]:
        """Get all items with the specified status."""
        with self._get_connection() as conn:
            cursor = conn.execute(
                "SELECT * FROM queue_items WHERE status = ? ORDER BY created_at",
                (status.value,)
            )
            return [self._row_to_item(row) for row in cursor.fetchall()]

    def get_all_items(self) -> list[QueueItem]:
        """Get all queue items."""
        with self._get_connection() as conn:
            cursor = conn.execute("SELECT * FROM queue_items ORDER BY created_at")
            return [self._row_to_item(row) for row in cursor.fetchall()]

    def get_queue_stats(self) -> dict[str, int]:
        """Get queue statistics by status."""
        with self._get_connection() as conn:
            cursor = conn.execute("""
                SELECT status, COUNT(*) as count
                FROM queue_items
                GROUP BY status
            """)
            return {row["status"]: row["count"] for row in cursor.fetchall()}

    def reset_stuck_processing_items(self) -> int:
        """Reset stuck processing items to pending."""
        stuck_statuses = [
            QueueItemStatus.IDENTIFYING.value,
            QueueItemStatus.RIPPING.value,
            QueueItemStatus.ENCODING.value,
            QueueItemStatus.ORGANIZING.value,
        ]

        with self._get_connection() as conn:
            cursor = conn.execute("""
                UPDATE queue_items
                SET status = ?, updated_at = ?
                WHERE status IN ({})
            """.format(",".join("?" * len(stuck_statuses))),
            [QueueItemStatus.PENDING.value, datetime.now(UTC)] + stuck_statuses)

            return cursor.rowcount

    def _row_to_item(self, row: sqlite3.Row) -> QueueItem:
        """Convert database row to QueueItem."""
        media_info = None
        if row["media_info_json"]:
            media_info_data = json.loads(row["media_info_json"])
            media_info = MediaInfo.from_dict(media_info_data)

        return QueueItem(
            item_id=row["id"],
            source_path=Path(row["source_path"]) if row["source_path"] else None,
            disc_title=row["disc_title"],
            status=QueueItemStatus(row["status"]),
            media_info=media_info,
            ripped_file=Path(row["ripped_file"]) if row["ripped_file"] else None,
            encoded_file=Path(row["encoded_file"]) if row["encoded_file"] else None,
            final_file=Path(row["final_file"]) if row["final_file"] else None,
            error_message=row["error_message"],
            created_at=datetime.fromisoformat(row["created_at"]),
            updated_at=datetime.fromisoformat(row["updated_at"]),
            progress_stage=row.get("progress_stage"),
            progress_percent=row.get("progress_percent", 0),
            progress_message=row.get("progress_message"),
            rip_spec_data=row.get("rip_spec_data"),
        )
```

#### 2.4.2 `src/spindle/storage/cache.py`
**Source:** Consolidate caching from multiple modules

**Full Implementation:**
```python
"""Unified caching system for Spindle."""

import logging
from typing import Any

from ..config import SpindleConfig
from ..disc.series_cache import SeriesCache
from ..identify.tmdb_cache import TMDBCache

logger = logging.getLogger(__name__)

class SpindleCache:
    """Unified cache management for all Spindle subsystems."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.tmdb = TMDBCache(config)
        self.series = SeriesCache(config)

    def clear_all(self) -> None:
        """Clear all caches."""
        logger.info("Clearing all caches")
        try:
            self.tmdb.clear()
            self.series.clear()
            logger.info("All caches cleared successfully")
        except Exception as e:
            logger.error(f"Error clearing caches: {e}")
            raise

    def get_cache_stats(self) -> dict[str, Any]:
        """Get statistics for all caches."""
        return {
            "tmdb": self.tmdb.get_stats(),
            "series": self.series.get_stats(),
        }

    def cleanup_expired(self) -> None:
        """Remove expired entries from all caches."""
        logger.info("Cleaning up expired cache entries")
        try:
            tmdb_removed = self.tmdb.cleanup_expired()
            series_removed = self.series.cleanup_expired()

            logger.info(f"Cleanup complete: {tmdb_removed} TMDB entries, {series_removed} series entries removed")
        except Exception as e:
            logger.error(f"Error during cache cleanup: {e}")
            raise

    def get_total_size(self) -> dict[str, int]:
        """Get total size information for all caches."""
        return {
            "tmdb_entries": self.tmdb.get_entry_count(),
            "series_entries": self.series.get_entry_count(),
            "total_size_bytes": self.tmdb.get_size_bytes() + self.series.get_size_bytes(),
        }

    def validate_integrity(self) -> dict[str, bool]:
        """Validate integrity of all caches."""
        return {
            "tmdb_valid": self.tmdb.validate_integrity(),
            "series_valid": self.series.validate_integrity(),
        }
```

### 2.5 CLI Simplification Implementation

**Scope:** Simplify CLI to use new core modules with clean delegation.

**Full Implementation:**
```python
"""Update src/spindle/cli.py to use new architecture."""

# Remove old imports
# from .processor import ContinuousProcessor  # DELETE
# from .queue.manager import QueueManager     # DELETE

# Add new imports
from .core.daemon import SpindleDaemon
from .core.orchestrator import SpindleOrchestrator
from .storage.queue import QueueManager  # Updated import path

@cli.command()
@click.option("--systemd", is_flag=True, help="Running under systemd")
@click.pass_context
def start(ctx: click.Context, systemd: bool) -> None:
    """Start continuous processing daemon."""
    config: SpindleConfig = ctx.obj["config"]

    daemon = SpindleDaemon(config)

    if systemd or os.getenv("INVOCATION_ID"):
        daemon.start_systemd_mode()
    else:
        daemon.start_daemon()

@cli.command()
@click.pass_context
def status(ctx: click.Context) -> None:
    """Show current daemon status."""
    config: SpindleConfig = ctx.obj["config"]

    # Check if daemon is running
    process_info = ProcessLock.find_spindle_process()
    if not process_info:
        console.print("[red]Spindle is not running[/red]")
        sys.exit(1)

    pid, mode = process_info
    console.print(f"[green]Spindle is running[/green] (PID {pid}, mode: {mode})")

    # Get orchestrator status via new architecture
    try:
        # Create orchestrator instance to get status
        orchestrator = SpindleOrchestrator(config)
        status_info = orchestrator.get_status()

        console.print("\n[bold]Status:[/bold]")
        console.print(f"  Running: {status_info['running']}")
        console.print(f"  Current disc: {status_info['current_disc'] or 'None'}")
        console.print(f"  Total queue items: {status_info['total_items']}")

        if status_info['queue_stats']:
            console.print("\n[bold]Queue:[/bold]")
            for status, count in status_info['queue_stats'].items():
                console.print(f"  {status}: {count}")

    except Exception as e:
        console.print(f"[yellow]Could not get detailed status: {e}[/yellow]")

@cli.command()
@click.pass_context
def stop(ctx: click.Context) -> None:
    """Stop the daemon."""
    config: SpindleConfig = ctx.obj["config"]

    # Check if running
    process_info = ProcessLock.find_spindle_process()
    if not process_info:
        console.print("Spindle is not running")
        return

    pid, mode = process_info

    try:
        # Send termination signal
        os.kill(pid, signal.SIGTERM)

        # Wait for graceful shutdown
        for _ in range(30):  # Wait up to 30 seconds
            if not ProcessLock.find_spindle_process():
                console.print("Spindle stopped")
                return
            time.sleep(1)

        # Force kill if still running
        console.print("[yellow]Forcing shutdown...[/yellow]")
        os.kill(pid, signal.SIGKILL)
        console.print("Spindle stopped")

    except ProcessLookupError:
        console.print("Spindle stopped")
    except PermissionError:
        console.print("[red]Permission denied - cannot stop Spindle[/red]")
        sys.exit(1)
    except Exception as e:
        console.print(f"[red]Error stopping Spindle: {e}[/red]")
        sys.exit(1)

# show command remains unchanged (already using new architecture)
```

### 2.6 Testing Implementation for Phase 2

**Scope:** Create comprehensive tests for new architecture following CLAUDE.md testing philosophy.

#### 2.6.1 Core Module Tests

**`tests/core/test_daemon.py`:**
```python
"""Test daemon lifecycle management."""

import pytest
from unittest.mock import Mock, patch
from pathlib import Path

from spindle.core.daemon import SpindleDaemon
from spindle.config import SpindleConfig


class TestSpindleDaemon:
    """Test SpindleDaemon functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(
            log_dir=tmp_path / "logs",
            staging_dir=tmp_path / "staging",
            library_dir=tmp_path / "library",
        )

    @pytest.fixture
    def daemon(self, config):
        """Create daemon instance."""
        return SpindleDaemon(config)

    def test_daemon_initialization(self, daemon, config):
        """Test daemon initializes correctly."""
        assert daemon.config == config
        assert daemon.orchestrator is None
        assert daemon.lock is None

    @patch('spindle.core.daemon.daemon')
    @patch('spindle.core.daemon.ProcessLock')
    def test_start_daemon_creates_context(self, mock_lock, mock_daemon_module, daemon):
        """Test daemon creates proper daemon context."""
        mock_lock.find_spindle_process.return_value = None
        mock_process_lock = Mock()
        mock_process_lock.acquire.return_value = True
        mock_lock.return_value = mock_process_lock

        mock_daemon_context = Mock()
        mock_daemon_module.DaemonContext.return_value = mock_daemon_context

        with patch.object(daemon, '_run_daemon') as mock_run:
            daemon.start_daemon()

        mock_daemon_module.DaemonContext.assert_called_once()
        mock_daemon_context.__enter__.assert_called_once()

    @patch('spindle.core.daemon.ProcessLock')
    def test_start_systemd_mode(self, mock_lock, daemon):
        """Test systemd mode starts without daemon context."""
        mock_process_lock = Mock()
        mock_process_lock.acquire.return_value = True
        mock_lock.return_value = mock_process_lock

        with patch.object(daemon, '_run_daemon') as mock_run:
            daemon.start_systemd_mode()

        mock_run.assert_called_once_with(None)

    def test_stop_cleans_up_resources(self, daemon):
        """Test stop method cleans up orchestrator and lock."""
        mock_orchestrator = Mock()
        mock_lock = Mock()
        daemon.orchestrator = mock_orchestrator
        daemon.lock = mock_lock

        daemon.stop()

        mock_orchestrator.stop.assert_called_once()
        mock_lock.release.assert_called_once()
```

**`tests/core/test_orchestrator.py`:**
```python
"""Test workflow orchestration."""

import pytest
from unittest.mock import Mock, AsyncMock, patch

from spindle.core.orchestrator import SpindleOrchestrator
from spindle.config import SpindleConfig


class TestSpindleOrchestrator:
    """Test SpindleOrchestrator functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(
            log_dir=tmp_path / "logs",
            staging_dir=tmp_path / "staging",
            library_dir=tmp_path / "library",
        )

    @pytest.fixture
    def orchestrator(self, config):
        """Create orchestrator instance."""
        with patch('spindle.core.orchestrator.QueueManager'), \
             patch('spindle.core.orchestrator.DiscHandler'), \
             patch('spindle.core.orchestrator.EncoderComponent'), \
             patch('spindle.core.orchestrator.OrganizerComponent'), \
             patch('spindle.core.orchestrator.NotificationService'):
            return SpindleOrchestrator(config)

    def test_orchestrator_initialization(self, orchestrator, config):
        """Test orchestrator initializes with all components."""
        assert orchestrator.config == config
        assert orchestrator.queue_manager is not None
        assert orchestrator.disc_handler is not None
        assert orchestrator.encoder is not None
        assert orchestrator.organizer is not None
        assert orchestrator.notifier is not None
        assert not orchestrator.is_running

    @patch('spindle.core.orchestrator.DiscMonitor')
    @patch('spindle.core.orchestrator.detect_disc')
    def test_start_initializes_monitoring(self, mock_detect, mock_monitor, orchestrator):
        """Test start method initializes disc monitoring."""
        mock_detect.return_value = None
        orchestrator.queue_manager.reset_stuck_processing_items.return_value = 0

        with patch('asyncio.get_event_loop') as mock_loop:
            mock_loop.return_value.create_task.return_value = Mock()
            orchestrator.start()

        assert orchestrator.is_running
        mock_monitor.assert_called_once()

    def test_stop_cleans_up_monitoring(self, orchestrator):
        """Test stop method cleans up resources."""
        mock_monitor = Mock()
        mock_task = Mock()
        orchestrator.disc_monitor = mock_monitor
        orchestrator.processing_task = mock_task
        orchestrator.is_running = True

        orchestrator.stop()

        assert not orchestrator.is_running
        mock_monitor.stop_monitoring.assert_called_once()
        mock_task.cancel.assert_called_once()

    def test_get_status_returns_comprehensive_info(self, orchestrator):
        """Test get_status returns complete status information."""
        orchestrator.queue_manager.get_queue_stats.return_value = {"pending": 2, "completed": 1}

        with patch('spindle.core.orchestrator.detect_disc') as mock_detect:
            mock_detect.return_value = None
            status = orchestrator.get_status()

        assert "running" in status
        assert "current_disc" in status
        assert "queue_stats" in status
        assert "total_items" in status
        assert status["total_items"] == 3
```

#### 2.6.2 Component Tests

**`tests/components/test_disc_handler.py`:**
```python
"""Test disc processing coordination."""

import pytest
from unittest.mock import Mock, AsyncMock, patch

from spindle.components.disc_handler import DiscHandler
from spindle.storage.queue import QueueItem, QueueItemStatus
from spindle.config import SpindleConfig


class TestDiscHandler:
    """Test DiscHandler functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(staging_dir=tmp_path / "staging")

    @pytest.fixture
    def handler(self, config):
        """Create disc handler instance."""
        with patch.multiple(
            'spindle.components.disc_handler',
            IntelligentDiscAnalyzer=Mock,
            TVSeriesDiscAnalyzer=Mock,
            SimpleMultiDiscManager=Mock,
            MakeMKVRipper=Mock,
            TMDBService=Mock,
        ):
            handler = DiscHandler(config)
            handler.queue_manager = Mock()
            return handler

    @pytest.mark.asyncio
    async def test_identify_disc_success(self, handler):
        """Test successful disc identification."""
        item = QueueItem(id=1, disc_title="Test Movie")
        disc_info = Mock(device="/dev/sr0", label="MOVIE_DISC")

        # Mock analysis result
        mock_analysis = Mock()
        mock_analysis.content_type = "movie"
        mock_analysis.confidence = 0.95
        mock_analysis.primary_title = "Test Movie"
        mock_analysis.year = 2023
        handler.disc_analyzer.analyze_disc.return_value = mock_analysis

        # Mock TMDB result
        mock_media_info = Mock()
        mock_media_info.title = "Test Movie"
        handler.tmdb_service.identify_media.return_value = mock_media_info

        # Mock multi-disc manager
        handler.multi_disc_manager.detect_multi_disc_series.return_value = False

        await handler.identify_disc(item, disc_info)

        assert item.status == QueueItemStatus.IDENTIFIED
        assert item.media_info == mock_media_info
        assert item.rip_spec_data is not None
        handler.queue_manager.update_item.assert_called()

    @pytest.mark.asyncio
    async def test_identify_disc_failure(self, handler):
        """Test disc identification failure handling."""
        item = QueueItem(id=1, disc_title="Test Movie")
        disc_info = Mock(device="/dev/sr0")

        handler.disc_analyzer.analyze_disc.side_effect = Exception("Analysis failed")

        await handler.identify_disc(item, disc_info)

        assert item.status == QueueItemStatus.FAILED
        assert "Analysis failed" in item.error_message

    @pytest.mark.asyncio
    async def test_rip_identified_item_success(self, handler):
        """Test successful disc ripping."""
        rip_spec_data = {
            "disc_info": {"device": "/dev/sr0", "label": "TEST"},
            "analysis_result": {"titles_to_rip": [{"index": 1}]},
        }
        item = QueueItem(
            id=1,
            disc_title="Test Movie",
            rip_spec_data=json.dumps(rip_spec_data)
        )

        mock_files = [Path("/tmp/test.mkv")]
        handler.ripper.rip_disc.return_value = mock_files

        with patch('spindle.components.disc_handler.eject_disc'):
            await handler.rip_identified_item(item)

        assert item.status == QueueItemStatus.RIPPED
        assert item.ripped_file == mock_files[0]
```

#### 2.6.3 Service Tests

**`tests/services/test_tmdb.py`:**
```python
"""Test TMDB service wrapper."""

import pytest
from unittest.mock import Mock, AsyncMock

from spindle.services.tmdb import TMDBService
from spindle.config import SpindleConfig


class TestTMDBService:
    """Test TMDBService functionality."""

    @pytest.fixture
    def config(self):
        """Create test configuration."""
        return SpindleConfig(tmdb_api_key="test_key")

    @pytest.fixture
    def service(self, config):
        """Create TMDB service instance."""
        with patch('spindle.services.tmdb.MediaIdentifier') as mock_identifier:
            service = TMDBService(config)
            service.identifier = mock_identifier.return_value
            return service

    @pytest.mark.asyncio
    async def test_identify_movie_success(self, service):
        """Test successful movie identification."""
        mock_media_info = Mock()
        service.identifier.identify_movie.return_value = mock_media_info

        result = await service.identify_media("Test Movie", "movie", 2023)

        assert result == mock_media_info
        service.identifier.identify_movie.assert_called_once_with("Test Movie", 2023)

    @pytest.mark.asyncio
    async def test_identify_tv_series_success(self, service):
        """Test successful TV series identification."""
        mock_media_info = Mock()
        service.identifier.identify_tv_series.return_value = mock_media_info

        result = await service.identify_media("Test Series", "tv_series", 2023)

        assert result == mock_media_info
        service.identifier.identify_tv_series.assert_called_once_with("Test Series", 2023)

    @pytest.mark.asyncio
    async def test_identify_unknown_content_type(self, service):
        """Test handling of unknown content type."""
        result = await service.identify_media("Test", "unknown", 2023)

        assert result is None
```

#### 2.6.4 Storage Tests

**`tests/storage/test_queue.py`:**
```python
"""Test queue management."""

import pytest
from pathlib import Path

from spindle.storage.queue import QueueManager, QueueItem, QueueItemStatus
from spindle.config import SpindleConfig


class TestQueueManager:
    """Test QueueManager functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(log_dir=tmp_path / "logs")

    @pytest.fixture
    def manager(self, config):
        """Create queue manager instance."""
        return QueueManager(config)

    def test_add_disc_creates_item(self, manager):
        """Test adding disc creates queue item."""
        item = manager.add_disc("Test Movie")

        assert item.id is not None
        assert item.disc_title == "Test Movie"
        assert item.status == QueueItemStatus.PENDING

    def test_update_item_persists_changes(self, manager):
        """Test updating item persists to database."""
        item = manager.add_disc("Test Movie")
        item.status = QueueItemStatus.COMPLETED

        manager.update_item(item)

        retrieved_items = manager.get_items_by_status(QueueItemStatus.COMPLETED)
        assert len(retrieved_items) == 1
        assert retrieved_items[0].disc_title == "Test Movie"

    def test_get_queue_stats_returns_counts(self, manager):
        """Test queue statistics calculation."""
        manager.add_disc("Movie 1")
        item2 = manager.add_disc("Movie 2")
        item2.status = QueueItemStatus.COMPLETED
        manager.update_item(item2)

        stats = manager.get_queue_stats()

        assert stats["pending"] == 1
        assert stats["completed"] == 1

    def test_reset_stuck_items(self, manager):
        """Test resetting stuck processing items."""
        item = manager.add_disc("Test Movie")
        item.status = QueueItemStatus.RIPPING
        manager.update_item(item)

        reset_count = manager.reset_stuck_processing_items()

        assert reset_count == 1
        updated_items = manager.get_items_by_status(QueueItemStatus.PENDING)
        assert len(updated_items) == 1
```

#### 2.6.5 Migration Commands

**Create new test structure:**
```bash
# Create new test directories
mkdir -p tests/{core,components,services,storage}

# Move existing relevant tests
mv tests/test_queue.py tests/storage/test_queue.py  # If exists
mv tests/test_cli.py tests/core/test_cli.py        # Update imports

# Update import statements in moved tests
find tests/ -name "*.py" -exec sed -i 's/from spindle\.queue\.manager/from spindle.storage.queue/g' {} \;
find tests/ -name "*.py" -exec sed -i 's/from spindle\.processor/from spindle.core.orchestrator/g' {} \;
```

**Integration Test Preservation:**
- Maintain existing integration test patterns from CLAUDE.md
- Focus on user-facing behavior rather than implementation details
- Test component interactions and workflow completion
- Mock external services (TMDB, Plex, drapto, MakeMKV)

### 2.7 Legacy Code Cleanup

**Scope:** Remove original modules and update all references after successful migration to new architecture.

**⚠️ CRITICAL:** Only execute this phase after Phase 2.2-2.6 are fully complete and tested. This phase makes irreversible changes to the codebase.

#### 2.7.1 Pre-Cleanup Validation

**Before any cleanup, verify:**
```bash
# All tests must pass with new architecture
uv run pytest tests/ -v

# CLI functionality verification
uv run spindle status  # Should use new daemon/orchestrator
uv run spindle show    # Should work with new logging
uv run spindle stop    # Should cleanly stop new orchestrator

# Integration test - full workflow
# Insert disc -> verify identification -> ripping -> encoding -> organization
```

**Create backup branch:**
```bash
git checkout -b pre-cleanup-backup
git commit -am "Backup before legacy cleanup - Phase 2.6 complete"
git checkout main
```

#### 2.7.2 Remove Legacy Processor Module

**Files to delete:**
```bash
# Remove the original monolithic processor
rm src/spindle/processor.py

# Verify no imports remain
grep -r "from.*processor import" src/
grep -r "import.*processor" src/
# Should return no results
```

**Update imports in:**
- `src/spindle/cli.py` - Remove any remaining processor imports
- Any test files still referencing old processor

#### 2.7.3 Service Module Cleanup

**Move and replace existing service files:**
```bash
# These moves should have been done in Phase 2.3, but verify:
# identify/tmdb.py -> services/tmdb.py (if not already moved)
# encode/drapto_wrapper.py -> services/drapto.py (if not already moved)
# organize/library.py -> services/plex.py (if not already moved)
# notify/ntfy.py -> services/ntfy.py (if not already moved)

# Remove empty directories
rmdir src/spindle/identify/ 2>/dev/null || true
rmdir src/spindle/encode/ 2>/dev/null || true
rmdir src/spindle/organize/ 2>/dev/null || true
rmdir src/spindle/notify/ 2>/dev/null || true
```

**Update all import statements:**
```bash
# Search and replace imports throughout codebase
find src/ -name "*.py" -exec grep -l "from spindle.identify.tmdb" {} \; | \
  xargs sed -i 's/from spindle\.identify\.tmdb/from spindle.services.tmdb/g'

find src/ -name "*.py" -exec grep -l "from spindle.encode.drapto_wrapper" {} \; | \
  xargs sed -i 's/from spindle\.encode\.drapto_wrapper/from spindle.services.drapto/g'

find src/ -name "*.py" -exec grep -l "from spindle.organize.library" {} \; | \
  xargs sed -i 's/from spindle\.organize\.library/from spindle.services.plex/g'

find src/ -name "*.py" -exec grep -l "from spindle.notify.ntfy" {} \; | \
  xargs sed -i 's/from spindle\.notify\.ntfy/from spindle.services.ntfy/g'
```

#### 2.7.4 Queue Module Migration

**Move queue management:**
```bash
# If not already moved in Phase 2.4
mv src/spindle/queue/manager.py src/spindle/storage/queue.py

# Remove old queue directory
rm -rf src/spindle/queue/

# Update imports
find src/ -name "*.py" -exec grep -l "from spindle.queue.manager" {} \; | \
  xargs sed -i 's/from spindle\.queue\.manager/from spindle.storage.queue/g'
```

#### 2.7.5 CLI Simplification Cleanup

**Remove deprecated CLI functions from `cli.py`:**
```python
# Remove any remaining foreground-related code
# Remove any processor-related imports or references
# Ensure CLI only uses new core.daemon.SpindleDaemon

# Clean up imports - remove unused:
# - Any processor imports
# - Any old service imports
# - Unused asyncio imports (if daemon handles async)
```

#### 2.7.6 Test Cleanup

**Remove old test files:**
```bash
# Remove tests for deleted modules
rm -f tests/test_processor.py 2>/dev/null || true
rm -f tests/queue/test_manager.py 2>/dev/null || true
rm -rf tests/queue/ 2>/dev/null || true
rm -rf tests/identify/ 2>/dev/null || true
rm -rf tests/encode/ 2>/dev/null || true
rm -rf tests/organize/ 2>/dev/null || true
rm -rf tests/notify/ 2>/dev/null || true

# Update test imports in remaining files
find tests/ -name "*.py" -exec grep -l "from spindle.processor" {} \; | \
  xargs sed -i '/from spindle\.processor/d'
```

#### 2.7.7 Final Verification

**Run comprehensive test suite:**
```bash
# All tests must pass
uv run pytest tests/ -v --cov=spindle

# Code quality checks
uv run black --check src/
uv run ruff check src/

# Verify no broken imports
python -c "import spindle.core.daemon; import spindle.core.orchestrator; print('Core imports OK')"
python -c "import spindle.components.disc_handler; import spindle.components.encoder; print('Components OK')"
python -c "import spindle.services.tmdb; import spindle.services.drapto; print('Services OK')"
```

**Integration testing:**
```bash
# Start daemon with new architecture
uv run spindle start --systemd &
SPINDLE_PID=$!

# Verify status works
uv run spindle status

# Verify show command works
timeout 5 uv run spindle show --lines 10

# Clean stop
uv run spindle stop
wait $SPINDLE_PID
```

#### 2.7.8 Documentation Updates

**Update imports in documentation:**
- Update any code examples in README.md that reference old modules
- Update CLAUDE.md with new module structure
- Update any inline documentation or docstrings referencing old paths

#### 2.7.9 Commit Cleanup Changes

**Create cleanup commit:**
```bash
git add -A
git commit -m "Phase 2.7: Remove legacy architecture

- Deleted processor.py (replaced by core/orchestrator.py)
- Moved all services to services/ directory
- Updated all import statements
- Cleaned up old test files
- Verified all functionality works with new architecture

🤖 Generated with [Claude Code](https://claude.ai/code)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

**Safety verification:**
```bash
# Verify the commit doesn't break anything
git show --name-status  # Review all changed files
uv run pytest tests/ -v  # Verify tests still pass
```

## Future Evolution Path

This refactoring provides a clean foundation for future enhancements:

**Immediate Benefits:**
- Clear daemon-only operation model
- Better monitoring and observability
- Improved code organization and maintainability

**Future Capabilities Enabled:**
- Plugin system (clean component interfaces)
- Alternative service implementations (service abstractions)
- Advanced TUI (separate monitoring from orchestration)
- Additional workflow components (standardized integration pattern)

The architecture balances current needs with future flexibility while avoiding premature optimization for uncertain requirements.