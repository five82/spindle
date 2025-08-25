"""Tests for content type handling (movies, TV series, etc.)."""

import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest

from spindle.config import SpindleConfig
from spindle.disc.analyzer import ContentPattern, ContentType, EpisodeInfo
from spindle.disc.monitor import DiscInfo
from spindle.disc.ripper import Title
from spindle.processor import ContinuousProcessor


class TestProcessorContentHandlers:
    """Test content type handling methods in detail."""

    @pytest.fixture
    def temp_config(self):
        """Create temporary configuration for testing."""
        with tempfile.TemporaryDirectory() as tmpdir:
            config = SpindleConfig(
                log_dir=Path(tmpdir) / "logs",
                staging_dir=Path(tmpdir) / "staging", 
                library_dir=Path(tmpdir) / "library",
            )
            config.ensure_directories()
            yield config

    @pytest.fixture
    def processor_with_mocks(self, temp_config):
        """Create processor with individual component mocks."""
        with patch.multiple(
            'spindle.processor',
            QueueManager=Mock,
            MakeMKVRipper=Mock,
            MediaIdentifier=Mock,
            DraptoEncoder=Mock,
            LibraryOrganizer=Mock,
            NtfyNotifier=Mock,
            IntelligentDiscAnalyzer=Mock,
            TVSeriesDiscAnalyzer=Mock,
        ):
            processor = ContinuousProcessor(temp_config)
            
            # Configure mocks for content handler tests
            processor.ripper.rip_title = Mock()
            processor.ripper.select_main_title = Mock()
            processor.tv_analyzer.analyze_tv_disc = Mock()  # Regular mock, not AsyncMock
            
            yield processor

    @pytest.fixture
    def sample_disc_info(self):
        """Sample disc info."""
        return DiscInfo(
            device="/dev/sr0",
            disc_type="BD",
            label="TEST_TV_SERIES",
        )

    @pytest.fixture
    def sample_tv_titles(self):
        """Sample TV series titles."""
        return [
            Title(title_id="1", name="Episode 1", duration=2700, chapters=12, size=2_000_000_000, tracks=[]),
            Title(title_id="2", name="Episode 2", duration=2650, chapters=12, size=2_100_000_000, tracks=[]),
            Title(title_id="3", name="Episode 3", duration=2720, chapters=12, size=1_950_000_000, tracks=[]),
        ]

    @pytest.fixture
    def sample_movie_titles(self):
        """Sample movie titles."""
        return [
            Title(title_id="1", name="Main Feature", duration=7200, chapters=24, size=20_000_000_000, tracks=[]),
            Title(title_id="2", name="Behind the Scenes", duration=1800, chapters=6, size=1_000_000_000, tracks=[]),
            Title(title_id="3", name="Trailer", duration=180, chapters=1, size=50_000_000, tracks=[]),
        ]

    def test_handle_tv_series_success(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test successful TV series handling."""
        # Mock episode mapping
        episode_mapping = {
            sample_tv_titles[0]: EpisodeInfo(
                episode_title="Pilot",
                season_number=1,
                episode_number=1,
            ),
            sample_tv_titles[1]: EpisodeInfo(
                episode_title="The Plan",
                season_number=1,
                episode_number=2,
            ),
        }

        processor_with_mocks.ripper.rip_title.side_effect = [
            Path("/staging/episodes/S01E01.mkv"),
            Path("/staging/episodes/S01E02.mkv"),
        ]

        # Mock asyncio.run to return episode_mapping directly
        with patch('asyncio.run') as mock_asyncio_run:
            mock_asyncio_run.return_value = episode_mapping
            
            result = processor_with_mocks._handle_tv_series(sample_disc_info, sample_tv_titles)

            assert len(result) == 2
            assert all(isinstance(path, Path) for path in result)
            assert processor_with_mocks.ripper.rip_title.call_count == 2

    def test_handle_tv_series_no_mapping(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test TV series handling when no episode mapping is found."""
        with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
            mock_basic_rip.return_value = [Path("/staging/basic.mkv")]
            
            with patch('asyncio.run') as mock_asyncio_run:
                mock_asyncio_run.return_value = None
                
                result = processor_with_mocks._handle_tv_series(sample_disc_info, sample_tv_titles)
                
                mock_basic_rip.assert_called_once()
                assert result == [Path("/staging/basic.mkv")]

    def test_handle_tv_series_error(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test TV series handling with error."""
        with patch('asyncio.run') as mock_asyncio_run:
            mock_asyncio_run.side_effect = Exception("Analysis failed")
            
            with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
                mock_basic_rip.return_value = [Path("/staging/fallback.mkv")]
                
                result = processor_with_mocks._handle_tv_series(sample_disc_info, sample_tv_titles)
                
                mock_basic_rip.assert_called_once()
                assert result == [Path("/staging/fallback.mkv")]

    def test_handle_cartoon_collection(self, processor_with_mocks, sample_disc_info, sample_tv_titles):
        """Test handling cartoon collection."""
        content_pattern = ContentPattern(
            type=ContentType.CARTOON_COLLECTION,
            confidence=0.85,
            episode_count=3,
        )
        
        processor_with_mocks.ripper.rip_title.side_effect = [
            Path(f"/staging/cartoon_{i}.mkv") for i in range(3)
        ]
        
        # Set cartoon duration config to match our test titles (around 45 minutes)
        processor_with_mocks.config.cartoon_min_duration = 40  # 40 minutes
        processor_with_mocks.config.cartoon_max_duration = 50  # 50 minutes
        
        result = processor_with_mocks._handle_cartoon_collection(
            sample_disc_info, sample_tv_titles, content_pattern
        )
        
        assert len(result) == 3
        assert processor_with_mocks.ripper.rip_title.call_count == 3

    def test_handle_movie_basic(self, processor_with_mocks, sample_disc_info, sample_movie_titles):
        """Test handling movie content - main feature only."""
        content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )
        
        processor_with_mocks.ripper.select_main_title.return_value = sample_movie_titles[0]
        processor_with_mocks.ripper.rip_title.return_value = Path("/staging/movie.mkv")
        processor_with_mocks.config.include_movie_extras = False
        
        result = processor_with_mocks._handle_movie(
            sample_disc_info, sample_movie_titles, content_pattern
        )
        
        assert len(result) == 1
        assert result[0] == Path("/staging/movie.mkv")
        processor_with_mocks.ripper.rip_title.assert_called_once()

    def test_handle_movie_with_extras(self, processor_with_mocks, sample_disc_info, sample_movie_titles):
        """Test handling movie with extras enabled."""
        content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )
        
        processor_with_mocks.ripper.select_main_title.return_value = sample_movie_titles[0]
        processor_with_mocks.ripper.rip_title.side_effect = [
            Path("/staging/movie.mkv"),
            Path("/staging/behind_scenes.mkv"),
        ]
        processor_with_mocks.config.include_movie_extras = True
        processor_with_mocks.config.max_extras_duration = 10  # 10 minutes minimum
        
        result = processor_with_mocks._handle_movie(
            sample_disc_info, sample_movie_titles, content_pattern
        )
        
        # Should rip main feature and behind the scenes (>10 min), but not trailer
        assert len(result) == 2
        assert processor_with_mocks.ripper.rip_title.call_count == 2

    def test_handle_movie_no_main_title(self, processor_with_mocks, sample_disc_info, sample_movie_titles):
        """Test handling movie when no main title is found."""
        content_pattern = ContentPattern(
            type=ContentType.MOVIE,
            confidence=0.95,
        )
        
        processor_with_mocks.ripper.select_main_title.return_value = None
        
        with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
            mock_basic_rip.return_value = [Path("/staging/fallback.mkv")]
            
            result = processor_with_mocks._handle_movie(
                sample_disc_info, sample_movie_titles, content_pattern
            )
            
            mock_basic_rip.assert_called_once()
            assert result == [Path("/staging/fallback.mkv")]

    def test_handle_basic_rip_success(self, processor_with_mocks, sample_disc_info, sample_movie_titles):
        """Test basic rip strategy."""
        processor_with_mocks.ripper.select_main_title.return_value = sample_movie_titles[0]
        processor_with_mocks.ripper.rip_title.return_value = Path("/staging/basic.mkv")
        
        result = processor_with_mocks._handle_basic_rip(sample_disc_info, sample_movie_titles)
        
        assert len(result) == 1
        assert result[0] == Path("/staging/basic.mkv")
        processor_with_mocks.ripper.select_main_title.assert_called_once_with(sample_movie_titles, sample_disc_info.label)
        processor_with_mocks.ripper.rip_title.assert_called_once()

    def test_handle_basic_rip_no_title(self, processor_with_mocks, sample_disc_info, sample_movie_titles):
        """Test basic rip when no main title found."""
        processor_with_mocks.ripper.select_main_title.return_value = None
        
        # Should raise RuntimeError when no title found
        with pytest.raises(RuntimeError, match="No suitable title found"):
            processor_with_mocks._handle_basic_rip(sample_disc_info, sample_movie_titles)
        
        processor_with_mocks.ripper.rip_title.assert_not_called()

    def test_handle_unknown_content_type(self, processor_with_mocks, sample_disc_info, sample_movie_titles):
        """Test handling unknown content type - falls back to basic rip."""
        content_pattern = ContentPattern(
            type=ContentType.UNKNOWN,
            confidence=0.2,
        )
        
        with patch.object(processor_with_mocks, '_handle_basic_rip') as mock_basic_rip:
            mock_basic_rip.return_value = [Path("/staging/unknown.mkv")]
            
            result = processor_with_mocks._handle_content_type(
                sample_disc_info, sample_movie_titles, content_pattern
            )
            
            mock_basic_rip.assert_called_once_with(
                sample_disc_info, sample_movie_titles, None
            )
            assert result == [Path("/staging/unknown.mkv")]