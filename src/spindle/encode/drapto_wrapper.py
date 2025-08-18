"""Wrapper for drapto encoding functionality."""

import json
import logging
import subprocess
import threading
from collections.abc import Callable
from pathlib import Path
from typing import Any

from ..config import SpindleConfig

logger = logging.getLogger(__name__)


class EncodeResult:
    """Result of an encoding operation."""

    def __init__(self, success: bool, input_file: Path, output_file: Path | None = None,
                 error_message: str | None = None,
                 input_size: int = 0, output_size: int = 0,
                 duration: float = 0.0):
        self.success = success
        self.input_file = input_file
        self.output_file = output_file
        self.error_message = error_message
        self.input_size = input_size
        self.output_size = output_size
        self.duration = duration

    @property
    def size_reduction_percent(self) -> float:
        """Calculate size reduction percentage."""
        if self.input_size == 0:
            return 0.0
        return ((self.input_size - self.output_size) / self.input_size) * 100

    def __str__(self) -> str:
        if self.success:
            return f"Encoded {self.input_file.name} -> {self.output_file.name} ({self.size_reduction_percent:.1f}% reduction)"
        return f"Failed to encode {self.input_file.name}: {self.error_message}"


class DraptoEncoder:
    """Wrapper for drapto video encoder."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.drapto_binary = config.drapto_binary

    def encode_file(self, input_file: Path, output_dir: Path,
                   progress_callback: Callable[[dict[str, Any]], None] | None = None) -> EncodeResult:
        """Encode a single video file using drapto."""

        if not input_file.exists():
            return EncodeResult(
                success=False,
                input_file=input_file,
                error_message="Input file does not exist",
            )

        # Ensure output directory exists
        output_dir.mkdir(parents=True, exist_ok=True)

        # Get input file size
        input_size = input_file.stat().st_size

        logger.info(f"Starting encode of {input_file.name}")

        try:
            # Build drapto command
            cmd = self._build_drapto_command(input_file, output_dir)

            if progress_callback:
                progress_callback({
                    "type": "initialization",
                    "message": f"Starting encode: {input_file.name}",
                    "input_file": str(input_file)
                })

            # Run drapto with streaming JSON progress
            result = self._run_drapto_with_progress(cmd, progress_callback)

            if result.returncode != 0:
                error_msg = result.stderr or result.stdout or "Unknown error"
                logger.error(f"Drapto encoding failed: {error_msg}")
                return EncodeResult(
                    success=False,
                    input_file=input_file,
                    error_message=error_msg,
                    input_size=input_size,
                )

            # Find the output file
            output_file = self._find_output_file(input_file, output_dir)
            if not output_file:
                return EncodeResult(
                    success=False,
                    input_file=input_file,
                    error_message="Output file not found after encoding",
                    input_size=input_size,
                )

            output_size = output_file.stat().st_size

            logger.info(f"Successfully encoded {input_file.name} -> {output_file.name}")

            if progress_callback:
                progress_callback({
                    "type": "completed",
                    "message": f"Completed encode: {output_file.name}",
                    "output_file": str(output_file),
                    "size_reduction_percent": ((input_size - output_size) / input_size * 100) if input_size > 0 else 0
                })

            return EncodeResult(
                success=True,
                input_file=input_file,
                output_file=output_file,
                input_size=input_size,
                output_size=output_size,
            )

        except subprocess.CalledProcessError as e:
            error_msg = f"Drapto process failed: {e}"
            logger.error(error_msg)
            return EncodeResult(
                success=False,
                input_file=input_file,
                error_message=error_msg,
                input_size=input_size,
            )
        except Exception as e:
            error_msg = f"Unexpected error during encoding: {e}"
            logger.error(error_msg)
            return EncodeResult(
                success=False,
                input_file=input_file,
                error_message=error_msg,
                input_size=input_size,
            )

    def _build_drapto_command(self, input_file: Path, output_dir: Path) -> list[str]:
        """Build the drapto command line."""
        cmd = [
            self.config.drapto_binary,
            "encode",
            "-i", str(input_file),
            "-o", str(output_dir),
            "--verbose",  # Enable verbose output for better logging
            "--json-progress",  # Enable structured JSON progress output
        ]

        # Add quality settings based on resolution detection
        # We'll let drapto auto-detect resolution and apply appropriate settings
        cmd.extend(["--quality-hd", str(self.config.drapto_quality_hd)])
        cmd.extend(["--quality-uhd", str(self.config.drapto_quality_uhd)])

        # Set encoding preset
        cmd.extend(["--preset", str(self.config.drapto_preset)])

        # Add ntfy notifications if configured
        if self.config.ntfy_topic:
            cmd.extend(["--ntfy", self.config.ntfy_topic])

        return cmd

    def _find_output_file(self, input_file: Path, output_dir: Path) -> Path | None:
        """Find the output file created by drapto."""
        # Drapto typically creates output files with the same base name
        # but with .mkv extension

        expected_name = input_file.stem + ".mkv"
        expected_path = output_dir / expected_name

        if expected_path.exists():
            return expected_path

        # If exact match not found, look for any .mkv files created recently
        mkv_files = list(output_dir.glob("*.mkv"))
        if mkv_files:
            # Return the most recently modified file
            return max(mkv_files, key=lambda f: f.stat().st_mtime)

        return None

    def _run_drapto_with_progress(self, cmd: list[str], 
                                  progress_callback: Callable[[dict[str, Any]], None] | None = None) -> subprocess.CompletedProcess:
        """Run drapto command and parse JSON progress output in real-time."""
        
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,  # Line buffered
            universal_newlines=True
        )
        
        stdout_lines = []
        
        def read_output():
            """Read and parse JSON progress from stdout."""
            while True:
                line = process.stdout.readline()
                if not line:
                    break
                    
                line = line.strip()
                stdout_lines.append(line)
                
                # Try to parse as JSON progress event
                if line.startswith('{') and progress_callback:
                    try:
                        progress_data = json.loads(line)
                        if isinstance(progress_data, dict) and 'type' in progress_data:
                            progress_callback(progress_data)
                    except json.JSONDecodeError:
                        # Not a JSON progress line, ignore
                        pass
        
        # Start reading output in background thread
        output_thread = threading.Thread(target=read_output)
        output_thread.start()
        
        # Wait for process to complete
        returncode = process.wait()
        output_thread.join()
        
        # Return a CompletedProcess-like object
        class CompletedProcessResult:
            def __init__(self, returncode: int, stdout_lines: list[str]):
                self.returncode = returncode
                self.stdout = '\n'.join(stdout_lines)
                self.stderr = ''
        
        return CompletedProcessResult(returncode, stdout_lines)

    def encode_batch(self, input_files: list[Path], output_dir: Path,
                    progress_callback: Callable[[dict[str, Any]], None] | None = None) -> list[EncodeResult]:
        """Encode multiple files in sequence."""
        results = []

        for i, input_file in enumerate(input_files, 1):
            if progress_callback:
                progress_callback({
                    "type": "batch_progress",
                    "message": f"Processing file {i}/{len(input_files)}: {input_file.name}",
                    "current_file": i,
                    "total_files": len(input_files),
                    "filename": input_file.name
                })

            result = self.encode_file(input_file, output_dir, progress_callback)
            results.append(result)

            if not result.success:
                logger.warning(f"Failed to encode {input_file.name}: {result.error_message}")
            else:
                logger.info(f"Successfully encoded {input_file.name}")

        return results

    def get_drapto_version(self) -> str | None:
        """Get the version of drapto being used."""
        try:
            result = subprocess.run(
                [self.config.drapto_binary, "--version"],
                check=False, capture_output=True,
                text=True,
                timeout=10,
            )

            if result.returncode == 0:
                return result.stdout.strip()
        except Exception as e:
            logger.warning(f"Could not get drapto version: {e}")

        return None

    def check_drapto_availability(self) -> bool:
        """Check if drapto is available and working."""
        try:
            result = subprocess.run(
                [self.config.drapto_binary, "--help"],
                check=False, capture_output=True,
                text=True,
                timeout=10,
            )

            return result.returncode == 0
        except Exception as e:
            logger.error(f"Drapto not available: {e}")
            return False
