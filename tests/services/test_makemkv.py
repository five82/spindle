"""Test MakeMKV service wrapper."""

import pytest
from unittest.mock import Mock
from pathlib import Path

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