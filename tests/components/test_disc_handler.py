"""Tests for the simplified disc handler."""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import AsyncMock, Mock, patch

import pytest

from spindle.components.disc_handler import DiscHandler
from spindle.disc.analyzer import DiscAnalysisResult
from spindle.disc.monitor import DiscInfo
from spindle.services.tmdb import MediaInfo
from spindle.storage.queue import QueueItem, QueueItemStatus
from spindle.config import SpindleConfig


@pytest.fixture
def config(tmp_path: Path) -> SpindleConfig:
    return SpindleConfig(
        staging_dir=tmp_path / "staging",
        tmdb_api_key="test",
        enable_enhanced_disc_metadata=False,
    )


@pytest.fixture
def handler(config: SpindleConfig) -> DiscHandler:
    with patch("spindle.components.disc_handler.IntelligentDiscAnalyzer") as analyzer_cls, \
         patch("spindle.components.disc_handler.MakeMKVService") as makemkv_cls:

        analyzer = analyzer_cls.return_value
        analyzer.analyze_disc = AsyncMock()

        ripper = makemkv_cls.return_value
        ripper.scan_disc = AsyncMock(
            return_value={
                "titles": [],
                "fingerprint": "f1",
                "makemkv_output": "",
            },
        )
        ripper.rip_disc = AsyncMock(return_value=[Path("/tmp/output.mkv")])

        disc_handler = DiscHandler(config)
        disc_handler.queue_manager = Mock()

        return disc_handler


@pytest.mark.asyncio
async def test_identify_disc_sets_review_when_unmatched(handler: DiscHandler):
    item = QueueItem(item_id=1, disc_title="Unknown Disc")
    disc_info = DiscInfo(device="/dev/sr0", disc_type="blu-ray", label="UNKNOWN")

    handler.disc_analyzer.analyze_disc.return_value = DiscAnalysisResult(
        disc_info=disc_info,
        primary_title="UNKNOWN",
        content_type="movie",
        confidence=0.5,
        titles_to_rip=[],
        commentary_tracks={},
        episode_mappings={},
        media_info=None,
        runtime_hint=None,
    )

    await handler.identify_disc(item, disc_info)

    assert item.status == QueueItemStatus.REVIEW
    assert item.error_message == "Identification requires manual review"


@pytest.mark.asyncio
async def test_identify_disc_success(handler: DiscHandler):
    item = QueueItem(item_id=1, disc_title="Test Disc")
    disc_info = DiscInfo(device="/dev/sr0", disc_type="blu-ray", label="TEST_DISC")

    media_info = MediaInfo(title="Test Movie", year=2020, media_type="movie", tmdb_id=1)
    analysis_result = DiscAnalysisResult(
        disc_info=disc_info,
        primary_title="Test Movie",
        content_type="movie",
        confidence=0.8,
        titles_to_rip=[],
        commentary_tracks={},
        episode_mappings={},
        media_info=media_info,
        runtime_hint=120,
    )

    handler.disc_analyzer.analyze_disc.return_value = analysis_result

    await handler.identify_disc(item, disc_info)

    assert item.status == QueueItemStatus.IDENTIFIED
    assert item.media_info.title == "Test Movie"


@pytest.mark.asyncio
async def test_rip_identified_item(handler: DiscHandler, tmp_path: Path):
    rip_spec_data = {
        "analysis_result": {
            "content_type": "movie",
            "confidence": 0.8,
            "primary_title": "Test",
            "titles_to_rip": [
                {
                    "title_id": "1",
                    "name": "Title 1",
                    "duration": 3600,
                    "chapters": 20,
                    "commentary_track_ids": [],
                },
            ],
            "episode_mappings": {},
        },
        "disc_info": {
            "label": "TEST",
            "device": "/dev/sr0",
            "disc_type": "blu-ray",
            "fingerprint": "f1",
        },
        "media_info": {
            "title": "Test Movie",
            "year": 2020,
            "media_type": "movie",
            "tmdb_id": 1,
        },
        "commentary_tracks": {},
        "is_multi_disc": False,
    }

    item = QueueItem(
        item_id=1,
        disc_title="Test",
        rip_spec_data=json.dumps(rip_spec_data),
    )

    handler.queue_manager = Mock()

    with patch("spindle.components.disc_handler.eject_disc") as eject_mock:
        await handler.rip_identified_item(item)

    handler.ripper.rip_disc.assert_awaited_once()
    eject_mock.assert_called_once()
    assert item.status == QueueItemStatus.RIPPED
