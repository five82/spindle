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
