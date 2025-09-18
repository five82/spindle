"""Tests for the simplified TMDB service."""

from __future__ import annotations

from unittest.mock import AsyncMock

import pytest

from spindle.config import SpindleConfig
from spindle.services.tmdb import TMDBService


@pytest.fixture
def config(tmp_path):
    return SpindleConfig(
        staging_dir=tmp_path / "staging",
        tmdb_api_key="dummy",
        enable_enhanced_disc_metadata=False,
    )


@pytest.mark.asyncio
async def test_identify_media_movie(config):
    service = TMDBService(config)

    async def fake_request(endpoint, params):
        if endpoint == "/search/movie":
            return {"results": [{"id": 1, "title": "Example"}]}
        if endpoint == "/movie/1":
            return {
                "title": "Example",
                "release_date": "2020-01-01",
                "overview": "",
                "genres": [],
                "runtime": 110,
            }
        return {}

    service._request = AsyncMock(side_effect=fake_request)

    media_info = await service.identify_media("Example", "movie")
    assert media_info is not None
    assert media_info.title == "Example"
    assert media_info.media_type == "movie"
    assert media_info.confidence > 0


@pytest.mark.asyncio
async def test_identify_media_tv(config):
    service = TMDBService(config)

    async def fake_request(endpoint, params):
        if endpoint == "/search/tv":
            return {"results": [{"id": 5, "name": "Example Show"}]}
        if endpoint == "/tv/5":
            return {
                "name": "Example Show",
                "first_air_date": "2010-01-01",
                "overview": "",
                "episode_run_time": [45],
                "genres": [],
                "number_of_seasons": 2,
                "seasons": [
                    {"season_number": 1},
                    {"season_number": 2},
                ],
            }
        if endpoint == "/tv/5/season/1":
            return {
                "episodes": [
                    {
                        "season_number": 1,
                        "episode_number": 1,
                        "name": "Pilot",
                        "runtime": 44,
                    },
                ],
            }
        return {}

    service._request = AsyncMock(side_effect=fake_request)

    media_info = await service.identify_media("Example Show", "tv", season_hint=1)
    assert media_info is not None
    assert media_info.media_type == "tv"
    assert media_info.season == 1
    assert media_info.episodes


@pytest.mark.asyncio
async def test_identify_media_empty_results(config):
    service = TMDBService(config)
    service._request = AsyncMock(return_value={"results": []})

    media_info = await service.identify_media("Missing", "movie")
    assert media_info is None
