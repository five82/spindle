"""Enhanced error handling system for better user experience."""

import logging
import sys
from enum import Enum
from pathlib import Path

from rich.console import Console

logger = logging.getLogger(__name__)
console = Console()


class ErrorCategory(Enum):
    """Categories of errors for better user experience."""

    CONFIGURATION = "configuration"
    DEPENDENCY = "dependency"
    HARDWARE = "hardware"
    NETWORK = "network"
    FILESYSTEM = "filesystem"
    MEDIA = "media"
    EXTERNAL_TOOL = "external_tool"
    SYSTEM = "system"
    USER_INPUT = "user_input"


class SpindleError(Exception):
    """Base exception for Spindle with enhanced user experience."""

    def __init__(
        self,
        message: str,
        category: ErrorCategory,
        *,
        solution: str | None = None,
        details: str | None = None,
        recoverable: bool = True,
        log_level: int = logging.ERROR,
        original_error: Exception | None = None,
    ):
        super().__init__(message)
        self.message = message
        self.category = category
        self.solution = solution
        self.details = details
        self.recoverable = recoverable
        self.log_level = log_level
        self.original_error = original_error

    def display_to_user(self) -> None:
        """Display error to user with helpful context."""
        # Choose appropriate emoji and color for category
        category_styles = {
            ErrorCategory.CONFIGURATION: ("⚙️", "yellow"),
            ErrorCategory.DEPENDENCY: ("📦", "red"),
            ErrorCategory.HARDWARE: ("🔌", "red"),
            ErrorCategory.NETWORK: ("🌐", "orange"),
            ErrorCategory.FILESYSTEM: ("📁", "red"),
            ErrorCategory.MEDIA: ("💿", "blue"),
            ErrorCategory.EXTERNAL_TOOL: ("🔧", "red"),
            ErrorCategory.SYSTEM: ("💻", "red"),
            ErrorCategory.USER_INPUT: ("⌨️", "yellow"),
        }

        emoji, color = category_styles.get(self.category, ("❌", "red"))

        # Display main error message
        console.print(
            f"\n{emoji} [{color}bold]{self.category.value.title()} Error[/{color}bold]",
        )
        console.print(f"[{color}]{self.message}[/{color}]")

        # Show additional details if available
        if self.details:
            console.print(f"\n[dim]Details:[/dim] {self.details}")

        # Show solution if available
        if self.solution:
            console.print(f"\n[green]💡 Solution:[/green] {self.solution}")

        # Show if recoverable
        if self.recoverable:
            console.print(
                "\n[dim]This error may be temporary. You can try again.[/dim]",
            )
        else:
            console.print(
                "\n[dim]This error requires intervention before continuing.[/dim]",
            )

        # Log the error appropriately
        if self.original_error:
            logger.log(
                self.log_level,
                "%s: %s",
                self.category.value,
                self.message,
                exc_info=self.original_error,
            )
        else:
            logger.log(self.log_level, "%s: %s", self.category.value, self.message)


class ConfigurationError(SpindleError):
    """Configuration-related errors."""

    def __init__(self, message: str, *, config_path: Path | None = None, **kwargs):
        solution = kwargs.pop("solution", None)
        if not solution and config_path:
            solution = f"Check your configuration file at {config_path}"
        super().__init__(
            message,
            ErrorCategory.CONFIGURATION,
            solution=solution,
            **kwargs,
        )


class DependencyError(SpindleError):
    """Missing or broken dependency errors."""

    def __init__(
        self,
        dependency: str,
        *,
        install_command: str | None = None,
        **kwargs,
    ):
        message = f"Required dependency '{dependency}' is not available"
        solution = kwargs.pop("solution", None)
        if not solution and install_command:
            solution = f"Install with: {install_command}"
        super().__init__(
            message,
            ErrorCategory.DEPENDENCY,
            solution=solution,
            recoverable=False,
            **kwargs,
        )


class HardwareError(SpindleError):
    """Hardware-related errors."""

    def __init__(self, message: str, **kwargs):
        solution = kwargs.pop(
            "solution",
            "Check disc is inserted properly and drive is accessible",
        )
        super().__init__(message, ErrorCategory.HARDWARE, solution=solution, **kwargs)


class MediaError(SpindleError):
    """Media/disc-related errors."""

    def __init__(self, message: str, **kwargs):
        solution = kwargs.pop(
            "solution",
            "Try cleaning the disc or using a different disc",
        )
        super().__init__(message, ErrorCategory.MEDIA, solution=solution, **kwargs)


class ExternalToolError(SpindleError):
    """External tool execution errors."""

    def __init__(
        self,
        tool: str,
        exit_code: int | None = None,
        stderr: str | None = None,
        **kwargs,
    ):
        message = f"{tool} failed"
        if exit_code is not None:
            message += f" with exit code {exit_code}"

        details = kwargs.pop("details", stderr)
        solution = kwargs.pop(
            "solution",
            f"Check {tool} is properly installed and configured",
        )

        super().__init__(
            message,
            ErrorCategory.EXTERNAL_TOOL,
            details=details,
            solution=solution,
            **kwargs,
        )


def handle_error(
    error: Exception,
    *,
    category: ErrorCategory | None = None,
    **kwargs,
) -> None:
    """Convert generic exceptions to SpindleError and display to user."""
    if isinstance(error, SpindleError):
        error.display_to_user()
        return

    # Convert common exceptions to SpindleError
    if category is None:
        if isinstance(error, FileNotFoundError | PermissionError):
            category = ErrorCategory.FILESYSTEM
        elif isinstance(error, ConnectionError | TimeoutError):
            category = ErrorCategory.NETWORK
        else:
            category = ErrorCategory.SYSTEM

    # Create SpindleError from generic exception
    spindle_error = SpindleError(
        message=str(error) or "An unexpected error occurred",
        category=category,
        original_error=error,
        **kwargs,
    )
    spindle_error.display_to_user()


def check_dependencies() -> list[DependencyError]:
    """Check for missing dependencies and return list of errors."""
    errors = []

    # Check for uv
    import shutil

    if not shutil.which("uv"):
        errors.append(
            DependencyError(
                "uv",
                install_command="curl -LsSf https://astral.sh/uv/install.sh | sh",
                details="Spindle requires uv package manager for dependency management",
            ),
        )

    # Check for MakeMKV
    if not shutil.which("makemkvcon"):
        errors.append(
            DependencyError(
                "MakeMKV",
                solution="Install MakeMKV from https://makemkv.com/ or your package manager",
                details="MakeMKV is required for disc ripping",
            ),
        )

    # Check for drapto (if encoding is enabled)
    if not shutil.which("drapto"):
        errors.append(
            DependencyError(
                "drapto",
                install_command="cargo install --git https://github.com/five82/drapto",
                details="drapto is required for AV1 encoding. Install Rust first if needed.",
            ),
        )

    return errors


def graceful_exit(exit_code: int = 1) -> None:
    """Exit gracefully with helpful message."""
    if exit_code == 0:
        console.print("\n[green]✨ Spindle completed successfully[/green]")
    else:
        console.print("\n[red]Spindle encountered errors and had to stop[/red]")
        console.print("[dim]Check the logs above for details on what went wrong[/dim]")
        console.print(
            "[dim]Run 'spindle config validate' to check your configuration[/dim]",
        )

    sys.exit(exit_code)


def with_error_handling(category: ErrorCategory):
    """Decorator to add consistent error handling to functions."""

    def decorator(func):
        def wrapper(*args, **kwargs):
            try:
                return func(*args, **kwargs)
            except SpindleError:
                raise  # Re-raise SpindleError as-is
            except Exception as e:
                handle_error(e, category=category)
                raise

        return wrapper

    return decorator
