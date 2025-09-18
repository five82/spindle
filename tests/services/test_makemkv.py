"""Test MakeMKV service wrapper."""

from pathlib import Path
from unittest.mock import Mock

import pytest

from spindle.services.makemkv import MakeMKVService
from spindle.config import SpindleConfig


class TestMakeMKVService:
    """Test MakeMKVService functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(staging_dir=tmp_path / "staging")

    @pytest.fixture
    def service(self, config):
        """Create MakeMKV service instance."""
        return MakeMKVService(config)

    def test_service_initialization(self, service, config):
        """Test service initializes with config."""
        assert service.config == config
        assert service.ripper is not None

    def test_has_ripper_instance(self, service):
        """Test service has MakeMKVRipper instance."""
        assert service.ripper is not None

    @pytest.mark.asyncio
    async def test_scan_disc_includes_fingerprint(self, service, monkeypatch):
        """scan_disc returns the MakeMKV fingerprint in the payload."""

        sample_titles = [object()]
        sample_output = 'CINFO:32,0,"ABCDEF1234567890"'

        monkeypatch.setattr(
            service.ripper,
            "scan_disc_with_output",
            lambda device: (sample_titles, sample_output),
        )
        monkeypatch.setattr(
            service.ripper,
            "extract_disc_fingerprint",
            lambda output: "ABCDEF1234567890",
        )

        result = await service.scan_disc("/dev/sr0")

        assert result["fingerprint"] == "ABCDEF1234567890"
        assert result["titles"] == sample_titles

    @pytest.mark.asyncio
    async def test_scan_disc_raises_when_fingerprint_missing(
        self,
        service,
        monkeypatch,
    ):
        """scan_disc aborts if MakeMKV does not emit a fingerprint."""

        sample_titles = [object()]

        monkeypatch.setattr(
            service.ripper,
            "scan_disc_with_output",
            lambda device: (sample_titles, ""),
        )
        monkeypatch.setattr(
            service.ripper,
            "extract_disc_fingerprint",
            lambda output: None,
        )

        with pytest.raises(RuntimeError):
            await service.scan_disc("/dev/sr0")
