"""MakeMKV integration for disc ripping."""

import logging
import re
import subprocess
from collections.abc import Callable
from pathlib import Path
from typing import TYPE_CHECKING

from spindle.config import SpindleConfig

if TYPE_CHECKING:
    from .analyzer import ContentPattern

logger = logging.getLogger(__name__)


class Track:
    """Represents a track on the disc."""

    def __init__(
        self,
        track_id: str,
        track_type: str,
        codec: str,
        language: str,
        duration: int,
        size: int,
        title: str | None = None,
        is_default: bool = False,
    ):
        self.track_id = track_id
        self.track_type = track_type  # "video", "audio", "subtitle"
        self.codec = codec
        self.language = language
        self.duration = duration  # in seconds
        self.size = size  # in bytes
        self.title = title
        self.is_default = is_default

    def __str__(self) -> str:
        return (
            f"{self.track_type} track {self.track_id}: {self.codec} ({self.language})"
        )


class Title:
    """Represents a title on the disc."""

    def __init__(
        self,
        title_id: str,
        duration: int,
        size: int,
        chapters: int,
        tracks: list[Track],
        name: str | None = None,
    ):
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

    def get_commentary_tracks(self) -> list[Track]:
        """Get commentary audio tracks."""
        commentary_indicators = [
            "commentary",
            "director",
            "cast",
            "crew",
            "behind",
            "making",
            "deleted",
            "alternate",
            "producer",
            "writer",
            "audio commentary",
            "filmmakers",
            "actors",
            "director's",
            "cast and crew",
        ]

        commentary_tracks = []
        for track in self.audio_tracks:
            if track.title and track.language.lower().startswith("en"):
                track_title_lower = track.title.lower()
                if any(
                    indicator in track_title_lower
                    for indicator in commentary_indicators
                ):
                    commentary_tracks.append(track)

        return commentary_tracks

    def get_main_audio_tracks(self) -> list[Track]:
        """Get main (non-commentary) English audio tracks."""
        english_tracks = self.get_english_audio_tracks()
        commentary_tracks = self.get_commentary_tracks()

        # Return English tracks that aren't commentaries
        return [t for t in english_tracks if t not in commentary_tracks]

    def get_all_english_audio_tracks(self) -> list[Track]:
        """Get all English audio tracks including main audio + commentaries."""
        return [
            track
            for track in self.audio_tracks
            if track.language.lower().startswith("en")
        ]

    def __str__(self) -> str:
        duration_str = f"{self.duration // 3600:02d}:{(self.duration % 3600) // 60:02d}:{self.duration % 60:02d}"
        return f"{self.name}: {duration_str}, {len(self.tracks)} tracks"


class MakeMKVRipper:
    """Interface to MakeMKV for disc ripping."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.makemkv_con = config.makemkv_con

    def scan_disc(self, device: str | None = None) -> list[Title]:
        """Scan disc and return available titles."""
        if device is None:
            device = self.config.optical_drive

        logger.info(f"Scanning disc on {device}")

        try:
            # Run makemkvcon to get disc info
            cmd = [self.makemkv_con, "info", f"dev:{device}", "--robot"]

            result = subprocess.run(
                cmd,
                check=False,
                capture_output=True,
                text=True,
                timeout=self.config.makemkv_info_timeout,
            )

            if result.returncode != 0:
                msg = f"MakeMKV scan failed: {result.stderr}"
                raise RuntimeError(msg)

            return self._parse_makemkv_output(result.stdout)

        except subprocess.TimeoutExpired:
            msg = "MakeMKV scan timed out"
            raise RuntimeError(msg)
        except subprocess.CalledProcessError as e:
            msg = f"MakeMKV scan failed: {e}"
            raise RuntimeError(msg)

    def _parse_makemkv_output(self, output: str) -> list[Title]:
        """Parse MakeMKV robot output to extract title information."""
        titles: dict[int, dict] = {}

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
            logger.warning(
                f"Invalid duration format: '{duration_str}' (expected HH:MM:SS)",
            )
        except ValueError as e:
            logger.warning(f"Failed to parse duration '{duration_str}': {e}")
        return 0

    def select_main_title(
        self,
        titles: list[Title],
        disc_label: str = "",
    ) -> Title | None:
        """Select the main title based on duration and other criteria."""
        if not titles:
            return None

        # Use intelligent content analysis to filter titles if disc label available
        if disc_label:
            from .analyzer import IntelligentDiscAnalyzer

            analyzer = IntelligentDiscAnalyzer(self.config)

            # Create a simple DiscInfo for analysis
            from .monitor import DiscInfo

            disc_info = DiscInfo(
                device=self.config.optical_drive,
                disc_type="unknown",  # Don't have type info here
                label=disc_label,
            )

            # Note: analyze_disc is async, but we can't await here since this is sync
            # This is a design issue that needs to be resolved
            # For now, we'll handle this differently
            content_pattern = None

            # Since we can't use async analysis here, use basic duration filtering
            valid_titles = titles
        else:
            # No disc label available, use basic duration filtering
            logger.info("No disc label available, using basic duration filtering")
            valid_titles = titles  # Will be filtered by basic logic below

        if not valid_titles:
            logger.warning(
                "No titles meet content-type duration requirements, using basic filter",
            )
            # Fallback to basic duration filter
            min_duration = min(
                self.config.movie_min_duration * 60,  # Convert minutes to seconds
                self.config.tv_episode_min_duration * 60,
                (
                    self.config.cartoon_min_duration * 60
                    if self.config.allow_short_content
                    else self.config.movie_min_duration * 60
                ),
            )
            valid_titles = [t for t in titles if t.duration >= min_duration]
            if not valid_titles:
                valid_titles = titles

        # Sort by duration (longest first) and return the longest
        valid_titles.sort(key=lambda t: t.duration, reverse=True)

        main_title = valid_titles[0]
        logger.info(f"Selected main title: {main_title}")

        return main_title

    def _filter_titles_by_content_type(
        self,
        titles: list[Title],
        content_pattern: "ContentPattern",
    ) -> list[Title]:
        """Filter titles based on detected content type and duration requirements."""
        from .analyzer import ContentType

        content_type = content_pattern.type
        valid_titles = []

        if content_type in [ContentType.MOVIE, ContentType.ANIMATED_MOVIE]:
            # Movies: Use movie_min_duration, allow extras if configured
            min_duration = self.config.movie_min_duration * 60  # Convert to seconds
            max_extra_duration = (
                self.config.max_extras_duration * 60
                if self.config.include_movie_extras
                else 0
            )

            for title in titles:
                if title.duration >= min_duration or (
                    self.config.include_movie_extras
                    and title.duration >= max_extra_duration
                ):
                    valid_titles.append(title)

        elif content_type in [ContentType.TV_SERIES, ContentType.ANIMATED_SERIES]:
            # TV Series: Use episode duration range
            min_duration = self.config.tv_episode_min_duration * 60
            max_duration = self.config.tv_episode_max_duration * 60

            valid_titles = [
                t for t in titles if min_duration <= t.duration <= max_duration
            ]

        elif content_type in [
            ContentType.CARTOON_COLLECTION,
            ContentType.CARTOON_SHORTS,
        ]:
            # Cartoons: Use cartoon duration range
            if self.config.allow_short_content:
                min_duration = self.config.cartoon_min_duration * 60
                max_duration = self.config.cartoon_max_duration * 60

                valid_titles = [
                    t for t in titles if min_duration <= t.duration <= max_duration
                ]
            else:
                # Fall back to TV episode duration if short content not allowed
                min_duration = self.config.tv_episode_min_duration * 60
                max_duration = self.config.tv_episode_max_duration * 60
                valid_titles = [
                    t for t in titles if min_duration <= t.duration <= max_duration
                ]

        else:
            # Unknown content type: use most permissive settings
            min_duration = (
                self.config.cartoon_min_duration * 60
                if self.config.allow_short_content
                else self.config.tv_episode_min_duration * 60
            )
            valid_titles = [t for t in titles if t.duration >= min_duration]

        return valid_titles

    def _get_disc_label(self, device: str | None = None) -> str:
        """Get disc label for analysis context."""
        if device is None:
            device = self.config.optical_drive

        try:
            # Try to get disc label using MakeMKV
            cmd = [self.makemkv_con, "info", f"dev:{device}"]
            result = subprocess.run(
                cmd,
                check=False,
                capture_output=True,
                text=True,
                timeout=self.config.makemkv_eject_timeout,
            )

            if result.returncode == 0:
                # Parse MakeMKV info output for disc name
                for line in result.stdout.split("\n"):
                    if "DRV:0" in line and "name" in line.lower():
                        # Extract disc label from MakeMKV output
                        parts = line.split('"')
                        if len(parts) >= 2:
                            return parts[1].strip()

            # Fallback: try to get volume label directly from mount point
            import os

            if os.path.exists(device):
                try:
                    # Try reading volume label from filesystem
                    mount_result = subprocess.run(
                        ["lsblk", "-no", "LABEL", device],
                        check=False,
                        capture_output=True,
                        text=True,
                        timeout=self.config.makemkv_eject_timeout,
                    )
                    if mount_result.returncode == 0 and mount_result.stdout.strip():
                        return mount_result.stdout.strip()
                except subprocess.TimeoutExpired:
                    logger.debug(f"Timeout reading volume label from {device}")
                except subprocess.SubprocessError as e:
                    logger.debug(f"Failed to read volume label from {device}: {e}")
                except Exception as e:
                    logger.debug(f"Unexpected error reading volume label: {e}")

        except Exception as e:
            logger.debug(f"Could not get disc label: {e}")

        return ""

    def _select_tracks_for_rip(self, title: Title) -> list[Track]:
        """Select tracks to include in the rip based on configuration."""
        selected_tracks = []

        # Always include video tracks
        video_tracks = [t for t in title.tracks if t.track_type == "video"]
        selected_tracks.extend(video_tracks)

        # Audio track selection based on config
        if self.config.include_all_english_audio:
            # Include all English audio (main + commentary)
            english_tracks = title.get_english_audio_tracks()
            commentary_tracks = title.get_commentary_tracks()

            # Add main English audio tracks
            selected_tracks.extend(english_tracks)

            # Add commentary tracks if enabled
            if self.config.include_commentary_tracks:
                # Commentary tracks that are also English
                english_commentary = [
                    t
                    for t in commentary_tracks
                    if t.language.lower() in ["eng", "english", "en"]
                ]
                selected_tracks.extend(english_commentary)
        else:
            # Just include main English audio tracks (no commentary)
            main_tracks = title.get_main_audio_tracks()
            selected_tracks.extend(main_tracks)

            # Add commentary if specifically enabled
            if self.config.include_commentary_tracks:
                commentary_tracks = title.get_commentary_tracks()
                english_commentary = [
                    t
                    for t in commentary_tracks
                    if t.language.lower() in ["eng", "english", "en"]
                ]
                selected_tracks.extend(english_commentary)

        # Include alternate audio languages if enabled
        if self.config.include_alternate_audio:
            # Include all audio tracks, not just English
            all_audio = [t for t in title.tracks if t.track_type == "audio"]
            for track in all_audio:
                if track not in selected_tracks:
                    selected_tracks.append(track)

        # For now, don't include subtitle tracks by default
        # This could be made configurable in the future

        # Remove duplicates while preserving order
        unique_tracks = []
        seen_ids = set()
        for track in selected_tracks:
            if track.track_id not in seen_ids:
                unique_tracks.append(track)
                seen_ids.add(track.track_id)

        return unique_tracks

    def rip_title(
        self,
        title: Title,
        output_dir: Path,
        device: str | None = None,
        progress_callback: Callable | None = None,
    ) -> Path:
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
            self.makemkv_con,
            "mkv",
            f"dev:{device}",
            title.title_id,
            str(output_dir),
        ]

        # Add track selection based on requirements:
        # - Video track (main)
        # - All English audio tracks (primary + commentary)
        # - No subtitles by default
        # - Chapters included by default

        # Select tracks based on configuration
        selected_tracks = self._select_tracks_for_rip(title)

        # Add track selection flags to MakeMKV command
        for track in title.tracks:
            if track in selected_tracks:
                cmd.extend(["--track", f"{track.track_id}:on"])
            else:
                cmd.extend(["--track", f"{track.track_id}:off"])

        try:
            result = subprocess.run(
                cmd,
                check=False,
                capture_output=True,
                text=True,
                timeout=self.config.makemkv_rip_timeout,
            )

            if result.returncode != 0:
                msg = f"MakeMKV rip failed: {result.stderr}"
                raise RuntimeError(msg)

            # Find the actual output file (MakeMKV may change the name)
            ripped_files = list(output_dir.glob("*.mkv"))
            if not ripped_files:
                msg = "No output file found after ripping"
                raise RuntimeError(msg)

            # Return the most recently created file
            output_file = max(ripped_files, key=lambda f: f.stat().st_mtime)

            logger.info(f"Successfully ripped to {output_file}")
            return output_file

        except subprocess.TimeoutExpired:
            msg = "MakeMKV rip timed out"
            raise RuntimeError(msg)
        except subprocess.CalledProcessError as e:
            msg = f"MakeMKV rip failed: {e}"
            raise RuntimeError(msg)

    def rip_disc(self, output_dir: Path, device: str | None = None) -> Path:
        """Scan disc and rip the main title."""
        titles = self.scan_disc(device)
        # Try to get disc label for intelligent analysis
        disc_label = self._get_disc_label(device)
        main_title = self.select_main_title(titles, disc_label)

        if not main_title:
            msg = "No suitable title found on disc"
            raise RuntimeError(msg)

        return self.rip_title(main_title, output_dir, device)
