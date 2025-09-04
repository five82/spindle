"""Simplified error handling for Spindle."""

import logging
import shutil

logger = logging.getLogger(__name__)


class SpindleError(Exception):
    """Base exception for Spindle."""


class ToolError(SpindleError):
    """External tool execution error (MakeMKV, drapto, etc)."""


class MediaError(SpindleError):
    """Media/disc reading error."""


class HardwareError(SpindleError):
    """Hardware/drive error."""


class ConfigurationError(SpindleError):
    """Configuration error."""


def check_dependencies() -> list[str]:
    """Check for missing dependencies and return list of missing tools."""
    missing = []

    # Check for MakeMKV
    if not shutil.which("makemkvcon"):
        missing.append("makemkvcon (MakeMKV)")
        logger.warning("MakeMKV not found - install from https://makemkv.com/")

    # Check for drapto (if encoding is enabled)
    if not shutil.which("drapto"):
        missing.append("drapto")
        logger.warning(
            "drapto not found - install with: cargo install --git https://github.com/five82/drapto",
        )

    return missing
