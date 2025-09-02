"""Enhanced disc metadata extraction from multiple sources."""

import logging
import re
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from defusedxml import ElementTree

from spindle.disc.ripper import Title

logger = logging.getLogger(__name__)


@dataclass
class EnhancedDiscMetadata:
    """Enhanced disc metadata from multiple sources."""

    # bd_info data
    volume_id: str | None = None
    disc_name: str | None = None
    provider: str | None = None
    disc_type_info: dict | None = None

    # bdmt_eng.xml data
    bdmt_title: str | None = None
    language: str | None = None
    thumbnails: list[str] | None = None

    # MakeMKV data
    makemkv_label: str | None = None
    titles: list[Title] | None = None

    # mcmf.xml data
    studio: str | None = None
    studio_url: str | None = None
    content_id: str | None = None

    def get_best_title_candidates(self) -> list[str]:
        """Get title candidates in priority order."""
        candidates = []

        # Priority 1: bd_info disc library metadata
        if self.disc_name and not self._is_generic_label(self.disc_name):
            candidates.append(self.disc_name)

        # Priority 2: bdmt_eng.xml title
        if self.bdmt_title and not self._is_generic_label(self.bdmt_title):
            candidates.append(self.bdmt_title)

        # Priority 3: cleaned volume ID
        if self.volume_id:
            cleaned = self._clean_volume_id(self.volume_id)
            if cleaned and not self._is_generic_label(cleaned):
                candidates.append(cleaned)

        # Priority 4: MakeMKV label (often generic)
        if self.makemkv_label and not self._is_generic_label(self.makemkv_label):
            candidates.append(self.makemkv_label)

        return candidates

    def _clean_volume_id(self, volume_id: str) -> str | None:
        """Clean volume identifier for title extraction."""
        if not volume_id:
            return None

        # Remove common patterns
        title = volume_id
        title = re.sub(r"^\d+_", "", title)  # Remove leading numbers (00000095_)
        title = re.sub(
            r"_S\d+_DISC_\d+$",
            "",
            title,
            flags=re.IGNORECASE,
        )  # Remove season/disc suffix
        title = re.sub(r"_TV$", "", title, flags=re.IGNORECASE)  # Remove TV suffix
        title = title.replace("_", " ")

        return title.strip() if title.strip() else None

    def _is_generic_label(self, label: str) -> bool:
        """Check if disc label is too generic for identification."""
        if not label:
            return True

        generic_patterns = [
            "LOGICAL_VOLUME_ID",
            "DVD_VIDEO",
            "BLURAY",
            "BD_ROM",
            "UNTITLED",
            r"^\d+$",  # Just numbers
            r"^[A-Z0-9_]{1,3}$",  # Very short codes
        ]

        for pattern in generic_patterns:
            if re.match(pattern, label, re.IGNORECASE):
                return True

        return False

    def is_tv_series(self) -> bool:
        """Detect if this is a TV series disc."""
        if self.disc_type_info and self.disc_type_info.get("is_tv"):
            return True

        # Check volume ID for TV patterns
        if self.volume_id:
            tv_patterns = [
                r"_S\d+_DISC_\d+",
                r"_TV_",
                r"SEASON_\d+",
                r"_SERIES_",
            ]
            for pattern in tv_patterns:
                if re.search(pattern, self.volume_id, re.IGNORECASE):
                    return True

        # Check disc name for TV patterns
        if self.disc_name:
            tv_indicators = ["Season", "Series", "TV", "Episode"]
            for indicator in tv_indicators:
                if indicator.lower() in self.disc_name.lower():
                    return True

        return False

    def get_season_disc_info(self) -> tuple[int | None, int | None]:
        """Extract season and disc number if this is a TV series."""
        season = None
        disc = None

        if self.disc_type_info:
            season = self.disc_type_info.get("season")
            disc = self.disc_type_info.get("disc")

        # Fallback: parse from disc name
        if not season and self.disc_name:
            season_match = re.search(r"Season\s+(\d+)", self.disc_name, re.IGNORECASE)
            if season_match:
                season = int(season_match.group(1))

            disc_match = re.search(r"Disc\s+(\d+)", self.disc_name, re.IGNORECASE)
            if disc_match:
                disc = int(disc_match.group(1))

        return season, disc


class EnhancedDiscMetadataExtractor:
    """Extract metadata from multiple disc sources with bd_info integration."""

    def __init__(self, config=None) -> None:
        self.logger = logging.getLogger(__name__)
        self.config = config

    def extract_all_metadata(
        self,
        disc_path: Path,
        device_path: str | None = None,
    ) -> EnhancedDiscMetadata:
        """Extract metadata from all available sources."""
        metadata = EnhancedDiscMetadata()

        # PRIORITY 1: bd_info command (most reliable for Blu-ray)
        bd_info_data = self.run_bd_info(disc_path, device_path)
        if bd_info_data:
            metadata.volume_id = bd_info_data.get("volume_identifier")
            metadata.disc_name = bd_info_data.get("disc_name")
            metadata.provider = bd_info_data.get("provider_data")
            if metadata.volume_id:
                metadata.disc_type_info = self.parse_disc_type(metadata.volume_id)

        # PRIORITY 2: bdmt_eng.xml (when present)
        bdmt_path = disc_path / "BDMV" / "META" / "DL" / "bdmt_eng.xml"
        if bdmt_path.exists():
            bdmt_data = self.parse_bdmt_metadata(bdmt_path)
            if bdmt_data:
                metadata.bdmt_title = bdmt_data.get("title")
                metadata.language = bdmt_data.get("language")
                metadata.thumbnails = bdmt_data.get("thumbnails", [])

        # PRIORITY 3: MakeMKV info output (handled separately in ripper)
        # This will be populated by the caller who has the MakeMKV output

        # PRIORITY 4: mcmf.xml for studio info
        mcmf_path = disc_path / "AACS" / "mcmf.xml"
        if mcmf_path.exists():
            mcmf_data = self.parse_mcmf(mcmf_path)
            if mcmf_data:
                metadata.studio_url = mcmf_data.get("uri")
                metadata.studio = self.extract_studio_from_url(
                    metadata.studio_url or "",
                )
                metadata.content_id = mcmf_data.get("content_id")

        return metadata

    def run_bd_info(self, disc_path: Path, device_path: str | None = None) -> dict:
        """Run bd_info command and parse output."""
        try:
            # Use raw device path if provided (better for volume identifier),
            # otherwise fall back to mount path
            bd_info_path = device_path if device_path else str(disc_path)
            self.logger.info(f"Running bd_info scan on {bd_info_path}")

            start_time = time.time()
            result = subprocess.run(
                ["bd_info", bd_info_path],
                check=False,
                capture_output=True,
                text=True,
                timeout=self.config.bd_info_timeout if self.config else 300,
            )

            scan_duration = time.time() - start_time
            self.logger.info(f"bd_info scan completed in {scan_duration:.1f}s")
            if result.returncode == 0:
                return self.parse_bd_info_output(result.stdout)
            self.logger.warning(f"bd_info command failed: {result.stderr}")
            return {}
        except subprocess.TimeoutExpired:
            self.logger.warning("bd_info command timed out")
            return {}
        except FileNotFoundError:
            self.logger.info("bd_info command not available (install libbluray-utils)")
            return {}
        except Exception as e:
            self.logger.warning(f"bd_info command failed: {e}")
            return {}

    def parse_bd_info_output(self, output: str) -> dict:
        """Parse bd_info command output for key metadata."""
        data = {}
        for raw_line in output.split("\n"):
            line = raw_line.strip()
            if "Volume Identifier" in line and ":" in line:
                data["volume_identifier"] = line.split(":", 1)[1].strip()
            elif "Disc name" in line and ":" in line:
                data["disc_name"] = line.split(":", 1)[1].strip()
            elif "provider data" in line and ":" in line:
                provider = line.split(":", 1)[1].strip().strip("'").strip()
                if provider and provider != "":
                    data["provider_data"] = provider
        return data

    def parse_disc_type(self, volume_id: str) -> dict:
        """Parse volume identifier for TV series indicators."""
        if not volume_id:
            return {"is_tv": False}

        # TV series patterns
        tv_patterns = [
            r"S(\d+)_DISC_(\d+)",  # BATMAN_TV_S1_DISC_1
            r"SEASON_(\d+)_DISC_(\d+)",
            r"_S(\d+)_D(\d+)",
            r"_S(\d+)_DISC_(\d+)",
        ]

        for pattern in tv_patterns:
            match = re.search(pattern, volume_id, re.IGNORECASE)
            if match:
                return {
                    "is_tv": True,
                    "season": (
                        int(match.group(1))
                        if match.lastindex and match.lastindex >= 1
                        else None
                    ),
                    "disc": (
                        int(match.group(2))
                        if match.lastindex and match.lastindex >= 2
                        else None
                    ),
                }

        return {"is_tv": False}

    def parse_bdmt_metadata(self, bdmt_path: Path) -> dict | None:
        """Parse bdmt_eng.xml metadata file."""
        try:
            tree = ElementTree.parse(bdmt_path)
            root = tree.getroot()

            if root is None:
                return None

            data: dict[str, Any] = {}

            # Extract disc title
            title_element = root.find(
                ".//{urn:BDA:bdmv;discinfo}title/{urn:BDA:bdmv;discinfo}name",
            )
            if title_element is not None and title_element.text:
                data["title"] = title_element.text.strip()

            # Extract language
            lang_element = root.find(".//{urn:BDA:bdmv;discinfo}language")
            if lang_element is not None and lang_element.text:
                data["language"] = lang_element.text.strip()

            # Extract thumbnails
            thumbnails = []
            for thumb_element in root.findall(".//{urn:BDA:bdmv;discinfo}thumbnail"):
                href = thumb_element.get("href")
                if href:
                    thumbnails.append(href)
            data["thumbnails"] = thumbnails

            return data

        except ElementTree.ParseError as e:
            self.logger.warning(f"Failed to parse bdmt_eng.xml: {e}")
            return None
        except Exception as e:
            self.logger.warning(f"Unexpected error parsing bdmt_eng.xml: {e}")
            return None

    def parse_mcmf(self, mcmf_path: Path) -> dict | None:
        """Parse mcmf.xml for studio information."""
        try:
            tree = ElementTree.parse(mcmf_path)
            root = tree.getroot()

            if root is None:
                return None

            data = {}

            # Extract content ID
            content_id = root.get("contentID")
            if content_id:
                data["content_id"] = content_id

            # Extract first URI (studio URL)
            uri_element = root.find(".//URI")
            if uri_element is not None and uri_element.text:
                data["uri"] = uri_element.text.strip()

            return data

        except ElementTree.ParseError as e:
            self.logger.warning(f"Failed to parse mcmf.xml: {e}")
            return None
        except Exception as e:
            self.logger.warning(f"Unexpected error parsing mcmf.xml: {e}")
            return None

    def extract_studio_from_url(self, url: str) -> str | None:
        """Extract studio name from mcmf URI."""
        if not url:
            return None

        # Studio mappings from real-world examples
        studio_mappings = {
            "sonypictures.com": "Sony Pictures",
            "warnerbros.com": "Warner Bros",
            "universalstudios.com": "Universal",
            "disney.com": "Disney",
            "paramount.com": "Paramount",
            "mgm.com": "MGM",
            "foxmovies.com": "Fox",
            "lionsgate.com": "Lionsgate",
        }

        url_lower = url.lower()
        for domain, studio in studio_mappings.items():
            if domain in url_lower:
                return studio

        # Try to extract domain name as fallback
        try:
            from urllib.parse import urlparse

            parsed = urlparse(url)
            domain = parsed.netloc.lower()
            # Remove common prefixes
            domain = re.sub(r"^www\.", "", domain)
            # Extract main domain name
            domain_parts = domain.split(".")
            if len(domain_parts) >= 2:
                return domain_parts[0].title()
        except Exception as e:
            self.logger.debug(f"Failed to extract studio from URL: {url}: {e}")

        return None

    def _is_generic_label(self, label: str) -> bool:
        """Check if disc label is too generic for identification."""
        if not label:
            return True

        generic_patterns = [
            "LOGICAL_VOLUME_ID",
            "DVD_VIDEO",
            "BLURAY",
            "BD_ROM",
            "UNTITLED",
            r"^\d+$",  # Just numbers
            r"^[A-Z0-9_]{1,3}$",  # Very short codes
        ]

        for pattern in generic_patterns:
            if re.match(pattern, label, re.IGNORECASE):
                return True

        return False

    def populate_makemkv_data(
        self,
        metadata: EnhancedDiscMetadata,
        makemkv_label: str,
        titles: list[Title],
    ) -> EnhancedDiscMetadata:
        """Populate MakeMKV data into existing metadata."""
        metadata.makemkv_label = makemkv_label
        metadata.titles = titles
        return metadata

    def parse_makemkv_info_output(self, makemkv_output: str) -> dict:
        """Parse MakeMKV info output for content identification."""
        data: dict[str, Any] = {}

        for raw_line in makemkv_output.split("\n"):
            line = raw_line.strip()
            if line.startswith("CINFO:"):
                # Parse CINFO lines: CINFO:type,flags,"value"
                parts = line.split(",", 2)
                if len(parts) >= 3:
                    cinfo_type = parts[0].split(":")[1]
                    value = parts[2].strip('"')

                    # CINFO types from MakeMKV documentation:
                    # 2 = disc name/title
                    # 30 = volume name
                    # 32 = volume ID
                    if (
                        cinfo_type == "2"
                        and value
                        and not self._is_generic_label(value)
                    ):
                        data["disc_title"] = value
                    elif (
                        cinfo_type == "30"
                        and value
                        and not self._is_generic_label(value)
                    ):
                        data["volume_name"] = value
                    elif cinfo_type == "32" and value:
                        data["volume_id"] = value

            elif line.startswith("TINFO:") and ",2," in line:
                # Parse title names: TINFO:title,2,0,"Title Name"
                parts = line.split(",", 3)
                if len(parts) >= 4:
                    title_name = parts[3].strip('"')
                    if title_name and not self._is_generic_label(title_name):
                        if "title_names" not in data:
                            data["title_names"] = []
                        data["title_names"].append(title_name)

        return data

    def populate_makemkv_data_from_output(
        self,
        metadata: EnhancedDiscMetadata,
        makemkv_output: str,
        titles: list[Title],
    ) -> EnhancedDiscMetadata:
        """Populate MakeMKV data from actual command output."""
        metadata.titles = titles

        # Parse the MakeMKV output for identification data
        makemkv_data = self.parse_makemkv_info_output(makemkv_output)

        # If we don't have better sources, use MakeMKV data
        if not metadata.disc_name and makemkv_data.get("disc_title"):
            metadata.disc_name = makemkv_data["disc_title"]

        if not metadata.volume_id and makemkv_data.get("volume_id"):
            metadata.volume_id = makemkv_data["volume_id"]
        elif not metadata.volume_id and makemkv_data.get("volume_name"):
            metadata.volume_id = makemkv_data["volume_name"]

        # Store the raw makemkv label for fallback
        if makemkv_data.get("volume_id"):
            metadata.makemkv_label = makemkv_data["volume_id"]

        return metadata
