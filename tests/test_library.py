"""Tests for library organization and Plex integration."""

import shutil
import tempfile
from pathlib import Path
from unittest.mock import Mock, patch

import pytest
from plexapi.exceptions import PlexApiException

from spindle.config import SpindleConfig
from spindle.identify.tmdb import MediaInfo
from spindle.organize.library import LibraryOrganizer


@pytest.fixture
def temp_dirs():
    """Create temporary directories for testing."""
    temp_dir = Path(tempfile.mkdtemp())
    dirs = {
        "library": temp_dir / "library",
        "review": temp_dir / "review",
        "staging": temp_dir / "staging",
    }

    for dir_path in dirs.values():
        dir_path.mkdir(parents=True, exist_ok=True)

    yield dirs

    # Cleanup
    shutil.rmtree(temp_dir)


@pytest.fixture
def mock_config(temp_dirs):
    """Create a mock configuration for testing."""
    config = Mock(spec=SpindleConfig)
    config.library_dir = temp_dirs["library"]
    config.review_dir = temp_dirs["review"]
    config.movies_dir = "Movies"
    config.tv_dir = "TV Shows"
    config.movies_library = "Movies"
    config.tv_library = "TV Shows"
    config.plex_url = "http://localhost:32400"
    config.plex_token = "test-token"
    config.plex_scan_interval = 1
    return config


@pytest.fixture
def mock_config_no_plex(temp_dirs):
    """Create a mock configuration without Plex setup."""
    config = Mock(spec=SpindleConfig)
    config.library_dir = temp_dirs["library"]
    config.review_dir = temp_dirs["review"]
    config.movies_dir = "Movies"
    config.tv_dir = "TV Shows"
    config.movies_library = "Movies"
    config.tv_library = "TV Shows"
    config.plex_url = None
    config.plex_token = None
    config.plex_scan_interval = 1
    return config


@pytest.fixture
def movie_info():
    """Create test movie MediaInfo."""
    return MediaInfo(
        title="Test Movie",
        year=2023,
        media_type="movie",
        tmdb_id=12345,
        overview="A test movie",
        genres=["Action", "Comedy"],
    )


@pytest.fixture
def tv_info():
    """Create test TV show MediaInfo."""
    return MediaInfo(
        title="Test TV Show",
        year=2023,
        media_type="tv",
        tmdb_id=67890,
        overview="A test TV show",
        genres=["Drama"],
        season=1,
        episode=3,
        episode_title="Test Episode",
    )


@pytest.fixture
def test_video_file(temp_dirs):
    """Create a test video file."""
    video_file = temp_dirs["staging"] / "test_movie.mkv"
    video_file.write_text("fake video content")
    return video_file


class TestLibraryOrganizerInit:
    """Test LibraryOrganizer initialization."""

    @patch("spindle.organize.library.PlexServer")
    def test_init_with_plex(self, mock_plex_server, mock_config):
        """Test initialization with Plex configuration."""
        mock_server = Mock()
        mock_server.friendlyName = "Test Plex Server"
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)

        assert organizer.config == mock_config
        assert organizer.plex_server == mock_server
        mock_plex_server.assert_called_once_with("http://localhost:32400", "test-token")

    @patch("spindle.organize.library.PlexServer")
    def test_init_plex_connection_failure(self, mock_plex_server, mock_config):
        """Test initialization when Plex connection fails."""
        mock_plex_server.side_effect = PlexApiException("Connection failed")

        organizer = LibraryOrganizer(mock_config)

        assert organizer.config == mock_config
        assert organizer.plex_server is None

    def test_init_no_plex_config(self, mock_config_no_plex):
        """Test initialization without Plex configuration."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        assert organizer.config == mock_config_no_plex
        assert organizer.plex_server is None


class TestOrganizeMedia:
    """Test media file organization."""

    def test_organize_movie(self, mock_config_no_plex, movie_info, test_video_file):
        """Test organizing a movie file."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        result_path = organizer.organize_media(test_video_file, movie_info)

        expected_path = (
            mock_config_no_plex.library_dir
            / "Movies"
            / "Test Movie (2023)"
            / "Test Movie (2023).mkv"
        )
        assert result_path == expected_path
        assert result_path.exists()
        assert not test_video_file.exists()  # Original should be moved
        assert result_path.read_text() == "fake video content"

    def test_organize_tv_show(self, mock_config_no_plex, tv_info, test_video_file):
        """Test organizing a TV show episode."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        result_path = organizer.organize_media(test_video_file, tv_info)

        expected_path = (
            mock_config_no_plex.library_dir
            / "TV Shows"
            / "Test TV Show (2023)"
            / "Season 01"
            / "Test TV Show - S01E03 - Test Episode.mkv"
        )
        assert result_path == expected_path
        assert result_path.exists()
        assert not test_video_file.exists()

    def test_organize_with_file_conflict(
        self, mock_config_no_plex, movie_info, temp_dirs,
    ):
        """Test organizing when target file already exists."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        # Create first file
        video_file1 = temp_dirs["staging"] / "test1.mkv"
        video_file1.write_text("content1")

        result1 = organizer.organize_media(video_file1, movie_info)

        # Create second file with same metadata
        video_file2 = temp_dirs["staging"] / "test2.mkv"
        video_file2.write_text("content2")

        result2 = organizer.organize_media(video_file2, movie_info)

        # First file should be original name
        expected1 = (
            mock_config_no_plex.library_dir
            / "Movies"
            / "Test Movie (2023)"
            / "Test Movie (2023).mkv"
        )
        assert result1 == expected1

        # Second file should have counter
        expected2 = (
            mock_config_no_plex.library_dir
            / "Movies"
            / "Test Movie (2023)"
            / "Test Movie (2023) (1).mkv"
        )
        assert result2 == expected2

        assert result1.exists()
        assert result2.exists()
        assert result1.read_text() == "content1"
        assert result2.read_text() == "content2"

    @patch("spindle.organize.library.shutil.move")
    def test_organize_media_move_failure(
        self, mock_move, mock_config_no_plex, movie_info, test_video_file,
    ):
        """Test handling of file move failure."""
        mock_move.side_effect = OSError("Permission denied")
        organizer = LibraryOrganizer(mock_config_no_plex)

        with pytest.raises(OSError):
            organizer.organize_media(test_video_file, movie_info)

    def test_organize_creates_directories(
        self, mock_config_no_plex, movie_info, test_video_file,
    ):
        """Test that organize_media creates necessary directories."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        # Ensure target directory doesn't exist initially
        target_dir = mock_config_no_plex.library_dir / "Movies" / "Test Movie (2023)"
        assert not target_dir.exists()

        organizer.organize_media(test_video_file, movie_info)

        assert target_dir.exists()
        assert target_dir.is_dir()


class TestPlexIntegration:
    """Test Plex server integration."""

    @patch("spindle.organize.library.PlexServer")
    def test_scan_plex_library_movie(self, mock_plex_server, mock_config, movie_info):
        """Test triggering Plex library scan for movie."""
        mock_server = Mock()
        mock_section = Mock()
        mock_section.update = Mock()

        mock_server.library.section.return_value = mock_section
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.scan_plex_library(movie_info)

        assert result is True
        mock_server.library.section.assert_called_once_with("Movies")
        mock_section.update.assert_called_once()

    @patch("spindle.organize.library.PlexServer")
    def test_scan_plex_library_tv(self, mock_plex_server, mock_config, tv_info):
        """Test triggering Plex library scan for TV show."""
        mock_server = Mock()
        mock_section = Mock()
        mock_section.update = Mock()

        mock_server.library.section.return_value = mock_section
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.scan_plex_library(tv_info)

        assert result is True
        mock_server.library.section.assert_called_once_with("TV Shows")
        mock_section.update.assert_called_once()

    def test_scan_plex_library_no_server(self, mock_config_no_plex, movie_info):
        """Test Plex scan when server is not configured."""
        organizer = LibraryOrganizer(mock_config_no_plex)
        result = organizer.scan_plex_library(movie_info)

        assert result is False

    @patch("spindle.organize.library.PlexServer")
    def test_scan_plex_library_error(self, mock_plex_server, mock_config, movie_info):
        """Test handling of Plex scan error."""
        mock_server = Mock()
        mock_server.library.section.side_effect = PlexApiException("Library not found")
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.scan_plex_library(movie_info)

        assert result is False


class TestAddToPlex:
    """Test complete add to Plex workflow."""

    @patch.object(LibraryOrganizer, "scan_plex_library")
    @patch.object(LibraryOrganizer, "organize_media")
    def test_add_to_plex_success(
        self, mock_organize, mock_scan, mock_config_no_plex, movie_info, test_video_file,
    ):
        """Test successful add to Plex workflow."""
        expected_path = mock_config_no_plex.library_dir / "organized_file.mkv"
        mock_organize.return_value = expected_path
        mock_scan.return_value = True

        organizer = LibraryOrganizer(mock_config_no_plex)
        result = organizer.add_to_plex(test_video_file, movie_info)

        assert result is True
        mock_organize.assert_called_once_with(test_video_file, movie_info)
        mock_scan.assert_called_once_with(movie_info)

    @patch.object(LibraryOrganizer, "organize_media")
    def test_add_to_plex_organize_failure(
        self, mock_organize, mock_config_no_plex, movie_info, test_video_file,
    ):
        """Test add to Plex when organize fails."""
        mock_organize.side_effect = OSError("File move failed")

        organizer = LibraryOrganizer(mock_config_no_plex)
        result = organizer.add_to_plex(test_video_file, movie_info)

        assert result is False


class TestCreateReviewDirectory:
    """Test review directory functionality."""

    def test_create_review_directory_default(
        self, mock_config_no_plex, test_video_file,
    ):
        """Test moving file to default review directory."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        result_path = organizer.create_review_directory(test_video_file)

        expected_path = (
            mock_config_no_plex.review_dir / "unidentified" / "test_movie.mkv"
        )
        assert result_path == expected_path
        assert result_path.exists()
        assert not test_video_file.exists()
        assert result_path.read_text() == "fake video content"

    def test_create_review_directory_custom_reason(
        self, mock_config_no_plex, test_video_file,
    ):
        """Test moving file to custom review directory."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        result_path = organizer.create_review_directory(
            test_video_file, "encoding_failed",
        )

        expected_path = (
            mock_config_no_plex.review_dir / "encoding_failed" / "test_movie.mkv"
        )
        assert result_path == expected_path
        assert result_path.exists()

    def test_create_review_directory_conflict(self, mock_config_no_plex, temp_dirs):
        """Test handling file name conflicts in review directory."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        # Create first file
        video_file1 = temp_dirs["staging"] / "conflict.mkv"
        video_file1.write_text("content1")

        result1 = organizer.create_review_directory(video_file1)

        # Create second file with same name
        video_file2 = temp_dirs["staging"] / "conflict.mkv"
        video_file2.write_text("content2")

        result2 = organizer.create_review_directory(video_file2)

        expected1 = mock_config_no_plex.review_dir / "unidentified" / "conflict.mkv"
        expected2 = mock_config_no_plex.review_dir / "unidentified" / "conflict_1.mkv"

        assert result1 == expected1
        assert result2 == expected2
        assert result1.exists()
        assert result2.exists()
        assert result1.read_text() == "content1"
        assert result2.read_text() == "content2"

    @patch("spindle.organize.library.shutil.move")
    def test_create_review_directory_move_failure(
        self, mock_move, mock_config_no_plex, test_video_file,
    ):
        """Test handling of move failure to review directory."""
        mock_move.side_effect = OSError("Permission denied")
        organizer = LibraryOrganizer(mock_config_no_plex)

        with pytest.raises(OSError):
            organizer.create_review_directory(test_video_file)


class TestPlexConnection:
    """Test Plex connection verification and utilities."""

    @patch("spindle.organize.library.PlexServer")
    def test_verify_plex_connection_success(self, mock_plex_server, mock_config):
        """Test successful Plex connection verification."""
        mock_server = Mock()
        mock_server.friendlyName = "Test Server"
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.verify_plex_connection()

        assert result is True

    def test_verify_plex_connection_no_server(self, mock_config_no_plex):
        """Test Plex connection verification when not configured."""
        organizer = LibraryOrganizer(mock_config_no_plex)
        result = organizer.verify_plex_connection()

        assert result is False

    @patch("spindle.organize.library.PlexServer")
    def test_verify_plex_connection_error(self, mock_plex_server, mock_config):
        """Test Plex connection verification error handling."""
        mock_server = Mock()

        def raise_exception():
            msg = "Server error"
            raise PlexApiException(msg)

        type(mock_server).friendlyName = property(lambda _: raise_exception())
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.verify_plex_connection()

        assert result is False

    @patch("spindle.organize.library.PlexServer")
    def test_get_plex_libraries_success(self, mock_plex_server, mock_config):
        """Test getting Plex library list."""
        mock_server = Mock()
        mock_section1 = Mock()
        mock_section1.title = "Movies"
        mock_section2 = Mock()
        mock_section2.title = "TV Shows"

        mock_server.library.sections.return_value = [mock_section1, mock_section2]
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.get_plex_libraries()

        assert result == ["Movies", "TV Shows"]

    def test_get_plex_libraries_no_server(self, mock_config_no_plex):
        """Test getting Plex libraries when not configured."""
        organizer = LibraryOrganizer(mock_config_no_plex)
        result = organizer.get_plex_libraries()

        assert result == []

    @patch("spindle.organize.library.PlexServer")
    def test_get_plex_libraries_error(self, mock_plex_server, mock_config):
        """Test handling error when getting Plex libraries."""
        mock_server = Mock()
        mock_server.library.sections.side_effect = PlexApiException("Connection error")
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.get_plex_libraries()

        assert result == []


class TestWaitForPlexScan:
    """Test waiting for Plex scan completion."""

    @patch("time.sleep")
    @patch("time.time")
    @patch("spindle.organize.library.PlexServer")
    def test_wait_for_plex_scan_movie_found(
        self, mock_plex_server, mock_time, mock_sleep, mock_config, movie_info,
    ):
        """Test waiting for Plex scan when movie is found quickly."""
        mock_time.side_effect = [0, 5]  # Start time, then 5 seconds elapsed

        mock_server = Mock()
        mock_library = Mock()
        mock_result = Mock()
        mock_library.search.return_value = [mock_result]
        mock_server.library.section.return_value = mock_library
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.wait_for_plex_scan(movie_info, timeout=60)

        assert result is True
        mock_library.search.assert_called_with(title="Test Movie", year=2023)
        mock_sleep.assert_not_called()  # Found immediately, no sleep needed

    @patch("time.sleep")
    @patch("time.time")
    @patch("spindle.organize.library.PlexServer")
    def test_wait_for_plex_scan_tv_found(
        self, mock_plex_server, mock_time, mock_sleep, mock_config, tv_info,
    ):
        """Test waiting for Plex scan when TV show is found."""
        mock_time.side_effect = [0, 5]

        mock_server = Mock()
        mock_library = Mock()
        mock_result = Mock()
        mock_library.search.return_value = [mock_result]
        mock_server.library.section.return_value = mock_library
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.wait_for_plex_scan(tv_info, timeout=60)

        assert result is True
        mock_library.search.assert_called_with(title="Test TV Show")

    @patch("time.sleep")
    @patch("time.time")
    @patch("spindle.organize.library.PlexServer")
    def test_wait_for_plex_scan_timeout(
        self, mock_plex_server, mock_time, mock_sleep, mock_config, movie_info,
    ):
        """Test timeout when waiting for Plex scan."""
        mock_time.side_effect = [0, 30, 65]  # Start, middle, timeout

        mock_server = Mock()
        mock_library = Mock()
        mock_library.search.return_value = []  # Never found
        mock_server.library.section.return_value = mock_library
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.wait_for_plex_scan(movie_info, timeout=60)

        assert result is False
        assert mock_sleep.called

    def test_wait_for_plex_scan_no_server(self, mock_config_no_plex, movie_info):
        """Test waiting for Plex scan when server not configured."""
        organizer = LibraryOrganizer(mock_config_no_plex)
        result = organizer.wait_for_plex_scan(movie_info)

        assert result is False

    @patch("time.sleep")
    @patch("time.time")
    @patch("spindle.organize.library.PlexServer")
    def test_wait_for_plex_scan_error_handling(
        self, mock_plex_server, mock_time, mock_sleep, mock_config, movie_info,
    ):
        """Test error handling during Plex scan waiting."""
        mock_time.side_effect = [0, 65]  # Timeout immediately to avoid infinite loop

        mock_server = Mock()
        mock_library = Mock()
        mock_library.search.side_effect = PlexApiException("Search failed")
        mock_server.library.section.return_value = mock_library
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.wait_for_plex_scan(movie_info, timeout=60)

        assert result is False


class TestIntegration:
    """Integration tests combining multiple components."""

    @patch("spindle.organize.library.PlexServer")
    def test_full_workflow_movie(
        self, mock_plex_server, mock_config, movie_info, test_video_file,
    ):
        """Test complete workflow for adding a movie."""
        # Setup Plex mock
        mock_server = Mock()
        mock_library = Mock()
        mock_library.update = Mock()
        mock_server.library.section.return_value = mock_library
        mock_plex_server.return_value = mock_server

        organizer = LibraryOrganizer(mock_config)
        result = organizer.add_to_plex(test_video_file, movie_info)

        assert result is True
        # Check file was organized
        expected_path = (
            mock_config.library_dir
            / "Movies"
            / "Test Movie (2023)"
            / "Test Movie (2023).mkv"
        )
        assert expected_path.exists()
        # Check Plex scan was triggered
        mock_library.update.assert_called_once()

    def test_unidentified_media_workflow(self, mock_config_no_plex, test_video_file):
        """Test workflow for unidentified media."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        result_path = organizer.create_review_directory(
            test_video_file, "identification_failed",
        )

        expected_path = (
            mock_config_no_plex.review_dir / "identification_failed" / "test_movie.mkv"
        )
        assert result_path == expected_path
        assert result_path.exists()
        assert not test_video_file.exists()


class TestEdgeCases:
    """Test edge cases and error conditions."""

    def test_organize_media_with_special_characters_in_filename(
        self, mock_config_no_plex, temp_dirs,
    ):
        """Test organizing media with special characters in filename."""
        special_video = temp_dirs["staging"] / "test: movie [2023] & more!.mkv"
        special_video.write_text("content")

        media_info = MediaInfo(
            title="Test: Movie & More!", year=2023, media_type="movie", tmdb_id=12345,
        )

        organizer = LibraryOrganizer(mock_config_no_plex)
        result_path = organizer.organize_media(special_video, media_info)

        # Should handle special characters appropriately
        assert result_path.exists()
        assert "Test Movie More" in str(result_path)  # Special chars removed/replaced

    def test_multiple_file_conflicts(self, mock_config_no_plex, movie_info, temp_dirs):
        """Test handling multiple file conflicts."""
        organizer = LibraryOrganizer(mock_config_no_plex)

        # Create multiple files that would conflict
        files = []
        for i in range(3):
            video_file = temp_dirs["staging"] / f"test{i}.mkv"
            video_file.write_text(f"content{i}")
            files.append(video_file)

        results = []
        for video_file in files:
            result = organizer.organize_media(video_file, movie_info)
            results.append(result)

        # Check all files were organized with proper numbering
        assert len(results) == 3
        assert results[0].name == "Test Movie (2023).mkv"
        assert results[1].name == "Test Movie (2023) (1).mkv"
        assert results[2].name == "Test Movie (2023) (2).mkv"

        for result in results:
            assert result.exists()
