"""Command-line interface for Spindle."""

import asyncio
import logging
import os
import signal
import sys
import time
from pathlib import Path

try:
    import daemon  # type: ignore[import-untyped]
    import daemon.pidfile  # type: ignore[import-untyped]
except ImportError:
    daemon = None

import click
from rich.console import Console
from rich.logging import RichHandler
from rich.table import Table

from .config import SpindleConfig, create_sample_config, load_config
from .disc.monitor import detect_disc
from .encode.drapto_wrapper import DraptoEncoder
from .error_handling import ConfigurationError, check_dependencies
from .identify.tmdb import MediaIdentifier
from .notify.ntfy import NtfyNotifier
from .organize.library import LibraryOrganizer
from .process_lock import ProcessLock
from .processor import ContinuousProcessor
from .queue.manager import QueueItemStatus, QueueManager
from .system_check import check_system_dependencies

console = Console()


def check_uv_requirement() -> None:
    """Check if uv is available and recommend proper usage."""
    # Check for dependencies and display errors nicely
    missing_deps = check_dependencies()
    if missing_deps:
        console.print("[red bold]🚫 Missing Dependencies[/red bold]")
        console.print("Spindle requires the following dependencies:\n")
        for dep in missing_deps:
            console.print(f"  • {dep}")
        console.print("\n[dim]Install the missing dependencies and try again[/dim]")
        sys.exit(1)

    # Check if we're running through uv for development
    if not os.environ.get("UV_RUN_RECURSION_DEPTH") and "site-packages" in str(
        Path(__file__),
    ):
        console.print(
            "[yellow]TIP: For development, use 'uv run spindle [command]'[/yellow]",
        )
        console.print(
            "For end users, install with: uv tool install git+https://github.com/five82/spindle.git",
        )


def setup_logging(
    *,
    verbose: bool = False,
    config: SpindleConfig | None = None,
) -> None:
    """Set up logging configuration."""
    level = logging.DEBUG if verbose else logging.INFO

    # Clean up existing handlers first to prevent resource leaks
    cleanup_logging()

    # Configure RichHandler to show path only at DEBUG level
    show_path = level == logging.DEBUG
    handlers: list[logging.Handler] = [
        RichHandler(console=console, rich_tracebacks=True, show_path=show_path),
    ]

    # Add file handler if config is available
    if config and config.log_dir:
        config.log_dir.mkdir(parents=True, exist_ok=True)
        log_file = config.log_dir / "spindle.log"
        file_handler = logging.FileHandler(log_file)
        file_handler.setFormatter(
            logging.Formatter(
                "%(asctime)s - %(name)s - %(levelname)s - %(message)s",
            ),
        )
        handlers.append(file_handler)

    logging.basicConfig(
        level=level,
        format="%(message)s",
        datefmt="[%X]",
        handlers=handlers,
        force=True,  # Force reconfiguration of root logger
    )


def cleanup_logging() -> None:
    """Clean up logging handlers to prevent ResourceWarnings."""
    root_logger = logging.getLogger()
    for handler in root_logger.handlers[:]:
        if isinstance(handler, logging.FileHandler):
            handler.close()
            root_logger.removeHandler(handler)


@click.group()
@click.option(
    "--config",
    "-c",
    type=click.Path(exists=True, path_type=Path),
    help="Configuration file path",
)
@click.option("--verbose", "-v", is_flag=True, help="Enable verbose logging")
@click.pass_context
def cli(ctx: click.Context, config: Path | None, verbose: bool) -> None:
    """Spindle - Automated disc ripping, encoding, and media library management."""
    check_uv_requirement()

    try:
        ctx.ensure_object(dict)
        loaded_config = load_config(config)
        ctx.obj["config"] = loaded_config
        ctx.obj["verbose"] = verbose

        # Setup logging with the loaded config for file logging
        setup_logging(verbose=verbose, config=loaded_config)
    except (OSError, ValueError, RuntimeError) as e:
        config_error = ConfigurationError(
            f"Failed to load configuration: {e}",
            config_path=config,
            solution="Run 'spindle config validate' to check your configuration file",
        )
        console.print(f"[red]Configuration Error:[/red] {config_error}")
        sys.exit(1)


@cli.group()
@click.pass_context
def config_cmd(ctx: click.Context) -> None:
    """Configuration management commands."""


@config_cmd.command("show")
@click.pass_context
def config_show(ctx: click.Context) -> None:
    """Show current configuration."""
    config: SpindleConfig = ctx.obj["config"]

    table = Table()
    table.add_column("Setting")
    table.add_column("Value")

    # Show key configuration values
    table.add_row("Library Directory", str(config.library_dir))
    table.add_row("Staging Directory", str(config.staging_dir))
    table.add_row("Log Directory", str(config.log_dir))
    table.add_row("Review Directory", str(config.review_dir))
    table.add_row("Optical Drive", config.optical_drive)
    table.add_row("TMDB API Key", "***" if config.tmdb_api_key else "Not configured")
    table.add_row("Plex URL", config.plex_url or "Not configured")
    table.add_row("Ntfy Topic", config.ntfy_topic or "Not configured")

    console.print(table)


@config_cmd.command("validate")
@click.pass_context
def config_validate(ctx: click.Context) -> None:
    """Validate current configuration."""
    config: SpindleConfig = ctx.obj["config"]

    console.print("[bold]Configuration Validation[/bold]")

    # Check directories
    errors = []

    for name, path in [
        ("Library", config.library_dir),
        ("Staging", config.staging_dir),
        ("Log", config.log_dir),
        ("Review", config.review_dir),
    ]:
        try:
            path.mkdir(parents=True, exist_ok=True)
            console.print(f"[green]✓[/green] {name} directory: {path}")
        except Exception as e:
            console.print(f"[red]✗[/red] {name} directory: {e}")
            errors.append(f"{name} directory: {e}")

    # Check required settings
    if not config.tmdb_api_key:
        console.print("[red]✗[/red] TMDB API key not configured")
        errors.append("TMDB API key not configured")
    else:
        console.print("[green]✓[/green] TMDB API key configured")

    # Check optical drive
    if Path(config.optical_drive).exists():
        console.print(f"[green]✓[/green] Optical drive: {config.optical_drive}")
    else:
        console.print(
            f"[yellow]⚠[/yellow] Optical drive not found: {config.optical_drive}",
        )

    if errors:
        console.print(f"\n[red]Found {len(errors)} configuration errors[/red]")
        sys.exit(1)
    else:
        console.print("\n[green]Configuration is valid[/green]")


@config_cmd.command("init")
@click.option(
    "--path",
    "-p",
    type=click.Path(path_type=Path),
    default=Path.home() / ".config" / "spindle" / "config.toml",
    help="Path for the configuration file",
)
def config_init(path: Path) -> None:
    """Create a sample configuration file."""
    try:
        create_sample_config(path)
        console.print(f"[green]Created sample configuration at {path}[/green]")
        console.print("Please edit the configuration file with your settings.")
    except OSError as e:
        console.print(f"[red]Error creating configuration: {e}[/red]")
        sys.exit(1)


@cli.command()
@click.option(
    "--path",
    "-p",
    type=click.Path(path_type=Path),
    default=Path.home() / ".config" / "spindle" / "config.toml",
    help="Path for the configuration file",
)
def init_config(path: Path) -> None:
    """Create a sample configuration file."""
    try:
        create_sample_config(path)
        console.print(f"[green]Created sample configuration at {path}[/green]")
        console.print("Please edit the configuration file with your settings.")
    except OSError as e:
        console.print(f"[red]Error creating configuration: {e}[/red]")
        sys.exit(1)


@cli.command()
@click.pass_context
def status(ctx: click.Context) -> None:
    """Show system status and queue information."""
    config: SpindleConfig = ctx.obj["config"]

    # Check system components
    console.print("[bold]System Status[/bold]")

    # Check if Spindle is running using modern process discovery
    spindle_running = False
    process_info = ProcessLock.find_spindle_process()

    if process_info:
        pid, mode = process_info
        console.print(f"🟢 Spindle: [green]Running in {mode} mode (PID {pid})[/green]")
        spindle_running = True
    else:
        console.print("🔴 Spindle: [red]Not running[/red]")

    # Check disc drive
    disc_info = detect_disc(config.optical_drive)
    if disc_info:
        console.print(f"📀 Disc: {disc_info}")
    else:
        console.print("📀 Disc: No disc detected")

    # Check drapto
    encoder = DraptoEncoder(config)
    if encoder.check_drapto_availability():
        version = encoder.get_drapto_version()
        console.print(f"⚙️ Drapto: Available ({version or 'unknown version'})")
    else:
        console.print("⚙️ Drapto: [red]Not available[/red]")

    # Check Plex - show different status based on whether Spindle is running
    organizer = LibraryOrganizer(config)
    if organizer.verify_plex_connection():
        if spindle_running:
            console.print("📚 Plex: Connected")
        else:
            console.print("📚 Plex: Available")
    else:
        console.print("📚 Plex: [yellow]Not configured or unreachable[/yellow]")

    # Check notifications
    NtfyNotifier(config)
    if config.ntfy_topic:
        console.print("📱 Notifications: Configured")
    else:
        console.print("📱 Notifications: [yellow]Not configured[/yellow]")

    # Show queue status
    console.print("\n[bold]Queue Status[/bold]")
    queue_manager = QueueManager(config)
    stats = queue_manager.get_queue_stats()

    if not stats:
        console.print("Queue is empty")
    else:
        table = Table()
        table.add_column("Status")
        table.add_column("Count", justify="right")

        for status, count in stats.items():
            if hasattr(status, "value"):
                # Enum object
                status_str = status.value.replace("_", " ").title()
            else:
                # String key
                status_str = status.replace("_", " ").title()
            table.add_row(status_str, str(count))

        console.print(table)


@cli.command()
@click.option("--daemon", "-d", is_flag=True, help="Run as background daemon (default)")
@click.option("--foreground", "-f", is_flag=True, help="Run in foreground")
@click.pass_context
def start(ctx: click.Context, daemon: bool, foreground: bool) -> None:
    """Start continuous processing mode - auto-rip discs and process queue."""
    config: SpindleConfig = ctx.obj["config"]

    # Check system dependencies before starting - validate required only
    console.print("Checking system dependencies...")
    check_system_dependencies(validate_required=True)

    # Default to daemon mode unless explicitly foreground
    # Exception: if running as systemd service, always run in foreground
    is_systemd = os.getenv("INVOCATION_ID") is not None
    run_as_daemon = False if is_systemd else daemon or not foreground

    if run_as_daemon:
        start_daemon(config)
    else:
        start_foreground(config)


def start_daemon(config: SpindleConfig) -> None:
    """Start Spindle as a background daemon."""
    if daemon is None:
        console.print("[red]ERROR: python-daemon package not installed[/red]")
        console.print("Install with: uv pip install python-daemon")
        sys.exit(1)

    # Set up paths
    log_file_path = config.log_dir / "spindle.log"
    config.ensure_directories()

    # Check if already running using modern process discovery
    process_info = ProcessLock.find_spindle_process()
    if process_info:
        pid, mode = process_info
        console.print(
            f"[yellow]Spindle is already running in {mode} mode (PID {pid})[/yellow]",
        )
        console.print("Use 'spindle stop' to stop it first")
        sys.exit(1)

    console.print("[green]Starting Spindle daemon...[/green]")
    console.print(f"Log file: {log_file_path}")
    console.print(f"Monitoring: {config.optical_drive}")

    # Create process lock
    lock = ProcessLock(config)

    # Set up daemon context (without PID file)
    daemon_context = daemon.DaemonContext(
        working_directory=Path.cwd(),
        umask=0o002,
    )

    # Set up logging for daemon
    def setup_daemon_logging() -> None:
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

    def run_daemon() -> None:
        setup_daemon_logging()

        # Acquire the process lock
        if not lock.acquire():
            logger = logging.getLogger(__name__)
            logger.error(
                "Failed to acquire process lock - another instance may be running",
            )
            sys.exit(1)

        processor = ContinuousProcessor(config)

        def signal_handler(signum: int, frame: object) -> None:
            logger = logging.getLogger(__name__)
            logger.info("Received signal %s, stopping processor", signum)
            processor.stop()
            lock.release()
            sys.exit(0)

        signal.signal(signal.SIGTERM, signal_handler)
        signal.signal(signal.SIGINT, signal_handler)

        try:
            logger = logging.getLogger(__name__)
            logger.info("Starting Spindle continuous processor")
            processor.start()

            # Keep daemon alive
            while processor.is_running:
                time.sleep(config.status_display_interval)

        except Exception as e:
            logger = logging.getLogger(__name__)
            logger.exception("Error in processor: %s", e)
            processor.stop()
            lock.release()
            sys.exit(1)
        finally:
            lock.release()

    try:
        with daemon_context:
            run_daemon()
    except Exception as e:
        console.print(f"[red]Failed to start daemon: {e}[/red]")
        sys.exit(1)


def start_foreground(config: SpindleConfig) -> None:
    """Start Spindle in foreground mode."""
    logger = logging.getLogger(__name__)

    # Check if already running
    process_info = ProcessLock.find_spindle_process()
    if process_info:
        pid, mode = process_info
        console.print(
            f"[yellow]Spindle is already running in {mode} mode (PID {pid})[/yellow]",
        )
        console.print("Use 'spindle stop' to stop it first")
        sys.exit(1)

    console.print("[green]Starting Spindle continuous processor (foreground)[/green]")
    console.print(f"Monitoring: {config.optical_drive}")
    console.print("Insert discs to begin automatic ripping and processing")
    console.print("Press Ctrl+C to stop")

    # Create process lock
    lock = ProcessLock(config)
    if not lock.acquire():
        console.print(
            "[red]Failed to acquire process lock - another instance may be running[/red]",
        )
        sys.exit(1)

    processor = ContinuousProcessor(config)

    def signal_handler(signum: int, frame: object) -> None:
        console.print("\n[yellow]Stopping Spindle processor...[/yellow]")
        processor.stop()
        lock.release()
        sys.exit(0)

    # Set up signal handlers
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    async def run_foreground():
        processor.start()

        # Keep main thread alive and show status changes
        last_status = None
        try:
            while processor.is_running:
                await asyncio.sleep(
                    config.status_display_interval,
                )  # Check status based on config interval
                status = processor.get_status()

                # Only show status if it has changed
                current_status_key = (status["total_items"], status["current_disc"])
                if status["total_items"] > 0 and current_status_key != last_status:
                    logger.info(
                        f"Queue: {status['total_items']} items | Current disc: {status['current_disc'] or 'None'}",
                    )
                    last_status = current_status_key
        except asyncio.CancelledError:
            processor.stop()
            raise

    try:
        asyncio.run(run_foreground())
    except Exception as e:
        console.print(f"[red]Error in processor: {e}[/red]")
        processor.stop()
        lock.release()
        sys.exit(1)
    finally:
        lock.release()


@cli.command()
@click.pass_context
def stop(ctx: click.Context) -> None:
    """Stop running Spindle process."""
    config: SpindleConfig = ctx.obj["config"]

    # Find running spindle process
    process_info = ProcessLock.find_spindle_process()

    if not process_info:
        console.print("[yellow]Spindle is not running[/yellow]")
        # Clean up any stale lock files
        lock_file = config.log_dir / "spindle.lock"
        if lock_file.exists():
            lock_file.unlink(missing_ok=True)
        return

    pid, mode = process_info
    console.print(f"[blue]Stopping Spindle {mode} mode (PID {pid})...[/blue]")

    if ProcessLock.stop_process(pid):
        console.print("[green]Spindle stopped[/green]")
        # Clean up lock file
        lock_file = config.log_dir / "spindle.lock"
        if lock_file.exists():
            lock_file.unlink(missing_ok=True)
    else:
        console.print(f"[red]Failed to stop Spindle process {pid}[/red]")
        sys.exit(1)


@cli.command("add-file")
@click.argument("file_path", type=click.Path(exists=True, path_type=Path))
@click.pass_context
def add_file(ctx: click.Context, file_path: Path) -> None:
    """Add a video file to the processing queue."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)

    if file_path.suffix.lower() not in [".mkv", ".mp4", ".avi"]:
        console.print(f"[red]Unsupported file type: {file_path.suffix}[/red]")
        sys.exit(1)

    item = queue_manager.add_file(file_path)
    console.print(f"[green]Added to queue: {item}[/green]")


@cli.group()
@click.pass_context
def queue(ctx: click.Context) -> None:
    """Queue management commands."""


@queue.command("status")
@click.pass_context
def queue_status(ctx: click.Context) -> None:
    """Show queue status information."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)
    stats = queue_manager.get_queue_stats()

    if not stats:
        console.print("Queue is empty")
    else:
        table = Table()
        table.add_column("Status")
        table.add_column("Count", justify="right")

        for status, count in stats.items():
            if hasattr(status, "value"):
                # Enum object
                status_str = status.value.replace("_", " ").title()
            else:
                # String key
                status_str = status.replace("_", " ").title()
            table.add_row(status_str, str(count))

        console.print(table)


@queue.command("list")
@click.pass_context
def queue_list(ctx: click.Context) -> None:
    """List all items in the queue."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)

    items = queue_manager.get_all_items()

    if not items:
        console.print("Queue is empty")
        return

    table = Table()
    table.add_column("ID", justify="right")
    table.add_column("Title")
    table.add_column("Status")
    table.add_column("Created")

    for item in items:
        title = item.disc_title or (
            item.source_path.name if item.source_path else "Unknown"
        )
        if item.media_info:
            title = str(item.media_info)

        table.add_row(
            str(item.item_id),
            title,
            item.status.value.title(),
            item.created_at.strftime("%Y-%m-%d %H:%M"),
        )

    console.print(table)


@queue.command("clear")
@click.option("--completed", is_flag=True, help="Only clear completed items")
@click.option("--failed", is_flag=True, help="Only clear failed items")
@click.option(
    "--force",
    is_flag=True,
    help="Force clear all items including those in processing",
)
@click.option(
    "--yes",
    "-y",
    is_flag=True,
    help="Skip confirmation prompt",
)
@click.pass_context
def queue_clear(
    ctx: click.Context,
    completed: bool,
    failed: bool,
    force: bool,
    yes: bool,
) -> None:
    """Clear items from the queue."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)

    if sum([completed, failed, force]) > 1:
        console.print("[red]Error: Cannot specify multiple clear options[/red]")
        return

    if completed:
        count = queue_manager.clear_completed()
        console.print(f"[green]Cleared {count} completed items[/green]")
    elif failed:
        count = queue_manager.clear_failed()
        console.print(f"[green]Cleared {count} failed items[/green]")
    elif force:
        if yes or click.confirm(
            "Are you sure you want to FORCE clear the entire queue (including processing items)?",
        ):
            count = queue_manager.clear_all(force=True)
            console.print(f"[green]Force cleared {count} items from queue[/green]")
    elif yes or click.confirm("Are you sure you want to clear the entire queue?"):
        try:
            count = queue_manager.clear_all()
            console.print(f"[green]Cleared {count} items from queue[/green]")
        except RuntimeError as e:
            console.print(f"[red]Error: {e}[/red]")
            console.print(
                "[yellow]Wait for processing items to complete or use --force to clear all items[/yellow]",
            )


@queue.command("retry")
@click.argument("item_id", type=int)
@click.pass_context
def queue_retry(ctx: click.Context, item_id: int) -> None:
    """Retry a failed queue item."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)

    try:
        item = queue_manager.get_item(item_id)
        if not item:
            console.print(f"[red]Item {item_id} not found[/red]")
            return

        if item.status != QueueItemStatus.FAILED:
            console.print(f"[yellow]Item {item_id} is not in failed state[/yellow]")
            return

        # Reset to pending for retry
        item.status = QueueItemStatus.PENDING
        item.error_message = None
        queue_manager.update_item(item)

        console.print(f"[green]Item {item_id} reset for retry[/green]")

    except Exception as e:
        console.print(f"[red]Error retrying item: {e}[/red]")


@cli.command("queue-health")
@click.pass_context
def queue_health(ctx: click.Context) -> None:
    """Check database health and schema integrity."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)

    console.print("[bold blue]Database Health Check[/bold blue]")
    console.print()

    health = queue_manager.check_database_health()

    # Display health status
    if health["database_exists"]:
        console.print(f"[green]✓[/green] Database file exists: {queue_manager.db_path}")
    else:
        console.print(f"[red]✗[/red] Database file missing: {queue_manager.db_path}")
        return

    if health["database_readable"]:
        console.print("[green]✓[/green] Database is readable")
    else:
        console.print("[red]✗[/red] Database is not readable")
        if "error" in health:
            console.print(f"[red]Error: {health['error']}[/red]")
        return

    console.print(f"[blue]Schema Version:[/blue] {health['schema_version']}")

    if health["table_exists"]:
        console.print("[green]✓[/green] Main queue_items table exists")
    else:
        console.print("[red]✗[/red] Main queue_items table missing")

    if health["integrity_check"]:
        console.print("[green]✓[/green] Database integrity check passed")
    else:
        console.print("[red]✗[/red] Database integrity check failed")

    console.print(f"[blue]Total Items:[/blue] {health['total_items']}")

    # Show column status
    if health["missing_columns"]:
        console.print(
            f"[yellow]⚠[/yellow] Missing columns: {', '.join(health['missing_columns'])}",
        )
    else:
        console.print("[green]✓[/green] All expected columns present")

    if "error" in health:
        console.print(f"[red]Error during health check: {health['error']}[/red]")


@cli.command("test-notify")
@click.pass_context
def test_notify(ctx: click.Context) -> None:
    """Send a test notification."""
    config: SpindleConfig = ctx.obj["config"]
    notifier = NtfyNotifier(config)

    if notifier.test_notification():
        console.print("[green]Test notification sent successfully[/green]")
    else:
        console.print("[red]Failed to send test notification[/red]")


async def process_queue_manual(config: SpindleConfig) -> None:
    """Process all pending items in the queue."""
    queue_manager = QueueManager(config)
    identifier = MediaIdentifier(config)
    encoder = DraptoEncoder(config)
    organizer = LibraryOrganizer(config)
    notifier = NtfyNotifier(config)

    pending_items = queue_manager.get_pending_items()

    if not pending_items:
        console.print("No items to process")
        return

    notifier.notify_queue_started(len(pending_items))
    console.print(f"[green]Processing {len(pending_items)} items[/green]")

    start_time = time.time()
    processed = 0
    failed = 0

    for item in pending_items:
        try:
            console.print(f"\n[blue]Processing: {item}[/blue]")

            # Skip if not in correct state
            if item.status not in [
                QueueItemStatus.RIPPED,
                QueueItemStatus.IDENTIFIED,
                QueueItemStatus.ENCODED,
            ]:
                continue

            # Identify media if needed
            if item.status == QueueItemStatus.RIPPED and not item.media_info:
                if not item.ripped_file:
                    console.print("[red]Error: No ripped file to identify[/red]")
                    continue
                console.print("Identifying media...")
                item.status = QueueItemStatus.IDENTIFYING
                queue_manager.update_item(item)

                item.media_info = await identifier.identify_media(item.ripped_file)

                if item.media_info:
                    item.status = QueueItemStatus.IDENTIFIED
                    console.print(f"[green]Identified: {item.media_info}[/green]")
                else:
                    # Move to review
                    if item.ripped_file:
                        organizer.create_review_directory(
                            item.ripped_file,
                            "unidentified",
                        )
                        notifier.notify_unidentified_media(item.ripped_file.name)
                    item.status = QueueItemStatus.REVIEW
                    console.print(
                        "[yellow]Could not identify, moved to review[/yellow]",
                    )

                queue_manager.update_item(item)
                continue

            # Encode if needed
            if item.status == QueueItemStatus.IDENTIFIED and not item.encoded_file:
                if not item.ripped_file:
                    console.print("[red]Error: No ripped file to encode[/red]")
                    continue
                console.print("Encoding...")
                # Use generic queue started notification for encoding
                notifier.notify_queue_started(1)
                item.status = QueueItemStatus.ENCODING
                queue_manager.update_item(item)

                result = encoder.encode_file(
                    item.ripped_file,
                    config.staging_dir / "encoded",
                )

                if result.success:
                    item.encoded_file = result.output_file
                    item.status = QueueItemStatus.ENCODED
                    # Use generic queue completed notification for encoding
                    notifier.notify_queue_completed(1, 0, "unknown")
                    console.print(f"[green]Encoded: {result.output_file}[/green]")
                else:
                    item.status = QueueItemStatus.FAILED
                    item.error_message = result.error_message
                    notifier.notify_error(f"Encoding failed: {result.error_message}")
                    console.print(f"[red]Encoding failed: {result.error_message}[/red]")
                    failed += 1

                queue_manager.update_item(item)
                continue

            # Organize and import to Plex
            if item.status == QueueItemStatus.ENCODED and item.encoded_file:
                if not item.media_info:
                    console.print("[red]Error: No media info for organization[/red]")
                    continue
                console.print("Organizing and importing to Plex...")
                item.status = QueueItemStatus.ORGANIZING
                queue_manager.update_item(item)

                if organizer.add_to_plex(item.encoded_file, item.media_info):
                    item.status = QueueItemStatus.COMPLETED
                    notifier.notify_media_added(
                        str(item.media_info),
                        item.media_info.media_type,
                    )
                    console.print(f"[green]Added to Plex: {item.media_info}[/green]")
                    processed += 1
                else:
                    item.status = QueueItemStatus.FAILED
                    item.error_message = "Failed to organize/import to Plex"
                    failed += 1

                queue_manager.update_item(item)

        except Exception as e:
            console.print(f"[red]Error processing {item}: {e}[/red]")
            item.status = QueueItemStatus.FAILED
            item.error_message = str(e)
            queue_manager.update_item(item)
            failed += 1

    # Send completion notification
    duration = time.strftime("%H:%M:%S", time.gmtime(time.time() - start_time))
    notifier.notify_queue_completed(processed, failed, duration)

    console.print(
        f"\n[green]Queue processing complete: {processed} processed, {failed} failed[/green]",
    )


# CLI utility functions
def format_duration(seconds: int) -> str:
    """Format duration in seconds to human readable format."""
    if seconds < 60:
        return f"0:00:{seconds:02d}"
    hours = seconds // 3600
    minutes = (seconds % 3600) // 60
    secs = seconds % 60
    return f"{hours}:{minutes:02d}:{secs:02d}"


def format_file_size(size_bytes: int) -> str:
    """Format file size in bytes to human readable format."""
    if size_bytes < 1024:
        return f"{size_bytes} B"
    if size_bytes < 1024 * 1024:
        return f"{size_bytes / 1024:.1f} KB"
    if size_bytes < 1024 * 1024 * 1024:
        return f"{size_bytes / (1024 * 1024):.1f} MB"
    return f"{size_bytes / (1024 * 1024 * 1024):.1f} GB"


def get_status_color(status: object) -> str:
    """Get color code for status display."""
    status_colors = {
        "pending": "yellow",
        "processing": "blue",
        "completed": "green",
        "failed": "red",
        "ripping": "blue",
        "ripped": "green",
        "identifying": "blue",
        "identified": "green",
        "encoding": "blue",
        "encoded": "green",
        "organizing": "blue",
        "review": "yellow",
    }

    # Handle enum objects
    status_str = status.value if hasattr(status, "value") else str(status)

    return status_colors.get(status_str.lower(), "white")


def format_table_data(data: list[dict], columns: list[str]) -> Table:
    """Format data into a rich table."""
    table = Table()

    for column in columns:
        table.add_column(column.title())

    for row in data:
        table.add_row(*[str(row.get(col, "")) for col in columns])

    return table


def format_queue_table(queue_items: list) -> Table:
    """Format queue items into a table."""
    table = Table()
    table.add_column("ID", justify="right")
    table.add_column("Title")
    table.add_column("Status")
    table.add_column("Created")

    for item in queue_items:
        title = item.disc_title or (
            item.source_path.name if item.source_path else "Unknown"
        )
        if item.media_info:
            title = str(item.media_info)

        table.add_row(
            str(item.item_id),
            title,
            item.status.value.title(),
            item.created_at.strftime("%Y-%m-%d %H:%M"),
        )

    return table


def main() -> None:
    """Entry point for the CLI."""
    cli()


if __name__ == "__main__":
    main()
