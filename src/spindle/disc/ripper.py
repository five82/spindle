"""MakeMKV integration for disc ripping."""

import logging
import re
import subprocess
from pathlib import Path
from typing import Callable, Union

from ..config import SpindleConfig

logger = logging.getLogger(__name__)


class Track:
    """Represents a track on the disc."""

    def __init__(self, track_id: str, track_type: str, codec: str,
                 language: str, duration: int, size: int,
                 title: Union[str, None] = None, is_default: bool = False):
        self.track_id = track_id
        self.track_type = track_type  # "video", "audio", "subtitle"
        self.codec = codec
        self.language = language
        self.duration = duration  # in seconds
        self.size = size  # in bytes
        self.title = title
        self.is_default = is_default

    def __str__(self) -> str:
        return f"{self.track_type} track {self.track_id}: {self.codec} ({self.language})"


class Title:
    """Represents a title on the disc."""

    def __init__(self, title_id: str, duration: int, size: int,
                 chapters: int, tracks: list[Track],
                 name: Union[str, None] = None):
        self.title_id = title_id
        self.duration = duration  # in seconds
        self.size = size  # in bytes
        self.chapters = chapters
        self.tracks = tracks
        self.name = name or f"Title {title_id}"

    @property
    def video_tracks(self) -> list[Track]:
        """Get video tracks."""
        return [t for t in self.tracks if t.track_type == "video"]

    @property
    def audio_tracks(self) -> list[Track]:
        """Get audio tracks."""
        return [t for t in self.tracks if t.track_type == "audio"]

    @property
    def subtitle_tracks(self) -> list[Track]:
        """Get subtitle tracks."""
        return [t for t in self.tracks if t.track_type == "subtitle"]

    def get_english_audio_tracks(self) -> list[Track]:
        """Get English audio tracks."""
        return [t for t in self.audio_tracks if t.language.lower().startswith("en")]

    def __str__(self) -> str:
        duration_str = f"{self.duration // 3600:02d}:{(self.duration % 3600) // 60:02d}:{self.duration % 60:02d}"
        return f"{self.name}: {duration_str}, {len(self.tracks)} tracks"


class MakeMKVRipper:
    """Interface to MakeMKV for disc ripping."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.makemkv_con = config.makemkv_con

    def scan_disc(self, device: Union[str, None] = None) -> list[Title]:
        """Scan disc and return available titles."""
        if device is None:
            device = self.config.optical_drive

        logger.info(f"Scanning disc on {device}")

        try:
            # Run makemkvcon to get disc info
            cmd = [self.makemkv_con, "info", f"dev:{device}", "--robot"]

            result = subprocess.run(
                cmd,
                check=False, capture_output=True,
                text=True,
                timeout=60,
            )

            if result.returncode != 0:
                raise RuntimeError(f"MakeMKV scan failed: {result.stderr}")

            return self._parse_makemkv_output(result.stdout)

        except subprocess.TimeoutExpired:
            raise RuntimeError("MakeMKV scan timed out")
        except subprocess.CalledProcessError as e:
            raise RuntimeError(f"MakeMKV scan failed: {e}")

    def _parse_makemkv_output(self, output: str) -> list[Title]:
        """Parse MakeMKV robot output to extract title information."""
        titles = {}

        for line in output.strip().split("\n"):
            if not line.startswith("TINFO:"):
                continue

            # Parse TINFO lines: TINFO:title_id,attr_id,value
            match = re.match(r"TINFO:(\d+),(\d+),(.+)", line)
            if not match:
                continue

            title_id, attr_id, value = match.groups()
            title_id = int(title_id)
            attr_id = int(attr_id)

            if title_id not in titles:
                titles[title_id] = {
                    "title_id": str(title_id),
                    "name": None,
                    "duration": 0,
                    "size": 0,
                    "chapters": 0,
                    "tracks": [],
                }

            # Map attribute IDs to title properties
            if attr_id == 2:  # Title name
                titles[title_id]["name"] = value.strip('"')
            elif attr_id == 9:  # Duration
                titles[title_id]["duration"] = self._parse_duration(value.strip('"'))
            elif attr_id == 10:  # Size
                titles[title_id]["size"] = int(value) if value.isdigit() else 0
            elif attr_id == 8:  # Chapter count
                titles[title_id]["chapters"] = int(value) if value.isdigit() else 0

        # Parse track information
        for line in output.strip().split("\n"):
            if not line.startswith("SINFO:"):
                continue

            # Parse SINFO lines: SINFO:title_id,stream_id,attr_id,value
            match = re.match(r"SINFO:(\d+),(\d+),(\d+),(.+)", line)
            if not match:
                continue

            title_id, stream_id, attr_id, value = match.groups()
            title_id = int(title_id)
            stream_id = int(stream_id)
            attr_id = int(attr_id)

            if title_id not in titles:
                continue

            # Find or create track
            track_info = None
            for track in titles[title_id]["tracks"]:
                if track.get("stream_id") == stream_id:
                    track_info = track
                    break

            if track_info is None:
                track_info = {
                    "stream_id": stream_id,
                    "track_id": str(stream_id),
                    "track_type": "unknown",
                    "codec": "",
                    "language": "",
                    "duration": 0,
                    "size": 0,
                    "title": None,
                    "is_default": False,
                }
                titles[title_id]["tracks"].append(track_info)

            # Map stream attributes
            if attr_id == 1:  # Stream type
                value = value.strip('"')
                if value == "Video":
                    track_info["track_type"] = "video"
                elif value == "Audio":
                    track_info["track_type"] = "audio"
                elif value == "Subtitles":
                    track_info["track_type"] = "subtitle"
            elif attr_id == 6:  # Codec
                track_info["codec"] = value.strip('"')
            elif attr_id == 3:  # Language
                track_info["language"] = value.strip('"')
            elif attr_id == 30:  # Name/Title
                track_info["title"] = value.strip('"')

        # Convert to Title objects
        title_objects = []
        for title_data in titles.values():
            tracks = []
            for track_data in title_data["tracks"]:
                track = Track(
                    track_id=track_data["track_id"],
                    track_type=track_data["track_type"],
                    codec=track_data["codec"],
                    language=track_data["language"],
                    duration=track_data["duration"],
                    size=track_data["size"],
                    title=track_data["title"],
                    is_default=track_data["is_default"],
                )
                tracks.append(track)

            title = Title(
                title_id=title_data["title_id"],
                duration=title_data["duration"],
                size=title_data["size"],
                chapters=title_data["chapters"],
                tracks=tracks,
                name=title_data["name"],
            )
            title_objects.append(title)

        return title_objects

    def _parse_duration(self, duration_str: str) -> int:
        """Parse duration string (HH:MM:SS) to seconds."""
        try:
            parts = duration_str.split(":")
            if len(parts) == 3:
                hours, minutes, seconds = map(int, parts)
                return hours * 3600 + minutes * 60 + seconds
        except ValueError:
            pass
        return 0

    def select_main_title(self, titles: list[Title]) -> Title | None:
        """Select the main title based on duration and other criteria."""
        if not titles:
            return None

        # Filter by minimum duration
        valid_titles = [
            t for t in titles
            if t.duration >= self.config.min_title_duration
        ]

        if not valid_titles:
            logger.warning("No titles meet minimum duration requirement")
            valid_titles = titles

        # Sort by duration (longest first) and return the longest
        valid_titles.sort(key=lambda t: t.duration, reverse=True)

        main_title = valid_titles[0]
        logger.info(f"Selected main title: {main_title}")

        return main_title

    def rip_title(self, title: Title, output_dir: Path,
                  device: Union[str, None] = None,
                  progress_callback: Union[Callable, None] = None) -> Path:
        """Rip a specific title to the output directory."""
        if device is None:
            device = self.config.optical_drive

        output_dir.mkdir(parents=True, exist_ok=True)

        # Generate output filename
        safe_name = re.sub(r"[^\w\s-]", "", title.name).strip()
        safe_name = re.sub(r"[-\s]+", "-", safe_name)
        output_file = output_dir / f"{safe_name}.mkv"

        logger.info(f"Ripping {title.name} to {output_file}")

        # Build MakeMKV command with track selection
        cmd = [
            self.makemkv_con, "mkv",
            f"dev:{device}",
            title.title_id,
            str(output_dir),
        ]

        # Add track selection based on requirements:
        # - Video track (main)
        # - English audio tracks (primary + commentary)
        # - No subtitles
        # - Chapters included by default

        try:
            result = subprocess.run(
                cmd,
                check=False, capture_output=True,
                text=True,
                timeout=3600,  # 1 hour timeout
            )

            if result.returncode != 0:
                raise RuntimeError(f"MakeMKV rip failed: {result.stderr}")

            # Find the actual output file (MakeMKV may change the name)
            ripped_files = list(output_dir.glob("*.mkv"))
            if not ripped_files:
                raise RuntimeError("No output file found after ripping")

            # Return the most recently created file
            output_file = max(ripped_files, key=lambda f: f.stat().st_mtime)

            logger.info(f"Successfully ripped to {output_file}")
            return output_file

        except subprocess.TimeoutExpired:
            raise RuntimeError("MakeMKV rip timed out")
        except subprocess.CalledProcessError as e:
            raise RuntimeError(f"MakeMKV rip failed: {e}")

    def rip_disc(self, output_dir: Path, device: Union[str, None] = None) -> Path:
        """Scan disc and rip the main title."""
        titles = self.scan_disc(device)
        main_title = self.select_main_title(titles)

        if not main_title:
            raise RuntimeError("No suitable title found on disc")

        return self.rip_title(main_title, output_dir, device)
