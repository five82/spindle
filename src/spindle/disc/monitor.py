"""Disc detection and monitoring using udev and watchdog."""

import logging
import subprocess
import time
from collections.abc import Callable
from pathlib import Path

from watchdog.events import FileSystemEventHandler
from watchdog.observers import Observer

logger = logging.getLogger(__name__)


class DiscInfo:
    """Information about detected disc."""

    def __init__(self, device: str, disc_type: str, label: str | None = None):
        self.device = device
        self.disc_type = disc_type
        self.label = label or "Unknown"
        self.detected_at = time.time()

    def __str__(self) -> str:
        return f"{self.disc_type} disc '{self.label}' on {self.device}"


class DiscMonitor:
    """Monitor for disc insertion events."""

    def __init__(
        self,
        device: str = "/dev/sr0",
        callback: Callable[[DiscInfo], None] | None = None,
    ):
        self.device = device
        self.callback = callback
        self.observer: Observer | None = None
        self.is_monitoring = False

    def start_monitoring(self) -> None:
        """Start monitoring for disc insertion events."""
        if self.is_monitoring:
            logger.warning("Already monitoring for disc events")
            return

        logger.info(f"Starting disc monitoring on {self.device}")

        # Set up filesystem watcher for the device
        device_path = Path(self.device).parent
        if device_path.exists():
            event_handler = DiscEventHandler(self.device, self._on_disc_detected)
            self.observer = Observer()
            self.observer.schedule(event_handler, str(device_path), recursive=False)
            self.observer.start()
            self.is_monitoring = True
        else:
            logger.error(f"Device path {device_path} does not exist")

    def stop_monitoring(self) -> None:
        """Stop monitoring for disc events."""
        if self.observer:
            self.observer.stop()
            self.observer.join()
            self.is_monitoring = False
            logger.info("Stopped disc monitoring")

    def _on_disc_detected(self, disc_info: DiscInfo) -> None:
        """Handle detected disc."""
        logger.info(f"Detected: {disc_info}")
        if self.callback:
            self.callback(disc_info)

    def check_for_disc(self) -> DiscInfo | None:
        """Check if there's currently a disc in the drive."""
        return detect_disc(self.device)


class DiscEventHandler(FileSystemEventHandler):
    """File system event handler for disc detection."""

    def __init__(self, device: str, callback: Callable[[DiscInfo], None]):
        self.device = device
        self.callback = callback
        self.last_check = 0

    def on_modified(self, event) -> None:
        """Handle file system modification events."""
        if event.src_path == self.device:
            # Debounce events - only check once per 2 seconds
            current_time = time.time()
            if current_time - self.last_check > 2:
                self.last_check = current_time
                disc_info = detect_disc(self.device)
                if disc_info:
                    self.callback(disc_info)


def detect_disc(device: str = "/dev/sr0") -> DiscInfo | None:
    """Detect if a disc is present and get its information."""
    try:
        # Use lsblk to check if the device has media
        result = subprocess.run(
            ["lsblk", "-no", "LABEL,FSTYPE", device],
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )

        if result.returncode == 0 and result.stdout.strip():
            # Parse the output
            output = result.stdout.strip()
            if output:
                parts = output.split()
                label = parts[0] if parts and parts[0] != "" else None
                fstype = parts[1] if len(parts) > 1 else "unknown"

                # Determine disc type based on filesystem or other detection
                disc_type = determine_disc_type(device, fstype)

                return DiscInfo(device, disc_type, label)

    except subprocess.TimeoutExpired:
        logger.warning(f"Timeout checking device {device}")
    except subprocess.CalledProcessError as e:
        logger.debug(f"No disc detected on {device}: {e}")
    except Exception as e:
        logger.error(f"Error detecting disc on {device}: {e}")

    return None


def determine_disc_type(device: str, fstype: str) -> str:
    """Determine the type of disc (DVD, Blu-ray, etc.)."""
    try:
        # Try to use blkid for more detailed information
        result = subprocess.run(
            ["blkid", "-p", "-s", "TYPE", device],
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )

        if result.returncode == 0:
            output = result.stdout.lower()
            if "udf" in output:
                # UDF is common for Blu-ray and modern DVDs
                # Try to detect Blu-ray vs DVD by checking for specific files
                return detect_bluray_vs_dvd(device)
            if "iso9660" in output:
                return "DVD"

    except Exception as e:
        logger.debug(f"Error determining disc type: {e}")

    # Fallback to filesystem type
    if fstype.lower() == "udf":
        return detect_bluray_vs_dvd(device)
    if fstype.lower() == "iso9660":
        return "DVD"

    return "Unknown"


def detect_bluray_vs_dvd(device: str) -> str:
    """Attempt to distinguish between Blu-ray and DVD."""
    try:
        # Try to mount and check for Blu-ray structure
        # This is a simplified check - in practice, you might want to use
        # more sophisticated detection methods
        result = subprocess.run(
            ["file", "-s", device],
            check=False,
            capture_output=True,
            text=True,
            timeout=5,
        )

        if result.returncode == 0:
            output = result.stdout.lower()
            # Look for Blu-ray indicators
            if "blu-ray" in output or "bdav" in output or "bdmv" in output:
                return "Blu-ray"

    except Exception as e:
        logger.debug(f"Error detecting Blu-ray vs DVD: {e}")

    # Default to DVD if we can't determine
    return "DVD"


def eject_disc(device: str = "/dev/sr0") -> bool:
    """Eject the disc from the drive."""
    try:
        result = subprocess.run(
            ["eject", device],
            check=False,
            capture_output=True,
            text=True,
            timeout=10,
        )

        if result.returncode == 0:
            logger.info(f"Successfully ejected disc from {device}")
            return True
        logger.error(f"Failed to eject disc: {result.stderr}")
        return False

    except Exception as e:
        logger.error(f"Error ejecting disc: {e}")
        return False


def wait_for_disc_removal(device: str = "/dev/sr0", timeout: int = 30) -> bool:
    """Wait for disc to be removed from drive."""
    start_time = time.time()

    while time.time() - start_time < timeout:
        if detect_disc(device) is None:
            return True
        time.sleep(1)

    return False
