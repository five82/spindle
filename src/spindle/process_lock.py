"""Modern process management without PID files."""

import fcntl
import os
import subprocess
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from spindle.config import SpindleConfig


class ProcessLock:
    """Manages single instance locking and process discovery."""

    def __init__(self, config: "SpindleConfig") -> None:
        self.lock_file = config.log_dir / "spindle.lock"
        self.lock_fd: int | None = None

    def acquire(self) -> bool:
        """Try to acquire exclusive lock. Returns True if successful."""
        try:
            self.lock_file.parent.mkdir(parents=True, exist_ok=True)
            self.lock_fd = os.open(str(self.lock_file), os.O_CREAT | os.O_WRONLY)
            fcntl.flock(self.lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            # Write our PID to the lock file for informational purposes
            os.write(self.lock_fd, str(os.getpid()).encode())
            os.fsync(self.lock_fd)
            return True
        except OSError:
            if self.lock_fd is not None:
                os.close(self.lock_fd)
                self.lock_fd = None
            return False

    def release(self) -> None:
        """Release the lock."""
        if self.lock_fd is not None:
            try:
                fcntl.flock(self.lock_fd, fcntl.LOCK_UN)
                os.close(self.lock_fd)
            except OSError:
                pass
            finally:
                self.lock_fd = None

    @staticmethod
    def find_spindle_process() -> tuple[int, str] | None:
        """Find running spindle process. Returns (pid, mode) or None."""
        try:
            # Use pgrep to find spindle start processes
            result = subprocess.run(
                ["pgrep", "-f", "spindle start", "-a"],
                check=False,
                capture_output=True,
                text=True,
                timeout=2,
            )

            if result.returncode == 0 and result.stdout.strip():
                for line in result.stdout.strip().split("\n"):
                    parts = line.split(maxsplit=1)
                    if len(parts) >= 2:
                        pid = int(parts[0])
                        cmdline = parts[1]

                        # Skip our own process and parent processes
                        current_pid = os.getpid()
                        parent_pid = os.getppid()
                        if pid in (current_pid, parent_pid):
                            continue

                        # Skip shell processes running the command (look for actual Python processes)
                        # Check for shell executables, not just substring matches
                        if (
                            "/zsh" in cmdline
                            or "zsh -" in cmdline
                            or "/bash" in cmdline
                            or "bash -" in cmdline
                            or "/sh -" in cmdline
                            or cmdline.endswith("/sh")
                        ):
                            continue

                        # Must contain python or uv run to be a real spindle process
                        if not ("python" in cmdline or "uv run" in cmdline):
                            continue

                        # Determine mode from command line
                        if "--systemd" in cmdline:
                            return (pid, "systemd")
                        return (pid, "daemon")  # Default for new daemon-only mode

        except (subprocess.SubprocessError, ValueError, FileNotFoundError):
            pass

        return None

    @staticmethod
    def is_process_running(pid: int) -> bool:
        """Check if a process with given PID is running."""
        try:
            os.kill(pid, 0)
            return True
        except (OSError, ProcessLookupError):
            return False

    @staticmethod
    def stop_process(pid: int) -> bool:
        """Stop a process gracefully, then forcefully if needed."""
        import signal
        import time

        try:
            # Send SIGTERM for graceful shutdown
            os.kill(pid, signal.SIGTERM)

            # Wait up to 10 seconds for graceful shutdown
            for _ in range(10):
                if not ProcessLock.is_process_running(pid):
                    return True
                time.sleep(1)

            # Force kill if still running
            os.kill(pid, signal.SIGKILL)
            time.sleep(0.5)
            return not ProcessLock.is_process_running(pid)

        except (OSError, ProcessLookupError):
            return True  # Process already stopped
