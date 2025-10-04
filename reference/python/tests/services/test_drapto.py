"""Test Drapto service wrapper."""

import pytest
from unittest.mock import Mock
from pathlib import Path

from spindle.services.drapto import DraptoService
from spindle.config import SpindleConfig


class TestDraptoService:
    """Test DraptoService functionality."""

    @pytest.fixture
    def config(self, tmp_path):
        """Create test configuration."""
        return SpindleConfig(staging_dir=tmp_path / "staging")

    @pytest.fixture
    def service(self, config):
        """Create Drapto service instance."""
        return DraptoService(config)

    def test_service_initialization(self, service, config):
        """Test service initializes with config."""
        assert service.config == config
        assert service.encoder is not None

    def test_has_encoder_instance(self, service):
        """Test service has DraptoEncoder instance."""
        assert service.encoder is not None