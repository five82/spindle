"""Command-line interface for Spindle."""

import logging
import os
import shutil
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
from .identify.tmdb import MediaIdentifier
from .notify.ntfy import NtfyNotifier
from .organize.library import LibraryOrganizer
from .processor import ContinuousProcessor
from .queue.manager import QueueItemStatus, QueueManager

console = Console()


def check_uv_requirement() -> None:
    """Check if uv is available and recommend proper usage."""
    # Check if uv is installed
    if not shutil.which("uv"):
        console.print("[red]ERROR: uv package manager is required but not found![/red]")
        console.print("Spindle uses uv for dependency management.")
        console.print("Install uv first:")
        console.print("  curl -LsSf https://astral.sh/uv/install.sh | sh")
        console.print("  source ~/.bashrc  # or restart terminal")
        console.print()
        console.print("Then install and run spindle with:")
        console.print("  uv tool install git+https://github.com/five82/spindle.git")
        console.print("  spindle [command]")
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


def setup_logging(verbose: bool = False) -> None:
    """Set up logging configuration."""
    level = logging.DEBUG if verbose else logging.INFO

    logging.basicConfig(
        level=level,
        format="%(message)s",
        datefmt="[%X]",
        handlers=[RichHandler(console=console, rich_tracebacks=True)],
    )


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
    setup_logging(verbose)

    try:
        ctx.ensure_object(dict)
        ctx.obj["config"] = load_config(config)
        ctx.obj["verbose"] = verbose
    except (OSError, ValueError, RuntimeError, Exception) as e:
        console.print(f"[red]Error loading configuration: {e}[/red]")
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
    except (OSError, Exception) as e:
        console.print(f"[red]Error creating configuration: {e}[/red]")
        sys.exit(1)


@cli.command()
@click.pass_context
def status(ctx: click.Context) -> None:
    """Show system status and queue information."""
    config: SpindleConfig = ctx.obj["config"]

    # Check system components
    console.print("[bold]System Status[/bold]")

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

    # Check Plex
    organizer = LibraryOrganizer(config)
    if organizer.verify_plex_connection():
        console.print("📚 Plex: Connected")
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
    pid_file_path = config.log_dir / "spindle.pid"
    log_file_path = config.log_dir / "spindle.log"

    config.ensure_directories()

    # Check if already running
    if pid_file_path.exists():
        try:
            with pid_file_path.open() as f:
                pid = int(f.read().strip())
            # Check if process is actually running
            os.kill(pid, 0)  # This will raise an exception if process doesn't exist
            console.print(f"[yellow]Spindle is already running with PID {pid}[/yellow]")
            console.print("Use 'spindle stop' to stop it first")
            sys.exit(1)
        except (OSError, ProcessLookupError, ValueError):
            # Process not running, remove stale PID file
            pid_file_path.unlink(missing_ok=True)

    console.print("[green]Starting Spindle daemon...[/green]")
    console.print(f"PID file: {pid_file_path}")
    console.print(f"Log file: {log_file_path}")
    console.print(f"Monitoring: {config.optical_drive}")

    # Set up daemon context
    daemon_context = daemon.DaemonContext(
        pidfile=daemon.pidfile.PIDLockFile(pid_file_path),
        working_directory=Path.cwd(),
        umask=0o002,
    )

    # Set up logging for daemon
    def setup_daemon_logging() -> None:
        import logging

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
        processor = ContinuousProcessor(config)

        def signal_handler(signum: int, frame: object) -> None:
            logging.info("Received signal %s, stopping processor", signum)
            processor.stop()
            sys.exit(0)

        signal.signal(signal.SIGTERM, signal_handler)
        signal.signal(signal.SIGINT, signal_handler)

        try:
            logging.info("Starting Spindle continuous processor")
            processor.start()

            # Keep daemon alive
            while processor.is_running:
                time.sleep(config.status_display_interval)

        except Exception as e:
            logging.exception("Error in processor: %s", e)
            processor.stop()
            sys.exit(1)

    try:
        with daemon_context:
            run_daemon()
    except Exception as e:
        console.print(f"[red]Failed to start daemon: {e}[/red]")
        sys.exit(1)


def start_foreground(config: SpindleConfig) -> None:
    """Start Spindle in foreground mode."""
    console.print("[green]Starting Spindle continuous processor (foreground)[/green]")
    console.print(f"Monitoring: {config.optical_drive}")
    console.print("Insert discs to begin automatic ripping and processing")
    console.print("Press Ctrl+C to stop")

    processor = ContinuousProcessor(config)

    def signal_handler(signum: int, frame: object) -> None:
        console.print("\n[yellow]Stopping Spindle processor...[/yellow]")
        processor.stop()
        sys.exit(0)

    # Set up signal handlers
    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    try:
        processor.start()

        # Keep main thread alive and show periodic status
        while processor.is_running:
            time.sleep(config.status_display_interval)  # Show status based on config
            status = processor.get_status()
            if status["total_items"] > 0:
                console.print(
                    f"[dim]Queue: {status['total_items']} items | Current disc: {status['current_disc'] or 'None'}[/dim]",
                )

    except Exception as e:
        console.print(f"[red]Error in processor: {e}[/red]")
        processor.stop()
        sys.exit(1)


@cli.command()
@click.pass_context
def stop(ctx: click.Context) -> None:
    """Stop running Spindle daemon."""
    config: SpindleConfig = ctx.obj["config"]

    pid_file_path = config.log_dir / "spindle.pid"

    if not pid_file_path.exists():
        console.print("[yellow]Spindle is not running (no PID file found)[/yellow]")
        return

    try:
        with open(pid_file_path) as f:
            pid = int(f.read().strip())

        # Check if process is running
        import os

        try:
            os.kill(pid, 0)  # Check if process exists
            console.print(f"[blue]Stopping Spindle daemon (PID {pid})...[/blue]")

            # Send SIGTERM
            os.kill(pid, signal.SIGTERM)

            # Wait for process to stop
            import time

            for _ in range(10):  # Wait up to 10 seconds
                try:
                    os.kill(pid, 0)
                    time.sleep(1)  # Keep this as 1 second for process termination check
                except ProcessLookupError:
                    break
            else:
                # If still running, force kill
                console.print(
                    "[yellow]Process didn't stop gracefully, force killing...[/yellow]",
                )
                os.kill(pid, signal.SIGKILL)

            # Clean up PID file
            pid_file_path.unlink(missing_ok=True)
            console.print("[green]Spindle stopped[/green]")

        except ProcessLookupError:
            # Process not running, clean up stale PID file
            pid_file_path.unlink(missing_ok=True)
            console.print(
                "[yellow]Spindle was not running (cleaned up stale PID file)[/yellow]",
            )

    except (ValueError, FileNotFoundError, PermissionError) as e:
        console.print(f"[red]Error stopping Spindle: {e}[/red]")
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


@cli.command("queue-list")
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


@cli.command("queue-clear")
@click.option("--completed", is_flag=True, help="Only clear completed items")
@click.option("--failed", is_flag=True, help="Only clear failed items")
@click.pass_context
def queue_clear(ctx: click.Context, completed: bool, failed: bool) -> None:
    """Clear items from the queue."""
    config: SpindleConfig = ctx.obj["config"]
    queue_manager = QueueManager(config)

    if completed and failed:
        console.print("[red]Error: Cannot specify both --completed and --failed[/red]")
        return

    if completed:
        count = queue_manager.clear_completed()
        console.print(f"[green]Cleared {count} completed items[/green]")
    elif failed:
        count = queue_manager.clear_failed()
        console.print(f"[green]Cleared {count} failed items[/green]")
    elif click.confirm("Are you sure you want to clear the entire queue?"):
        try:
            count = queue_manager.clear_all()
            console.print(f"[green]Cleared {count} items from queue[/green]")
        except RuntimeError as e:
            console.print(f"[red]Error: {e}[/red]")
            console.print(
                "[yellow]Wait for processing items to complete or use --completed to clear only finished items[/yellow]",
            )


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


def main() -> None:
    """Entry point for the CLI."""
    cli()


if __name__ == "__main__":
    main()
