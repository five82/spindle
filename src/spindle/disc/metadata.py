"""BDMV metadata parsing for UPC/barcode extraction."""

import logging
import re
from dataclasses import dataclass
from pathlib import Path
from xml.etree.ElementTree import Element

from defusedxml import ElementTree

logger = logging.getLogger(__name__)


@dataclass
class DiscMetadata:
    """Disc metadata extracted from BDMV."""

    upc: str | None = None
    ean: str | None = None
    title: str | None = None
    language: str | None = None
    region: str | None = None


class BDMVMetadataParser:
    """Parser for BDMV metadata files containing UPC/EAN codes."""

    def __init__(self) -> None:
        self.logger = logging.getLogger(__name__)

    def parse_disc_metadata(self, disc_path: Path) -> DiscMetadata | None:
        """Parse BDMV metadata from disc path.

        Args:
            disc_path: Path to mounted disc or BDMV directory

        Returns:
            DiscMetadata object with extracted information or None
        """
        # Check for BDMV structure
        bdmv_path = self._find_bdmv_path(disc_path)
        if not bdmv_path:
            self.logger.debug(f"No BDMV structure found at {disc_path}")
            return None

        # Look for metadata directory
        meta_path = bdmv_path / "META" / "DL"
        if not meta_path.exists():
            self.logger.debug(f"No META/DL directory found at {bdmv_path}")
            return None

        # Find and parse XML metadata files
        for xml_file in meta_path.glob("*.xml"):
            metadata = self._parse_xml_metadata(xml_file)
            if metadata and (metadata.upc or metadata.ean):
                self.logger.info(f"Found UPC/EAN metadata in {xml_file.name}")
                return metadata

        self.logger.debug(f"No UPC/EAN codes found in metadata at {meta_path}")
        return None

    def _find_bdmv_path(self, disc_path: Path) -> Path | None:
        """Find BDMV directory in disc structure."""
        # Direct BDMV directory
        if disc_path.name == "BDMV" and disc_path.is_dir():
            return disc_path

        # BDMV subdirectory
        bdmv_subdir = disc_path / "BDMV"
        if bdmv_subdir.exists() and bdmv_subdir.is_dir():
            return bdmv_subdir

        # Search for BDMV in subdirectories (some discs have extra structure)
        for subdir in disc_path.iterdir():
            if subdir.is_dir():
                bdmv_candidate = subdir / "BDMV"
                if bdmv_candidate.exists() and bdmv_candidate.is_dir():
                    return bdmv_candidate

        return None

    def _parse_xml_metadata(self, xml_file: Path) -> DiscMetadata | None:
        """Parse individual XML metadata file."""
        try:
            tree = ElementTree.parse(xml_file)
            root = tree.getroot()

            if root is None:
                self.logger.warning(f"XML file {xml_file} has no root element")
                return None

            metadata = DiscMetadata()

            # Search for UPC/EAN in various XML structures
            # Common patterns in BDMV metadata files
            upc_ean = self._extract_upc_ean(root)
            if upc_ean:
                if len(upc_ean) == 12:
                    metadata.upc = upc_ean
                elif len(upc_ean) == 13:
                    metadata.ean = upc_ean
                else:
                    # Could be either format with different encoding
                    metadata.upc = upc_ean

            # Extract other useful metadata
            metadata.title = self._extract_text_value(
                root,
                ["title", "name", "product_name"],
            )
            metadata.language = self._extract_text_value(root, ["language", "lang"])
            metadata.region = self._extract_text_value(root, ["region", "territory"])

            return metadata

        except ElementTree.ParseError as e:
            self.logger.warning(f"Failed to parse XML file {xml_file}: {e}")
            return None
        except Exception as e:
            self.logger.warning(f"Unexpected error parsing {xml_file}: {e}")
            return None

    def _extract_upc_ean(self, root: Element) -> str | None:
        """Extract UPC or EAN code from XML element tree."""
        # Common UPC/EAN tag names and patterns
        upc_patterns = [
            # Direct tag names
            "upc",
            "ean",
            "barcode",
            "product_code",
            "catalog_number",
            # Attribute patterns
            "code",
            "id",
            "identifier",
            "product_id",
            # Nested patterns
            "product/upc",
            "product/ean",
            "product/code",
            "disc/upc",
            "disc/ean",
            "disc/code",
            "metadata/upc",
            "metadata/ean",
            "metadata/code",
        ]

        # Search by tag names
        for pattern in upc_patterns:
            if "/" in pattern:
                # Handle nested paths
                parts = pattern.split("/")
                element: Element | None = root
                for part in parts:
                    if element is not None:
                        element = element.find(part)
                    if element is None:
                        break
                else:
                    # Only reached if we didn't break out of the loop
                    if element is not None and element.text:
                        code = self._clean_code(element.text)
                        if self._is_valid_upc_ean(code):
                            return code
            else:
                # Direct tag search
                elements = root.findall(f".//{pattern}")
                for element in elements:
                    if element.text:
                        code = self._clean_code(element.text)
                        if self._is_valid_upc_ean(code):
                            return code

        # Search by attributes
        for element in root.iter():
            for attr_name, attr_value in element.attrib.items():
                if any(
                    pattern in attr_name.lower()
                    for pattern in ["upc", "ean", "code", "barcode"]
                ):
                    code = self._clean_code(attr_value)
                    if self._is_valid_upc_ean(code):
                        return code

        # Search element text for numeric patterns
        for element in root.iter():
            if element.text:
                # Look for 12-13 digit sequences
                matches = re.findall(r"\b\d{12,13}\b", element.text)
                for match in matches:
                    if self._is_valid_upc_ean(match):
                        return str(match)

        return None

    def _extract_text_value(
        self,
        root: Element,
        tag_names: list[str],
    ) -> str | None:
        """Extract text value from first matching tag."""
        for tag_name in tag_names:
            if "/" in tag_name:
                # Handle nested paths
                parts = tag_name.split("/")
                element: Element | None = root
                for part in parts:
                    if element is not None:
                        element = element.find(part)
                    if element is None:
                        break
                else:
                    # Only reached if we didn't break out of the loop
                    if element is not None and element.text:
                        return element.text.strip()
            else:
                # Direct tag search
                element = root.find(f".//{tag_name}")
                if element is not None and element.text:
                    return element.text.strip()
        return None

    def _clean_code(self, code: str) -> str:
        """Clean and normalize UPC/EAN code."""
        # Remove non-digit characters
        return re.sub(r"[^\d]", "", code)

    def _is_valid_upc_ean(self, code: str) -> bool:
        """Validate UPC/EAN code format."""
        if not code or not code.isdigit():
            return False

        # UPC-A: 12 digits, EAN-13: 13 digits
        # Also accept 8-digit EAN-8 and 14-digit GTIN-14
        valid_lengths = [8, 12, 13, 14]
        return len(code) in valid_lengths
