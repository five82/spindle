"""Command-line interface for Spindle."""

import logging
import os
import sys
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
from .core.daemon import SpindleDaemon
from .core.orchestrator import SpindleOrchestrator
from .disc.monitor import detect_disc
from .error_handling import ConfigurationError, check_dependencies
from .process_manager import ProcessManager
from .services.drapto import DraptoService
from .services.ntfy import NotificationService
from .services.plex import PlexService
from .storage.queue import QueueItemStatus, QueueManager
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
    process_info = ProcessManager.find_spindle_process()

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
    drapto_service = DraptoService(config)
    if drapto_service.validate_drapto_available():
        console.print("⚙️ Drapto: Available")
    else:
        console.print("⚙️ Drapto: [red]Not available[/red]")

    # Check Plex - show different status based on whether Spindle is running
    plex_service = PlexService(config)
    if plex_service.test_connection():
        if spindle_running:
            console.print("📚 Plex: Connected")
        else:
            console.print("📚 Plex: Available")
    else:
        console.print("📚 Plex: [yellow]Not configured or unreachable[/yellow]")

    # Check notifications
    if config.ntfy_topic:
        console.print("📱 Notifications: Configured")
    else:
        console.print("📱 Notifications: [yellow]Not configured[/yellow]")

    # Show queue status via orchestrator
    console.print("\n[bold]Queue Status[/bold]")
    if spindle_running:
        try:
            orchestrator = SpindleOrchestrator(config)
            status_info = orchestrator.get_status()

            console.print(f"Current disc: {status_info.get('current_disc') or 'None'}")
            console.print(f"Total items: {status_info.get('total_items', 0)}")

            if status_info.get("queue_stats"):
                table = Table()
                table.add_column("Status")
                table.add_column("Count", justify="right")

                for status, count in status_info["queue_stats"].items():
                    status_str = status.replace("_", " ").title()
                    table.add_row(status_str, str(count))

                console.print(table)
            else:
                console.print("Queue is empty")

        except Exception as e:
            console.print(f"[yellow]Could not get detailed status: {e}[/yellow]")
    else:
        queue_manager = QueueManager(config)
        stats = queue_manager.get_queue_stats()

        if not stats:
            console.print("Queue is empty")
        else:
            table = Table()
            table.add_column("Status")
            table.add_column("Count", justify="right")

            for status, count in stats.items():
                status_str = status.replace("_", " ").title()
                table.add_row(status_str, str(count))

            console.print(table)


@cli.command()
@click.option("--systemd", is_flag=True, help="Running under systemd (internal)")
@click.pass_context
def start(ctx: click.Context, systemd: bool) -> None:
    """Start continuous processing daemon - auto-rip discs and process queue."""
    config: SpindleConfig = ctx.obj["config"]

    # Check system dependencies before starting - validate required only
    console.print("Checking system dependencies...")
    check_system_dependencies(validate_required=True)

    daemon = SpindleDaemon(config)

    if systemd or os.getenv("INVOCATION_ID"):
        daemon.start_systemd_mode()
    else:
        daemon.start_daemon()


@cli.command()
@click.pass_context
def stop(ctx: click.Context) -> None:
    """Stop running Spindle process."""
    # Find running spindle process
    process_info = ProcessManager.find_spindle_process()

    if not process_info:
        console.print("[yellow]Spindle is not running[/yellow]")
        return

    pid, mode = process_info
    console.print(f"[blue]Stopping Spindle {mode} mode (PID {pid})...[/blue]")

    if ProcessManager.stop_process(pid):
        console.print("[green]Spindle stopped[/green]")
    else:
        console.print(f"[red]Failed to stop Spindle process {pid}[/red]")
        sys.exit(1)


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
        with subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
            universal_newlines=True,
        ) as proc:
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
    table.add_column("Fingerprint")

    for item in items:
        title = item.disc_title or (
            item.source_path.name if item.source_path else "Unknown"
        )
        if item.media_info:
            title = str(item.media_info)

        fingerprint = item.disc_fingerprint or "-"
        if fingerprint != "-":
            fingerprint = fingerprint[:12]

        table.add_row(
            str(item.item_id),
            title,
            item.status.value.title(),
            item.created_at.strftime("%Y-%m-%d %H:%M"),
            fingerprint,
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
    notification_service = NotificationService(config)

    if notification_service.test_notifications():
        console.print("[green]Test notification sent successfully[/green]")
    else:
        console.print("[red]Failed to send test notification[/red]")


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
    table.add_column("Fingerprint")

    for item in queue_items:
        title = item.disc_title or (
            item.source_path.name if item.source_path else "Unknown"
        )
        if item.media_info:
            title = str(item.media_info)

        fingerprint = item.disc_fingerprint or "-"
        if fingerprint != "-":
            fingerprint = fingerprint[:12]

        table.add_row(
            str(item.item_id),
            title,
            item.status.value.title(),
            item.created_at.strftime("%Y-%m-%d %H:%M"),
            fingerprint,
        )

    return table


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


def main() -> None:
    """Entry point for the CLI."""
    cli()


if __name__ == "__main__":
    main()
