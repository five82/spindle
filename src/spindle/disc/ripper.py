"""MakeMKV integration for disc ripping."""

import logging
import re
import subprocess
import time
from collections.abc import Callable
from pathlib import Path
from typing import TYPE_CHECKING

from spindle.config import SpindleConfig
from spindle.error_handling import MediaError, ToolError

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
        *,
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
        hours = self.duration // 3600
        minutes = (self.duration % 3600) // 60
        seconds = self.duration % 60
        duration_str = f"{hours:02d}:{minutes:02d}:{seconds:02d}"
        return f"{self.name}: {duration_str}, {len(self.tracks)} tracks"


class MakeMKVRipper:
    """Interface to MakeMKV for disc ripping."""

    def __init__(self, config: SpindleConfig):
        self.config = config
        self.makemkv_con = config.makemkv_con
        self._last_progress_percent = -1.0  # Track last reported progress
        self._progress_report_threshold = 5  # Only report every 5% change
        self._ripping_started = False  # Track when actual ripping begins

    def scan_disc(self, device: str | None = None) -> list[Title]:
        """Scan disc and return available titles."""
        titles, _ = self.scan_disc_with_output(device)
        return titles

    def scan_disc_with_output(
        self,
        device: str | None = None,
    ) -> tuple[list[Title], str]:
        """Scan disc and return both titles and raw MakeMKV output."""
        if device is None:
            device = self.config.optical_drive

        logger.info(f"Scanning disc on {device}")

        try:
            # Run makemkvcon to get disc info
            cmd = [self.makemkv_con, "info", f"dev:{device}", "--robot"]

            # Show progress during long scan
            start_time = time.time()

            result = subprocess.run(
                cmd,
                check=False,
                capture_output=True,
                text=True,
                timeout=self.config.makemkv_info_timeout,
            )

            scan_duration = time.time() - start_time
            logger.info(f"MakeMKV scan completed in {scan_duration:.1f}s")

            if result.returncode != 0:
                error_msg = result.stderr or result.stdout
                # Try to extract a clean error message from MakeMKV output
                clean_error = self._extract_makemkv_error(error_msg)
                msg = "MakeMKV"
                msg = f"{msg} failed with exit code {result.returncode}: {clean_error}"
                raise ToolError(
                    msg,
                )

            raw_output = result.stdout
            titles = self._parse_makemkv_output(raw_output)
            return titles, raw_output

        except subprocess.TimeoutExpired:
            msg = "MakeMKV"
            msg = f"{msg} disc scan operation timed out"
            raise ToolError(msg)
        except subprocess.CalledProcessError as e:
            msg = "MakeMKV"
            msg = f"{msg} failed with exit code {e.returncode}: {e}"
            raise ToolError(msg)

    def _extract_makemkv_error(self, output: str) -> str:
        """Extract a clean error message from MakeMKV MSG output."""
        for line in output.strip().split("\n"):
            if line.startswith("MSG:"):
                # Parse MSG lines: MSG:code,flags,count,"text",...
                parts = line.split(",", 4)
                if len(parts) >= 4:
                    text = parts[3].strip('"')
                    # Return the first meaningful error message
                    if (
                        "too old" in text.lower()
                        or "registration key" in text.lower()
                        or "failed" in text.lower()
                        or "error" in text.lower()
                    ):
                        return text
        # If no specific error found, return the original output
        return output.strip()

    def _build_selection_rule(self) -> str:
        """Build MakeMKV selection rule based on configuration."""
        rules = [
            "-sel:all",  # Deselect everything first
            "+sel:video",  # Always include video tracks
        ]

        # Audio track selection based on config
        if self.config.include_all_english_audio:
            rules.append("+sel:audio&(eng)")  # All English audio

            if not self.config.include_commentary_tracks:
                # Try to exclude commentary tracks
                rules.append("-sel:audio&(commentary)")
                # Also try common commentary indicators
                rules.append("-sel:audio&(director)")
                rules.append("-sel:audio&(cast)")
        else:
            # Just main English audio (try to exclude commentary)
            rules.append("+sel:audio&(eng)&(!commentary)")

        # Include alternate audio if requested
        if self.config.include_alternate_audio:
            rules.append("+sel:audio&(!eng)")  # Non-English audio

        # No subtitles by default (matches current logic)
        rules.append("-sel:subtitle")

        return ",".join(rules)

    def _configure_makemkv_selection(self, selection_rule: str) -> None:
        """Configure MakeMKV selection rule via settings file."""
        # MakeMKV settings file location
        settings_file = Path.home() / ".MakeMKV" / "settings.conf"

        # Ensure directory exists
        settings_file.parent.mkdir(exist_ok=True)

        # Read existing settings
        settings = {}
        if settings_file.exists():
            with open(settings_file) as f:
                for line in f:
                    if "=" in line and not line.strip().startswith("#"):
                        key, value = line.strip().split("=", 1)
                        settings[key.strip()] = value.strip().strip('"')

        # Update selection rule
        settings["app_DefaultSelectionString"] = selection_rule

        # Write settings back
        with open(settings_file, "w") as f:
            f.write("# MakeMKV settings file (managed by Spindle)\n")
            for key, value in settings.items():
                f.write(f'{key} = "{value}"\n')

        logger.debug(f"Configured MakeMKV selection rule: {selection_rule}")

    def _parse_makemkv_output(self, output: str) -> list[Title]:
        """Parse MakeMKV robot output to extract title information."""
        # Check for MakeMKV error messages first
        for line in output.strip().split("\n"):
            if line.startswith("MSG:"):
                # Parse MSG lines: MSG:code,flags,count,"text",...
                parts = line.split(",", 4)
                if len(parts) >= 4:
                    parts[0].split(":")[1]  # Extract code after MSG:
                    text = parts[3].strip('"')
                    # Check for common error conditions
                    if "too old" in text.lower() or "registration key" in text.lower():
                        msg = "MakeMKV"
                        msg = f"{msg} license error: {text}. Update MakeMKV or purchase a license key from makemkv.com"
                        raise ToolError(
                            msg,
                        )
                    if "failed" in text.lower() or "error" in text.lower():
                        # Provide user-friendly error messages based on common issues
                        if "copy protection" in text.lower() or "aacs" in text.lower():
                            msg = "Disc is copy protected and cannot be read"
                            solution = "This disc has copy protection that MakeMKV cannot bypass. Try a different disc or update your MakeMKV license."
                        elif "no disc" in text.lower() or "not found" in text.lower():
                            msg = "No readable disc found in drive"
                            solution = "Make sure the disc is properly inserted and the drive door is closed. Try ejecting and reinserting the disc."
                        elif (
                            "read error" in text.lower() or "i/o error" in text.lower()
                        ):
                            msg = "Disc read error - disc may be damaged or dirty"
                            solution = "Clean the disc with a soft cloth. If the problem persists, the disc may be damaged."
                        elif "timeout" in text.lower():
                            msg = "Disc scanning timed out - drive may be slow or disc is complex"
                            solution = "Try again, or increase the makemkv_info_timeout setting in your config."
                        else:
                            msg = "MakeMKV could not read this disc"
                            solution = "Check disc quality, drive compatibility, and ensure MakeMKV is up to date. See logs for technical details."

                        msg = f"{msg}: {text}. {solution}"
                        raise ToolError(msg)

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

            # Parse SINFO lines: SINFO:title_id,stream_id,attr_id,type_code,"type_name"
            match = re.match(r"SINFO:(\d+),(\d+),(\d+),(\d+),(.+)", line)
            if not match:
                continue

            title_id, stream_id, attr_id, type_code, value = match.groups()
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
                else:
                    logger.warning(
                        f"Unknown track type: '{value}' for title {title_id}, stream {stream_id}",
                    )
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

            # Log track parsing summary at debug level
            audio_tracks = [t for t in tracks if t.track_type == "audio"]
            logger.debug(
                f"Parsed title {title_data['title_id']}: {len(tracks)} total tracks, {len(audio_tracks)} audio tracks",
            )

        return title_objects

    def _parse_duration(self, duration_str: str) -> int:
        """Parse duration string (HH:MM:SS) to seconds."""
        try:
            # Clean up the duration string - remove any leading numbers and quotes
            # MakeMKV sometimes returns format like '0,"1:39:03' instead of '1:39:03'
            clean_duration = duration_str
            if ',"' in clean_duration:
                # Extract the time part after the ',"' prefix
                clean_duration = clean_duration.split(',"')[1]

            # Remove any remaining quotes
            clean_duration = clean_duration.strip('"')

            parts = clean_duration.split(":")
            if len(parts) == 3:
                hours, minutes, seconds = map(int, parts)
                return hours * 3600 + minutes * 60 + seconds
            logger.warning(
                f"Invalid duration format: '{duration_str}' -> '{clean_duration}' (expected HH:MM:SS)",
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

            IntelligentDiscAnalyzer(self.config)

            # Create a simple DiscInfo for analysis
            from .monitor import DiscInfo

            DiscInfo(
                device=self.config.optical_drive,
                disc_type="unknown",  # Don't have type info here
                label=disc_label,
            )

            # Note: analyze_disc is async, but we can't await here since this is sync
            # This is a design issue that needs to be resolved
            # For now, we'll handle this differently

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

        if content_type == ContentType.MOVIE:
            # Movies: Use movie_min_duration, allow extras if configured
            min_duration = self.config.movie_min_duration * 60  # Convert to seconds
            max_extra_duration = (
                self.config.max_extras_duration * 60
                if self.config.include_extras
                else 0
            )

            for title in titles:
                if title.duration >= min_duration or (
                    self.config.include_extras and title.duration >= max_extra_duration
                ):
                    valid_titles.append(title)

        elif content_type == ContentType.TV_SERIES:
            # TV Series: Handle both regular episodes and cartoon shorts
            # Use broader range to accommodate both TV episodes and cartoon shorts
            if self.config.allow_short_content:
                # Allow cartoon shorts (2-15 min) and regular episodes (15-90 min)
                min_duration = self.config.cartoon_min_duration * 60
                max_duration = max(
                    self.config.cartoon_max_duration * 60,
                    self.config.tv_episode_max_duration * 60,
                )
            else:
                # Standard TV episode duration only
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
        """Select tracks to include in ripping based on configuration."""
        selected_tracks = []

        # Always include all video tracks
        selected_tracks.extend(title.video_tracks)

        # Handle audio track selection based on configuration
        if self.config.include_all_english_audio:
            # Include all English audio tracks
            english_audio = title.get_all_english_audio_tracks()

            if self.config.include_commentary_tracks:
                # Include all English audio (main + commentary)
                selected_tracks.extend(english_audio)
            else:
                # Include only main English audio, exclude commentary
                main_audio = title.get_main_audio_tracks()
                selected_tracks.extend(main_audio)
        else:
            # Include only main English audio tracks
            main_audio = title.get_main_audio_tracks()
            selected_tracks.extend(main_audio)

        # Include alternate language audio if requested
        if self.config.include_alternate_audio:
            # Add non-English audio tracks
            for track in title.audio_tracks:
                if (
                    not track.language.lower().startswith("en")
                    and track not in selected_tracks
                ):
                    selected_tracks.append(track)

        # Remove duplicates while preserving order
        seen_ids = set()
        deduplicated_tracks = []
        for track in selected_tracks:
            if track.track_id not in seen_ids:
                seen_ids.add(track.track_id)
                deduplicated_tracks.append(track)

        return deduplicated_tracks

    def _parse_makemkv_progress(self, line: str) -> dict | None:
        """Parse MakeMKV progress output lines."""
        # MakeMKV has two progress formats:
        # 1. Robot mode: PRGV:current,total,max
        # 2. Regular mode: Current progress - X% , Total progress - Y%

        # Try robot format first
        if line.startswith("PRGV:"):
            try:
                # Parse PRGV line: PRGV:current,total,max
                # current: current file progress (0-65536)
                # total: total progress (0-65536)
                # max: maximum value (always 65536)
                parts = line[5:].split(",", 2)  # Skip "PRGV:" prefix
                if len(parts) >= 3:
                    current = int(parts[0]) if parts[0].isdigit() else 0
                    total = int(parts[1]) if parts[1].isdigit() else 0
                    maximum = int(parts[2]) if parts[2].isdigit() else 65536

                    if maximum > 0:
                        # Use total progress for overall percentage
                        # Ignore lines where current is complete but total hasn't started
                        # This happens when MakeMKV reports individual track completion
                        if current == maximum and total == 0:
                            return None

                        percentage = (total / maximum) * 100

                        # Filter out progress reports before actual ripping starts
                        if not self._ripping_started:
                            return None

                        # Filter out early 100% reports - these are often initialization artifacts
                        # Only allow 100% if we've seen some incremental progress first
                        if percentage == 100.0 and self._last_progress_percent < 95.0:
                            return None

                        # Filter out duplicate/minor updates
                        # Only report if progress changed significantly and moving forward
                        if (
                            percentage >= self._last_progress_percent
                            and percentage - self._last_progress_percent
                            >= self._progress_report_threshold
                        ):
                            self._last_progress_percent = percentage
                            return {
                                "type": "ripping_progress",
                                "stage": "Saving to MKV file",
                                "current": total,
                                "maximum": maximum,
                                "percentage": percentage,
                            }
            except (ValueError, IndexError) as e:
                logger.debug(f"Failed to parse PRGV line '{line}': {e}")

        # Try regular progress format
        elif "Current progress" in line and "Total progress" in line:
            try:
                # Parse: Current progress - 17% , Total progress - 17%
                match = re.search(
                    r"Current progress - (\d+)%.*Total progress - (\d+)%",
                    line,
                )
                if match:
                    current_percent = int(match.group(1))
                    total_percent = int(match.group(2))
                    return {
                        "type": "ripping_progress",
                        "stage": "Saving to MKV file",
                        "current": current_percent,
                        "maximum": 100,
                        "percentage": total_percent,  # Use total progress
                    }
            except (ValueError, IndexError) as e:
                logger.debug(f"Failed to parse progress line '{line}': {e}")

        # Parse action messages
        elif line.startswith("Current action:"):
            action = line.replace("Current action:", "").strip()
            return {
                "type": "ripping_status",
                "message": action,
            }
        elif line.startswith("Current operation:"):
            operation = line.replace("Current operation:", "").strip()
            return {
                "type": "ripping_status",
                "message": operation,
            }

        # MakeMKV status messages: MSG:code,flags,count,message,...
        elif line.startswith("MSG:"):
            try:
                parts = line.split(",", 4)
                if len(parts) >= 4:
                    code = parts[0].split(":")[1]
                    message = parts[3].strip('"')
                    return {
                        "type": "ripping_status",
                        "code": code,
                        "message": message,
                    }
            except (ValueError, IndexError) as e:
                logger.debug(f"Failed to parse message line '{line}': {e}")

        return None

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

        # Clean up any existing MakeMKV output files to avoid overwrite prompts
        # MakeMKV creates files like title_t00.mkv, title_t01.mkv, etc.
        for existing_file in output_dir.glob("title_t*.mkv"):
            logger.debug(f"Removing existing MakeMKV output file: {existing_file}")
            existing_file.unlink()

        logger.info(f"Ripping {title.name} to {output_file}")

        # Reset progress tracking for new rip
        self._last_progress_percent = -1.0
        self._ripping_started = False  # Reset ripping flag

        # Configure MakeMKV selection rules based on our config
        selection_rule = self._build_selection_rule()
        self._configure_makemkv_selection(selection_rule)
        logger.debug(f"Using selection rule: {selection_rule}")

        # Build MakeMKV command
        cmd = [
            self.makemkv_con,
            "mkv",
            "--noscan",  # Skip initial scan since we already did that
            "--robot",  # Use robot mode for structured PRGV output
            f"dev:{device}",
            title.title_id,
            str(output_dir),
        ]

        # Add progress flag if callback is provided
        if progress_callback:
            cmd.append("--progress=-same")

        try:
            if progress_callback:
                # Use Popen for real-time progress monitoring
                process = subprocess.Popen(
                    cmd,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.STDOUT,
                    text=True,
                    universal_newlines=True,
                )

                # Monitor progress in real-time
                stdout_lines = []
                while True:
                    output = process.stdout.readline() if process.stdout else ""
                    if output == "" and process.poll() is not None:
                        break
                    if output:
                        stdout_lines.append(output.strip())
                        # Log all MakeMKV output for debugging
                        if output.strip():
                            logger.debug(f"MakeMKV output: {output.strip()}")
                            # Check if actual ripping has started
                            if "Saving" in output and "titles into directory" in output:
                                self._ripping_started = True
                                logger.debug("Actual ripping has started")
                        # Parse progress information
                        progress_data = self._parse_makemkv_progress(output.strip())
                        if progress_data:
                            logger.debug(f"Parsed progress: {progress_data}")
                            progress_callback(progress_data)

                # Wait for process to complete
                return_code = process.poll()
                stdout_output = "\n".join(stdout_lines)

                if return_code != 0:
                    msg = "MakeMKV"
                    msg = f"{msg} failed with exit code {return_code}: {stdout_output}"
                    raise ToolError(
                        msg,
                    )
            else:
                # Use subprocess.run for compatibility (tests)
                result = subprocess.run(
                    cmd,
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=self.config.makemkv_rip_timeout,
                )

                if result.returncode != 0:
                    msg = "MakeMKV"
                    msg = f"{msg} failed with exit code {result.returncode}: {result.stderr}"
                    raise ToolError(
                        msg,
                    )

            # Find the actual output file (MakeMKV may change the name)
            ripped_files = list(output_dir.glob("*.mkv"))
            if not ripped_files:
                msg = "No files were ripped from the disc"
                raise MediaError(msg)

            # Return the most recently created file
            output_file = max(ripped_files, key=lambda f: f.stat().st_mtime)

            logger.info(f"Successfully ripped to {output_file}")
            return output_file

        except subprocess.TimeoutExpired:
            msg = "MakeMKV"
            msg = f"{msg} ripping operation timed out after {self.config.makemkv_rip_timeout}s"
            raise ToolError(
                msg,
            )
        except subprocess.CalledProcessError as e:
            msg = "MakeMKV"
            msg = f"{msg} failed with exit code {e.returncode}: {e}"
            raise ToolError(msg)

    def rip_disc(self, output_dir: Path, device: str | None = None) -> Path:
        """Scan disc and rip the main title."""
        titles = self.scan_disc(device)
        # Try to get disc label for intelligent analysis
        disc_label = self._get_disc_label(device)
        main_title = self.select_main_title(titles, disc_label)

        if not main_title:
            msg = "No suitable main title found on disc"
            raise MediaError(msg)

        return self.rip_title(main_title, output_dir, device)
